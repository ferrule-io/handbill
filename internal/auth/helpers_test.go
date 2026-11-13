package auth

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-webauthn/webauthn/protocol/webauthncbor"
	"github.com/go-webauthn/webauthn/protocol/webauthncose"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ferrule-io/handbill/internal/appapi"
	"github.com/ferrule-io/handbill/internal/config"
	"github.com/ferrule-io/handbill/internal/db/dbtest"
	"github.com/ferrule-io/handbill/internal/identity"
	"github.com/ferrule-io/handbill/internal/mail"
	"github.com/ferrule-io/handbill/internal/server"
)

const (
	testOrigin = "http://dash.localhost:8083"
	testRPID   = "dash.localhost"
)

// env is a server wired like cmd/handbill (appapi + auth) on a fresh
// database, with a fake Google issuer and a recording mail sender.
type env struct {
	t      *testing.T
	h      http.Handler
	pool   *pgxpool.Pool
	google *fakeGoogle
	mail   *fakeMail
}

type envOpts struct {
	noGoogle bool
	mail     bool
}

func newEnv(t *testing.T, o envOpts) *env {
	t.Helper()
	pool := dbtest.Pool(t)
	e := &env{t: t, pool: pool, mail: &fakeMail{enabled: o.mail}}
	vars := map[string]string{
		"APP_ORIGIN":   testOrigin,
		"SITES_DOMAIN": "sites.localhost",
		"SECRET_KEY":   strings.Repeat("s", 32),
		"DATABASE_URL": "postgres://localhost/unused",
	}
	issuer := ""
	if !o.noGoogle {
		e.google = newFakeGoogle(t)
		issuer = e.google.srv.URL
		vars["GOOGLE_CLIENT_ID"] = "test-client"
		vars["GOOGLE_CLIENT_SECRET"] = "test-secret"
	}
	cfg, err := config.Load(func(k string) string { return vars[k] })
	if err != nil {
		t.Fatal(err)
	}
	d := &server.Deps{Config: cfg, Logger: slog.New(slog.DiscardHandler), DB: pool}
	h, err := server.New(d, appapi.Register, func(r *server.Routes, d *server.Deps) error {
		if err := register(r, d, options{googleIssuer: issuer, mail: e.mail}); err != nil {
			return err
		}
		// Stands in for any other feature reading identity.
		r.App.HandleFunc("GET /whoami", func(w http.ResponseWriter, r *http.Request) {
			u, ok := identity.FromContext(r.Context())
			if !ok {
				io.WriteString(w, "anonymous")
				return
			}
			io.WriteString(w, u.ID.String()+" "+u.Email)
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	e.h = h
	return e
}

func (e *env) exec(sql string, args ...any) {
	e.t.Helper()
	if _, err := e.pool.Exec(e.t.Context(), sql, args...); err != nil {
		e.t.Fatalf("exec %q: %v", sql, err)
	}
}

func (e *env) scalar(dst any, sql string, args ...any) {
	e.t.Helper()
	if err := e.pool.QueryRow(e.t.Context(), sql, args...).Scan(dst); err != nil {
		e.t.Fatalf("query %q: %v", sql, err)
	}
}

// browser keeps cookies like a browser on the app origin and sends the
// Origin and CSRF headers the UI's api() helper sends.
type browser struct {
	e       *env
	cookies map[string]string
	csrf    string
	ua      string
}

func (e *env) browser() *browser {
	return &browser{e: e, cookies: map[string]string{}, ua: "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0 Safari/537.36"}
}

type response struct {
	status int
	header http.Header
	body   []byte
}

func (r response) json(t *testing.T, v any) {
	t.Helper()
	if err := json.Unmarshal(r.body, v); err != nil {
		t.Fatalf("decode %s: %v", r.body, err)
	}
}

func (r response) errorCode(t *testing.T) string {
	t.Helper()
	var out struct{ Error struct{ Code string } }
	r.json(t, &out)
	return out.Error.Code
}

func (b *browser) raw(method, path string, body io.Reader, hdr map[string]string) response {
	b.e.t.Helper()
	req := httptest.NewRequest(method, testOrigin+path, body)
	req.Header.Set("User-Agent", b.ua)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	for k, v := range b.cookies {
		req.AddCookie(&http.Cookie{Name: k, Value: v})
	}
	rec := httptest.NewRecorder()
	b.e.h.ServeHTTP(rec, req)
	for _, c := range rec.Result().Cookies() {
		if !c.Secure || !c.HttpOnly || c.Path != "/" || c.Domain != "" {
			b.e.t.Fatalf("cookie %s lacks __Host- attributes: %+v", c.Name, c)
		}
		if c.MaxAge < 0 {
			delete(b.cookies, c.Name)
		} else {
			b.cookies[c.Name] = c.Value
		}
	}
	return response{status: rec.Code, header: rec.Header(), body: rec.Body.Bytes()}
}

// api sends a JSON request the way web/lib/api.ts does.
func (b *browser) api(method, path string, in any) response {
	b.e.t.Helper()
	if b.csrf == "" && method != "GET" {
		b.me()
	}
	var body io.Reader
	hdr := map[string]string{"Origin": testOrigin, appapi.CSRFHeader: b.csrf}
	if in != nil {
		j, err := json.Marshal(in)
		if err != nil {
			b.e.t.Fatal(err)
		}
		body = bytes.NewReader(j)
		hdr["Content-Type"] = "application/json"
	}
	return b.raw(method, path, body, hdr)
}

type meResponse struct {
	User *struct {
		ID            string
		Email         string
		EmailVerified bool
	}
	Recovery      bool
	CSRFToken     string
	GoogleEnabled bool
	MailEnabled   bool
}

func (b *browser) me() meResponse {
	b.e.t.Helper()
	res := b.raw("GET", "/app-api/me", nil, nil)
	if res.status != 200 {
		b.e.t.Fatalf("me: %d %s", res.status, res.body)
	}
	var out meResponse
	res.json(b.e.t, &out)
	b.csrf = out.CSRFToken
	return out
}

func (b *browser) whoami() string {
	return string(b.raw("GET", "/whoami", nil, nil).body)
}

func mustStatus(t *testing.T, what string, r response, status int) {
	t.Helper()
	if r.status != status {
		t.Fatalf("%s: status %d, want %d: %s", what, r.status, status, r.body)
	}
}

// authenticator is a software passkey: an ES256 key whose credential is
// discoverable (it stores the user handle).
type authenticator struct {
	key        *ecdsa.PrivateKey
	credID     []byte
	userHandle []byte
	counter    uint32
	backup     bool // BE and BS flags (a synced passkey)
	origin     string
}

func newAuthenticator() *authenticator {
	k, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	id := make([]byte, 16)
	_, _ = rand.Read(id)
	return &authenticator{key: k, credID: id, origin: testOrigin}
}

var b64 = base64.RawURLEncoding

func (a *authenticator) flags(at bool) byte {
	f := byte(0x01 | 0x04) // UP, UV
	if a.backup {
		f |= 0x08 | 0x10
	}
	if at {
		f |= 0x40
	}
	return f
}

func (a *authenticator) clientData(typ, challenge string) []byte {
	cd, _ := json.Marshal(map[string]any{"type": typ, "challenge": challenge, "origin": a.origin, "crossOrigin": false})
	return cd
}

// create answers a navigator.credentials.create() options object.
func (a *authenticator) create(t *testing.T, options json.RawMessage) map[string]any {
	t.Helper()
	var o struct {
		Challenge string
		RP        struct{ ID string }
		User      struct{ ID string }
	}
	if err := json.Unmarshal(options, &o); err != nil {
		t.Fatal(err)
	}
	if o.RP.ID != testRPID {
		t.Fatalf("rp.id = %q", o.RP.ID)
	}
	handle, err := b64.DecodeString(o.User.ID)
	if err != nil {
		t.Fatalf("user.id %q: %v", o.User.ID, err)
	}
	a.userHandle = handle
	cose, err := webauthncbor.Marshal(webauthncose.EC2PublicKeyData{
		PublicKeyData: webauthncose.PublicKeyData{KeyType: int64(webauthncose.EllipticKey), Algorithm: int64(webauthncose.AlgES256)},
		Curve:         1,
		XCoord:        pad32(a.key.X.Bytes()),
		YCoord:        pad32(a.key.Y.Bytes()),
	})
	if err != nil {
		t.Fatal(err)
	}
	rpHash := sha256.Sum256([]byte(testRPID))
	var ad bytes.Buffer
	ad.Write(rpHash[:])
	ad.WriteByte(a.flags(true))
	binary.Write(&ad, binary.BigEndian, a.counter)
	ad.Write(make([]byte, 16)) // AAGUID
	binary.Write(&ad, binary.BigEndian, uint16(len(a.credID)))
	ad.Write(a.credID)
	ad.Write(cose)
	att, err := webauthncbor.Marshal(map[string]any{"fmt": "none", "attStmt": map[string]any{}, "authData": ad.Bytes()})
	if err != nil {
		t.Fatal(err)
	}
	return map[string]any{
		"id": b64.EncodeToString(a.credID), "rawId": b64.EncodeToString(a.credID), "type": "public-key",
		"authenticatorAttachment": "platform",
		"clientExtensionResults":  map[string]any{},
		"response": map[string]any{
			"clientDataJSON":    b64.EncodeToString(a.clientData("webauthn.create", o.Challenge)),
			"attestationObject": b64.EncodeToString(att),
			"transports":        []string{"internal", "hybrid"},
		},
	}
}

// get answers a navigator.credentials.get() options object.
func (a *authenticator) get(t *testing.T, options json.RawMessage) map[string]any {
	t.Helper()
	var o struct {
		Challenge        string
		RPID             string `json:"rpId"`
		AllowCredentials []struct{ ID string }
	}
	if err := json.Unmarshal(options, &o); err != nil {
		t.Fatal(err)
	}
	if len(o.AllowCredentials) > 0 {
		found := false
		for _, c := range o.AllowCredentials {
			found = found || c.ID == b64.EncodeToString(a.credID)
		}
		if !found {
			t.Fatal("authenticator's credential is not in allowCredentials")
		}
	}
	a.counter++
	rpHash := sha256.Sum256([]byte(testRPID))
	var ad bytes.Buffer
	ad.Write(rpHash[:])
	ad.WriteByte(a.flags(false))
	binary.Write(&ad, binary.BigEndian, a.counter)
	cd := a.clientData("webauthn.get", o.Challenge)
	cdHash := sha256.Sum256(cd)
	digest := sha256.Sum256(append(ad.Bytes(), cdHash[:]...))
	sig, err := ecdsa.SignASN1(rand.Reader, a.key, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	return map[string]any{
		"id": b64.EncodeToString(a.credID), "rawId": b64.EncodeToString(a.credID), "type": "public-key",
		"authenticatorAttachment": "platform",
		"clientExtensionResults":  map[string]any{},
		"response": map[string]any{
			"clientDataJSON":    b64.EncodeToString(cd),
			"authenticatorData": b64.EncodeToString(ad.Bytes()),
			"signature":         b64.EncodeToString(sig),
			"userHandle":        b64.EncodeToString(a.userHandle),
		},
	}
}

func pad32(b []byte) []byte {
	if len(b) >= 32 {
		return b
	}
	return append(make([]byte, 32-len(b)), b...)
}

type begin struct {
	Flow    string
	Options json.RawMessage
}

// signUp runs the passkey sign-up ceremony and returns the authenticator.
func (b *browser) signUp(t *testing.T, email string) *authenticator {
	t.Helper()
	a := newAuthenticator()
	res := b.api("POST", "/app-api/auth/passkey/signup/begin", map[string]string{"email": email})
	mustStatus(t, "signup begin", res, 200)
	var bg begin
	res.json(t, &bg)
	res = b.api("POST", "/app-api/auth/passkey/signup/finish", map[string]any{"flow": bg.Flow, "credential": a.create(t, bg.Options)})
	mustStatus(t, "signup finish", res, 200)
	return a
}

// signIn runs the passkey sign-in ceremony (usernameless when email is "").
func (b *browser) signIn(t *testing.T, a *authenticator, email string) response {
	t.Helper()
	res := b.api("POST", "/app-api/auth/passkey/login/begin", map[string]string{"email": email})
	if res.status != 200 {
		return res
	}
	var bg begin
	res.json(t, &bg)
	return b.api("POST", "/app-api/auth/passkey/login/finish", map[string]any{"flow": bg.Flow, "credential": a.get(t, bg.Options)})
}

// addPasskey runs the add-passkey ceremony from the current session.
func (b *browser) addPasskey(t *testing.T) (*authenticator, response) {
	t.Helper()
	a := newAuthenticator()
	res := b.api("POST", "/app-api/auth/passkey/register/begin", nil)
	if res.status != 200 {
		return a, res
	}
	var bg begin
	res.json(t, &bg)
	return a, b.api("POST", "/app-api/auth/passkey/register/finish", map[string]any{"flow": bg.Flow, "credential": a.create(t, bg.Options)})
}

// fakeGoogle is an OIDC issuer: discovery, JWKS, and a token endpoint that
// checks PKCE and returns an ID token for the identity queued with next.
type fakeGoogle struct {
	srv *httptest.Server
	key *rsa.PrivateKey

	mu      sync.Mutex
	pending map[string]fakeGrant // by code
}

type fakeGrant struct {
	sub, email    string
	verified      bool
	nonce         string
	challenge     string
	redirectURI   string
	wrongNonce    bool
	wrongAudience bool
}

func newFakeGoogle(t *testing.T) *fakeGoogle {
	k, _ := rsa.GenerateKey(rand.Reader, 2048)
	g := &fakeGoogle{key: k, pending: map[string]fakeGrant{}}
	mux := http.NewServeMux()
	g.srv = httptest.NewServer(mux)
	t.Cleanup(g.srv.Close)
	mux.HandleFunc("GET /.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"issuer":                                g.srv.URL,
			"authorization_endpoint":                g.srv.URL + "/auth",
			"token_endpoint":                        g.srv.URL + "/token",
			"jwks_uri":                              g.srv.URL + "/jwks",
			"id_token_signing_alg_values_supported": []string{"RS256"},
		})
	})
	mux.HandleFunc("GET /jwks", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{Key: &k.PublicKey, KeyID: "k1", Algorithm: "RS256", Use: "sig"}}})
	})
	mux.HandleFunc("POST /token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		g.mu.Lock()
		grant, ok := g.pending[r.PostForm.Get("code")]
		delete(g.pending, r.PostForm.Get("code"))
		g.mu.Unlock()
		sum := sha256.Sum256([]byte(r.PostForm.Get("code_verifier")))
		if !ok || b64.EncodeToString(sum[:]) != grant.challenge || r.PostForm.Get("redirect_uri") != grant.redirectURI {
			w.WriteHeader(400)
			io.WriteString(w, `{"error":"invalid_grant"}`)
			return
		}
		user, pass, _ := r.BasicAuth()
		if user != "test-client" || pass != "test-secret" {
			if r.PostForm.Get("client_id") != "test-client" || r.PostForm.Get("client_secret") != "test-secret" {
				w.WriteHeader(401)
				io.WriteString(w, `{"error":"invalid_client"}`)
				return
			}
		}
		nonce, aud := grant.nonce, "test-client"
		if grant.wrongNonce {
			nonce = "other"
		}
		if grant.wrongAudience {
			aud = "someone-else"
		}
		claims, _ := json.Marshal(map[string]any{
			"iss": g.srv.URL, "aud": aud, "sub": grant.sub, "email": grant.email, "email_verified": grant.verified,
			"nonce": nonce, "iat": time.Now().Unix(), "exp": time.Now().Add(time.Hour).Unix(),
		})
		signer, _ := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: k}, (&jose.SignerOptions{}).WithType("JWT").WithHeader("kid", "k1"))
		jws, _ := signer.Sign(claims)
		idt, _ := jws.CompactSerialize()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"access_token": "at", "token_type": "Bearer", "expires_in": 3600, "id_token": idt})
	})
	return g
}

