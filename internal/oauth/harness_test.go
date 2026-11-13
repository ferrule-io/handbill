package oauth_test

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ferrule-io/handbill/internal/appapi"
	"github.com/ferrule-io/handbill/internal/auth"
	"github.com/ferrule-io/handbill/internal/db"
	"github.com/ferrule-io/handbill/internal/oauth"
	"github.com/ferrule-io/handbill/internal/safehttp"
	"github.com/ferrule-io/handbill/internal/server"
	"github.com/ferrule-io/handbill/internal/sites/sitestest"
	"github.com/ferrule-io/handbill/internal/token"
)

// cimdHost is a fake TLS host serving client ID metadata documents.
type cimdHost struct {
	srv  *httptest.Server
	mu   sync.Mutex
	docs map[string]cimdDoc
	hits atomic.Int64
	// lastHeader is the most recent request's headers.
	lastHeader http.Header
}

type cimdDoc struct {
	status int
	header map[string]string
	body   string
}

func newCIMDHost(t *testing.T) *cimdHost {
	c := &cimdHost{docs: map[string]cimdDoc{}}
	c.srv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c.hits.Add(1)
		c.mu.Lock()
		d, ok := c.docs[r.URL.Path]
		c.lastHeader = r.Header.Clone()
		c.mu.Unlock()
		if !ok {
			http.NotFound(w, r)
			return
		}
		for k, v := range d.header {
			w.Header().Set(k, v)
		}
		if w.Header().Get("Content-Type") == "" {
			w.Header().Set("Content-Type", "application/json")
		}
		status := d.status
		if status == 0 {
			status = http.StatusOK
		}
		w.WriteHeader(status)
		_, _ = io.WriteString(w, d.body)
	}))
	t.Cleanup(c.srv.Close)
	return c
}

// url is the client_id for a document served at path.
func (c *cimdHost) url(path string) string { return c.srv.URL + path }

func (c *cimdHost) set(path string, d cimdDoc) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.docs[path] = d
}

func (c *cimdHost) header() http.Header {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lastHeader
}

// document is a valid CIMD document for clientID.
func document(clientID string, redirectURIs ...string) string {
	b, _ := json.Marshal(map[string]any{
		"client_id":                  clientID,
		"client_name":                "Test Agent",
		"redirect_uris":              redirectURIs,
		"grant_types":                []string{"authorization_code", "refresh_token"},
		"token_endpoint_auth_method": "none",
	})
	return string(b)
}

// harness is the app (auth, appapi, oauth, and extra modules) behind a TLS
// test server, so __Host- cookies work with a cookie jar.
type harness struct {
	t      *testing.T
	d      *server.Deps
	srv    *httptest.Server
	cimd   *cimdHost
	now    atomic.Pointer[time.Time]
	client *http.Client // browser: cookie jar, no redirects followed
}

type harnessOpts struct {
	dcr     bool
	modules []server.Module
	// cimdClient overrides the CIMD fetcher (default: trusts the fake host).
	cimdClient *safehttp.Client
}

func newHarness(t *testing.T, o harnessOpts) *harness {
	t.Helper()
	d := sitestest.Deps(t)
	h := &harness{t: t, d: d, cimd: newCIMDHost(t)}
	h.srv = httptest.NewUnstartedServer(nil)
	h.srv.StartTLS()
	t.Cleanup(h.srv.Close)
	// The app is https://example.com:<port> (in the test certificate; WebAuthn
	// refuses IP RP IDs), dialed at the listener.
	addr := h.srv.Listener.Addr().String()
	_, port, _ := net.SplitHostPort(addr)
	h.srv.URL = "https://example.com:" + port
	h.srv.Client().Transport.(*http.Transport).DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, addr)
	}
	u, _ := url.Parse(h.srv.URL)
	d.Config.AppOrigin = u
	d.Config.AppHost = u.Hostname()
	d.Config.OAuthDCREnabled = o.dcr

	cimdClient := o.cimdClient
	if cimdClient == nil {
		pool := x509.NewCertPool()
		pool.AddCert(h.cimd.srv.Certificate())
		cimdClient = safehttp.New(safehttp.Options{
			AllowLoopback: true, TLSClientConfig: &tls.Config{RootCAs: pool},
			Timeout: time.Second, MaxBodyBytes: 5 << 10,
		})
	}
	mods := append([]server.Module{auth.Register, appapi.Register, oauth.Module(oauth.Options{CIMD: cimdClient, Now: h.clock})}, o.modules...)
	handler, err := server.New(d, mods...)
	if err != nil {
		t.Fatal(err)
	}
	h.srv.Config.Handler = handler

	jar, _ := cookiejar.New(nil)
	h.client = &http.Client{
		Jar:           jar,
		Transport:     h.srv.Client().Transport,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	return h
}

