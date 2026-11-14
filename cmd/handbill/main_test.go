package main

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ferrule-io/handbill/internal/ratelimit"
	"github.com/ferrule-io/handbill/internal/server"
	"github.com/ferrule-io/handbill/internal/sites/sitestest"
	"github.com/ferrule-io/handbill/internal/token"
)

// handler builds the production handler and the routes it was built from.
func handler(t *testing.T, d *server.Deps) (http.Handler, *server.Routes) {
	t.Helper()
	var routes *server.Routes
	capture := func(r *server.Routes, _ *server.Deps) error { routes = r; return nil }
	h, err := server.New(d, append(append([]server.Module{}, modules...), capture)...)
	if err != nil {
		t.Fatal(err)
	}
	return h, routes
}

var wildcard = regexp.MustCompile(`\{[^}]+\}`)

// Every pattern in the rate-limit table must be a registered route, or
// its limit silently never applies.
func TestRateLimitRulesMatchRoutes(t *testing.T) {
	d := sitestest.Deps(t)
	_, routes := handler(t, d)
	for _, rule := range ratelimit.Rules {
		for _, p := range rule.Patterns {
			method, path, _ := strings.Cut(p, " ")
			req := httptest.NewRequest(method, "http://"+d.Config.AppHost+wildcard.ReplaceAllString(path, "x"), nil)
			if _, got := routes.App.Handler(req); got != p {
				t.Errorf("rule %s: pattern %q is served by %q", rule.Name, p, got)
			}
		}
	}
}

// The rate limits run inside the session middleware, so per-user rules see
// the signed-in user wherever the requests come from.
func TestRateLimitsSeeTheSessionUser(t *testing.T) {
	d := sitestest.Deps(t)
	h, _ := handler(t, d)
	user := sitestest.User(t, d, "a@example.com")
	plaintext, hash := token.New()
	if _, err := d.DB.Exec(t.Context(), `INSERT INTO sessions (id, token_hash, user_id, expires_at) VALUES ($1, $2, $3, $4)`,
		uuid.Must(uuid.NewV7()), hash, user, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	var codes []int
	for i := range 25 {
		req := httptest.NewRequest("POST", sitestest.AppOrigin+"/app-api/invites/accept", strings.NewReader(`{}`))
		req.RemoteAddr = "198.51.100." + string(rune('0'+i%10)) + ":1234"
		req.AddCookie(&http.Cookie{Name: "__Host-session", Value: plaintext})
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		codes = append(codes, w.Code)
	}
	if codes[len(codes)-1] != http.StatusTooManyRequests {
		t.Fatalf("per-user limit not applied across addresses: %v", codes)
	}
}