// googleRoundTrip starts a Google flow in mode, lets the fake issuer
// "authenticate" grant, and follows the callback. It returns the callback
// response (a redirect).
func (b *browser) googleRoundTrip(t *testing.T, mode, ret string, grant fakeGrant) response {
	t.Helper()
	res := b.api("POST", "/app-api/auth/google/start", map[string]string{"mode": mode, "return": ret})
	mustStatus(t, "google start", res, 200)
	var out struct{ URL string }
	res.json(t, &out)
	u, err := url.Parse(out.URL)
	if err != nil || !strings.HasPrefix(out.URL, b.e.google.srv.URL+"/auth?") {
		t.Fatalf("auth url %q", out.URL)
	}
	q := u.Query()
	if q.Get("code_challenge_method") != "S256" || q.Get("nonce") == "" || q.Get("state") == "" || q.Get("scope") != "openid email" {
		t.Fatalf("auth url lacks PKCE/nonce/state/scope: %q", out.URL)
	}
	grant.nonce, grant.challenge, grant.redirectURI = q.Get("nonce"), q.Get("code_challenge"), q.Get("redirect_uri")
	code := b64.EncodeToString([]byte(grant.sub + time.Now().String()))
	b.e.google.mu.Lock()
	b.e.google.pending[code] = grant
	b.e.google.mu.Unlock()
	return b.raw("GET", "/auth/google/callback?state="+url.QueryEscape(q.Get("state"))+"&code="+url.QueryEscape(code), nil, nil)
}

func location(t *testing.T, r response) string {
	t.Helper()
	if r.status != http.StatusSeeOther {
		t.Fatalf("status %d, want 303: %s", r.status, r.body)
	}
	return r.header.Get("Location")
}

// fakeMail records messages.
type fakeMail struct {
	enabled bool
	mu      sync.Mutex
	sent    []mail.Message
}

func (f *fakeMail) Enabled() bool { return f.enabled }

func (f *fakeMail) Send(_ context.Context, m mail.Message) error {
	if !f.enabled {
		return mail.ErrDisabled
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, m)
	return nil
}