func (h *harness) clock() time.Time {
	if p := h.now.Load(); p != nil {
		return *p
	}
	return time.Now()
}

// advance moves the module's clock forward (the database's now() is unchanged).
func (h *harness) advance(d time.Duration) {
	t := h.clock().Add(d)
	h.now.Store(&t)
}

// signIn creates an account and signs the browser in as it.
func (h *harness) signIn(email string) uuid.UUID {
	h.t.Helper()
	user := sitestest.User(h.t, h.d, email)
	h.setSession(user)
	return user
}

func (h *harness) setSession(user uuid.UUID) {
	h.t.Helper()
	plaintext, hash := token.New()
	if _, err := db.New(h.d.DB).CreateSession(h.t.Context(), db.CreateSessionParams{
		ID: uuid.Must(uuid.NewV7()), TokenHash: hash, UserID: user, UserAgent: "test", ExpiresAt: time.Now().Add(time.Hour),
	}); err != nil {
		h.t.Fatal(err)
	}
	u, _ := url.Parse(h.srv.URL)
	h.client.Jar.SetCookies(u, []*http.Cookie{{Name: auth.SessionCookie, Value: plaintext, Path: "/", Secure: true, HttpOnly: true}})
}

// userID looks up an account by primary email.
func (h *harness) userID(email string) uuid.UUID {
	h.t.Helper()
	var id uuid.UUID
	if err := h.d.DB.QueryRow(h.t.Context(), `SELECT user_id FROM emails WHERE email = $1`, email).Scan(&id); err != nil {
		h.t.Fatal(err)
	}
	return id
}

func (h *harness) signOut() {
	jar, _ := cookiejar.New(nil)
	h.client.Jar = jar
}

func (h *harness) do(req *http.Request) *http.Response {
	h.t.Helper()
	res, err := h.client.Do(req)
	if err != nil {
		h.t.Fatal(err)
	}
	h.t.Cleanup(func() { res.Body.Close() })
	return res
}

func (h *harness) get(path string) *http.Response {
	h.t.Helper()
	req, _ := http.NewRequest("GET", h.srv.URL+path, nil)
	return h.do(req)
}

// csrf returns the browser's CSRF token.
func (h *harness) csrf() string {
	h.t.Helper()
	var me struct {
		CSRFToken string `json:"csrfToken"`
	}
	decode(h.t, h.get("/app-api/me"), &me)
	return me.CSRFToken
}

func decode(t *testing.T, res *http.Response, v any) {
	t.Helper()
	b, _ := io.ReadAll(res.Body)
	if err := json.Unmarshal(b, v); err != nil {
		t.Fatalf("decode %d %q: %v", res.StatusCode, b, err)
	}
}

// authorize sends GET /oauth/authorize and returns the response unfollowed.
func (h *harness) authorize(params url.Values) *http.Response {
	h.t.Helper()
	return h.get("/oauth/authorize?" + params.Encode())
}

// requestHandle extracts the consent request handle from a /consent redirect.
func requestHandle(t *testing.T, res *http.Response) string {
	t.Helper()
	loc := res.Header.Get("Location")
	if res.StatusCode != http.StatusSeeOther || !strings.HasPrefix(loc, "/consent?request=") {
		t.Fatalf("authorize: %d %q, want 303 /consent?request=", res.StatusCode, loc)
	}
	u, _ := url.Parse(loc)
	return u.Query().Get("request")
}

