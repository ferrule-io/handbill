// Package appapi is the JSON API the UI calls under /app-api/ (session
// cookie authenticated, CSRF protected; internal to the UI, not a public
// API). Feature packages register endpoints with Handle; appapi enforces
// the session, Origin, and CSRF rules of docs/design.md#csrf-and-cors and
// renders errors as {"error": {"code": ..., "message": ...}}.
package appapi

import (
	"bytes"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"

	"github.com/ferrule-io/handbill/internal/identity"
	"github.com/ferrule-io/handbill/internal/server"
)

// MaxBodyBytes caps request bodies read by ReadJSON.
const MaxBodyBytes = 1 << 20

// CSRFCookie holds the browser's CSRF secret. The __Host- prefix keeps site
// hosts (same-site subdomains) from setting or overwriting it.
const CSRFCookie = "__Host-csrf"

// CSRFHeader carries the token returned by GET /app-api/me.
const CSRFHeader = "X-CSRF-Token"

// Handler serves one endpoint. A returned *Error is rendered with its
// status; any other error is logged and answered with a generic 500.
type Handler func(w http.ResponseWriter, r *http.Request) error

// Option adjusts an endpoint registered with Handle.
type Option func(*options)

type options struct{ public bool }

// Public lets requests without a signed-in user through. Unsafe methods
// still require a matching Origin and CSRF token.
func Public() Option { return func(o *options) { o.public = true } }

// Error is an API error rendered as {"error": {"code": ..., "message": ...}}.
type Error struct {
	Status        int
	Code, Message string
}

func (e *Error) Error() string { return fmt.Sprintf("%s (%d): %s", e.Code, e.Status, e.Message) }

// NewError returns an *Error.
func NewError(status int, code, message string) *Error {
	return &Error{Status: status, Code: code, Message: message}
}

// Handle registers a JSON endpoint on the app mux. Every request needs a
// session unless Public() is passed; unsafe methods also require
// Origin == APP_ORIGIN and a valid X-CSRF-Token header.
func Handle(r *server.Routes, d *server.Deps, pattern string, h Handler, opts ...Option) {
	var o options
	for _, opt := range opts {
		opt(&o)
	}
	r.App.Handle(pattern, handler(d, h, o))
}

func handler(d *server.Deps, h Handler, o options) http.Handler {
	origin := d.Config.AppOrigin.String()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if err := check(d, r, origin, o); err != nil {
			writeError(w, err)
			return
		}
		if err := h(w, r); err != nil {
			var apiErr *Error
			if !errors.As(err, &apiErr) {
				d.Logger.Error("app-api handler failed", "method", r.Method, "path", r.URL.Path, "err", err)
				apiErr = NewError(http.StatusInternalServerError, "internal", "Something went wrong on our side. Try again.")
			}
			writeError(w, apiErr)
		}
	})
}

func check(d *server.Deps, r *http.Request, origin string, o options) *Error {
	if !safeMethod(r.Method) {
		if r.Header.Get("Origin") != origin {
			return NewError(http.StatusForbidden, "bad_origin", "This request did not come from the app.")
		}
		if !validCSRF(d, r) {
			return NewError(http.StatusForbidden, "bad_csrf", "The page's security token is missing or stale. Reload and try again.")
		}
	}
	if !o.public {
		if _, ok := identity.FromContext(r.Context()); !ok {
			return NewError(http.StatusUnauthorized, "unauthenticated", "Sign in to continue.")
		}
	}
	return nil
}

func safeMethod(m string) bool {
	return m == http.MethodGet || m == http.MethodHead || m == http.MethodOptions
}

// CSRFToken returns the CSRF token for this browser, setting the CSRF
// cookie when the request has none. The token is an HMAC of the cookie
// value, so the HttpOnly cookie itself is never exposed to scripts.
func CSRFToken(w http.ResponseWriter, r *http.Request, d *server.Deps) string {
	if v, ok := csrfCookie(r); ok {
		return csrfMAC(d, v)
	}
	var b [32]byte
	_, _ = rand.Read(b[:])
	v := base64.RawURLEncoding.EncodeToString(b[:])
	http.SetCookie(w, &http.Cookie{
		Name:     CSRFCookie,
		Value:    v,
		Path:     "/",
		MaxAge:   400 * 24 * 60 * 60,
		Secure:   true,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
	return csrfMAC(d, v)
}

func csrfCookie(r *http.Request) (string, bool) {
	c, err := r.Cookie(CSRFCookie)
	if err != nil {
		return "", false
	}
	b, err := base64.RawURLEncoding.DecodeString(c.Value)
	if err != nil || len(b) != 32 {
		return "", false
	}
	return c.Value, true
}

func csrfMAC(d *server.Deps, cookie string) string {
	m := hmac.New(sha256.New, d.Config.SecretKey)
	m.Write([]byte("handbill/csrf\x00"))
	m.Write([]byte(cookie))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

func validCSRF(d *server.Deps, r *http.Request) bool {
	v, ok := csrfCookie(r)
	if !ok {
		return false
	}
	got := r.Header.Get(CSRFHeader)
	return got != "" && hmac.Equal([]byte(got), []byte(csrfMAC(d, v)))
}

// WriteJSON writes v as JSON with status.
func WriteJSON(w http.ResponseWriter, status int, v any) {
	h := w.Header()
	h.Set("Content-Type", "application/json")
	h.Set("X-Content-Type-Options", "nosniff")
	if h.Get("Cache-Control") == "" {
		h.Set("Cache-Control", "no-store")
	}
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, e *Error) {
	WriteJSON(w, e.Status, map[string]any{"error": map[string]string{"code": e.Code, "message": e.Message}})
}

// ReadJSON decodes a JSON request body into v. The body must be
// application/json, at most MaxBodyBytes, a single value, and contain no
// fields v does not declare. Failures are *Error values.
func ReadJSON(r *http.Request, v any) error {
	mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mt != "application/json" {
		return NewError(http.StatusUnsupportedMediaType, "unsupported_media_type", "Send the request body as application/json.")
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, MaxBodyBytes+1))
	if err != nil {
		return NewError(http.StatusBadRequest, "invalid_json", "The request body could not be read.")
	}
	if len(body) > MaxBodyBytes {
		return NewError(http.StatusRequestEntityTooLarge, "too_large", "The request body is too large.")
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return NewError(http.StatusBadRequest, "invalid_json", "The request body is not valid: "+err.Error())
	}
	if _, err := dec.Token(); err != io.EOF {
		return NewError(http.StatusBadRequest, "invalid_json", "The request body must be a single JSON value.")
	}
	return nil
}

// Register claims /app-api/ so unknown endpoints get a JSON 404 instead of
// the UI's 404 page. GET also covers HEAD; a bare "/app-api/" pattern would
// conflict with the UI's "GET /".
func Register(r *server.Routes, d *server.Deps) error {
	notFound := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeError(w, NewError(http.StatusNotFound, "not_found", "No such endpoint."))
	})
	for _, m := range []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"} {
		r.App.Handle(m+" /app-api/", notFound)
	}
	return nil
}