// consentError is the error code of a /consent?error= redirect, or "".
func consentError(res *http.Response) string {
	loc := res.Header.Get("Location")
	if res.StatusCode != http.StatusSeeOther || !strings.HasPrefix(loc, "/consent?error=") {
		return ""
	}
	u, _ := url.Parse(loc)
	return u.Query().Get("error")
}

// consent posts the decision and returns the response.
func (h *harness) consent(request string, approve bool) *http.Response {
	h.t.Helper()
	body, _ := json.Marshal(map[string]any{"request": request, "approve": approve})
	req, _ := http.NewRequest("POST", h.srv.URL+"/app-api/oauth/consent", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", h.srv.URL)
	req.Header.Set(appapi.CSRFHeader, h.csrf())
	return h.do(req)
}

// approve consents and returns the redirect URL's query.
func (h *harness) approve(request string) url.Values {
	h.t.Helper()
	res := h.consent(request, true)
	var out struct {
		RedirectTo string `json:"redirectTo"`
	}
	decode(h.t, res, &out)
	u, err := url.Parse(out.RedirectTo)
	if err != nil || res.StatusCode != 200 {
		h.t.Fatalf("consent: %d %q", res.StatusCode, out.RedirectTo)
	}
	return u.Query()
}

// form posts a form to path without cookies, as a native client does.
func (h *harness) form(path string, v url.Values) *http.Response {
	h.t.Helper()
	req, _ := http.NewRequest("POST", h.srv.URL+path, strings.NewReader(v.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res, err := h.srv.Client().Do(req)
	if err != nil {
		h.t.Fatal(err)
	}
	h.t.Cleanup(func() { res.Body.Close() })
	return res
}

func pkce() (verifier, challenge string) {
	verifier = strings.Repeat("v", 20) + uuid.NewString()
	sum := sha256.Sum256([]byte(verifier))
	return verifier, base64.RawURLEncoding.EncodeToString(sum[:])
}

// flow is one client's authorization state.
type flow struct {
	clientID, redirectURI, verifier, challenge string
}

// newCIMDClient serves a document for a client with one loopback redirect.
func (h *harness) newCIMDClient(path string) flow {
	id := h.cimd.url(path)
	h.cimd.set(path, cimdDoc{body: document(id, "http://127.0.0.1/callback")})
	v, c := pkce()
	return flow{clientID: id, redirectURI: "http://127.0.0.1:5555/callback", verifier: v, challenge: c}
}

func (f flow) params() url.Values {
	return url.Values{
		"client_id": {f.clientID}, "redirect_uri": {f.redirectURI}, "response_type": {"code"},
		"code_challenge": {f.challenge}, "code_challenge_method": {"S256"}, "state": {"s1"},
	}
}

// code runs authorize and consent and returns an authorization code.
func (h *harness) code(f flow) string {
	h.t.Helper()
	q := h.approve(requestHandle(h.t, h.authorize(f.params())))
	return q.Get("code")
}

type tokens struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	RefreshToken string `json:"refresh_token"`
	Scope        string `json:"scope"`
	Error        string `json:"error"`
}

func (h *harness) exchange(f flow, code string) (int, tokens) {
	h.t.Helper()
	res := h.form("/oauth/token", url.Values{
		"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {f.redirectURI},
		"code_verifier": {f.verifier}, "client_id": {f.clientID},
	})
	var tk tokens
	decode(h.t, res, &tk)
	return res.StatusCode, tk
}

func (h *harness) refresh(f flow, refreshToken string) (int, tokens) {
	h.t.Helper()
	res := h.form("/oauth/token", url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refreshToken}, "client_id": {f.clientID}})
	var tk tokens
	decode(h.t, res, &tk)
	return res.StatusCode, tk
}

// bearer sends POST /mcp with an access token and no Origin.
func (h *harness) bearer(accessToken string) *http.Response {
	h.t.Helper()
	req, _ := http.NewRequest("POST", h.srv.URL+"/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("MCP-Protocol-Version", "2025-11-25")
	if accessToken != "" {
		req.Header.Set("Authorization", "Bearer "+accessToken)
	}
	res, err := h.srv.Client().Do(req)
	if err != nil {
		h.t.Fatal(err)
	}
	h.t.Cleanup(func() { res.Body.Close() })
	return res
}
