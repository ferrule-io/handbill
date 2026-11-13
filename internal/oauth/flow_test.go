package oauth_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/oauthex"

	"github.com/ferrule-io/handbill/internal/appapi"
	"github.com/ferrule-io/handbill/internal/mcp"
	"github.com/ferrule-io/handbill/internal/safehttp"
	"github.com/ferrule-io/handbill/internal/server"
)

func withMCP() harnessOpts { return harnessOpts{modules: []server.Module{mcp.Register}} }

func TestMetadata(t *testing.T) {
	for _, dcr := range []bool{false, true} {
		h := newHarness(t, harnessOpts{dcr: dcr})
		for _, p := range []string{"/.well-known/oauth-protected-resource/mcp", "/.well-known/oauth-protected-resource"} {
			res := h.get(p)
			var prm map[string]any
			decode(t, res, &prm)
			if res.StatusCode != 200 || prm["resource"] != h.srv.URL+"/mcp" || res.Header.Get("Cache-Control") != "public, max-age=3600" {
				t.Fatalf("%s: %d %v", p, res.StatusCode, prm)
			}
			if _, ok := prm["scopes_supported"]; ok {
				t.Fatal("PRM lists scopes")
			}
			if res.Header.Get("Access-Control-Allow-Origin") != "" {
				t.Fatal("CORS header sent")
			}
		}
		res := h.get("/.well-known/oauth-authorization-server")
		var as map[string]any
		decode(t, res, &as)
		if as["issuer"] != h.srv.URL || as["token_endpoint"] != h.srv.URL+"/oauth/token" ||
			as["authorization_response_iss_parameter_supported"] != true || as["client_id_metadata_document_supported"] != true {
			t.Fatalf("AS metadata: %v", as)
		}
		if _, ok := as["registration_endpoint"]; ok != dcr {
			t.Fatalf("registration_endpoint present = %v with dcr = %v", ok, dcr)
		}
	}
}

func TestBearerDiscovery(t *testing.T) {
	h := newHarness(t, withMCP())
	meta := h.srv.URL + "/.well-known/oauth-protected-resource/mcp"

	res := h.bearer("")
	if res.StatusCode != 401 || res.Header.Get("WWW-Authenticate") != `Bearer resource_metadata="`+meta+`"` {
		t.Fatalf("no token: %d %q", res.StatusCode, res.Header.Get("WWW-Authenticate"))
	}
	invalid := func(tok string) {
		t.Helper()
		res := h.bearer(tok)
		wa := res.Header.Get("WWW-Authenticate")
		if res.StatusCode != 401 || !strings.Contains(wa, `error="invalid_token"`) || !strings.Contains(wa, `resource_metadata="`+meta+`"`) {
			t.Fatalf("token %q: %d %q", tok, res.StatusCode, wa)
		}
		if _, err := oauthex.ParseWWWAuthenticate([]string{wa}); err != nil {
			t.Fatalf("parse %q: %v", wa, err)
		}
	}
	invalid("garbage")
	invalid(strings.Repeat("A", 43))

	f := h.newCIMDClient("/client")
	h.signIn("a@example.com")
	_, tk := h.exchange(f, h.code(f))
	if res := h.bearer(tk.AccessToken); res.StatusCode != 200 {
		t.Fatalf("valid token: %d", res.StatusCode)
	}

	// Expired, revoked, and other-resource tokens.
	exec := func(sql string) {
		t.Helper()
		if _, err := h.d.DB.Exec(t.Context(), sql); err != nil {
			t.Fatal(err)
		}
	}
	_, tk2 := h.exchange(f, h.code(f))
	exec(`UPDATE oauth_access_tokens SET expires_at = now() - interval '1 second' WHERE token_hash = sha256('` + tk2.AccessToken + `'::bytea)`)
	invalid(tk2.AccessToken)
	_, tk3 := h.exchange(f, h.code(f))
	exec(`UPDATE oauth_access_tokens SET revoked_at = now() WHERE token_hash = sha256('` + tk3.AccessToken + `'::bytea)`)
	invalid(tk3.AccessToken)
	_, tk4 := h.exchange(f, h.code(f))
	exec(`UPDATE oauth_access_tokens SET resource = 'https://other.example/mcp' WHERE token_hash = sha256('` + tk4.AccessToken + `'::bytea)`)
	invalid(tk4.AccessToken)

	// A session cookie alone, or a token in the query, is not a credential.
	req, _ := http.NewRequest("POST", h.srv.URL+"/mcp?access_token="+tk.AccessToken, strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	if res := h.do(req); res.StatusCode != 401 {
		t.Fatalf("cookie + query token: %d", res.StatusCode)
	}
}

func TestOrigin(t *testing.T) {
	h := newHarness(t, withMCP())
	f := h.newCIMDClient("/client")
	h.signIn("a@example.com")
	_, tk := h.exchange(f, h.code(f))
	for origin, want := range map[string]int{
		"https://evil.example":                 403,
		"null":                                 403,
		"https://abcdefghijkl.sites.localhost": 403,
		"http://localhost:3000":                403,
		h.srv.URL:                              200,
	} {
		req, _ := http.NewRequest("POST", h.srv.URL+"/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		req.Header.Set("MCP-Protocol-Version", "2025-11-25")
		req.Header.Set("Authorization", "Bearer "+tk.AccessToken)
		req.Header.Set("Origin", origin)
		res, err := h.srv.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != want {
			t.Errorf("Origin %q: %d, want %d", origin, res.StatusCode, want)
		}
	}
	// Without Origin, and served on a loopback listener whose Host is an IP
	// (the SDK's localhost protection is off).
	if res := h.bearer(tk.AccessToken); res.StatusCode != 200 {
		t.Fatalf("no Origin: %d", res.StatusCode)
	}
	req, _ := http.NewRequest("GET", h.srv.URL+"/mcp", nil)
	req.Header.Set("Authorization", "Bearer "+tk.AccessToken)
	req.Header.Set("Accept", "text/event-stream")
	res, _ := h.srv.Client().Do(req)
	res.Body.Close()
	if res.StatusCode != 405 {
		t.Fatalf("GET /mcp: %d", res.StatusCode)
	}
}

func TestCIMDFetch(t *testing.T) {
	h := newHarness(t, harnessOpts{})
	h.signIn("a@example.com")
	try := func(path string, d *cimdDoc) string {
		t.Helper()
		id := h.cimd.url(path)
		if d != nil {
			if d.body == "" {
				d.body = document(id, "http://127.0.0.1/callback")
			}
			h.cimd.set(path, *d)
		}
		f := flow{clientID: id, redirectURI: "http://127.0.0.1:5555/callback"}
		_, f.challenge = pkce()
		res := h.authorize(f.params())
		if code := consentError(res); code != "" {
			return code
		}
		requestHandle(t, res)
		return ""
	}
	if got := try("/ok", &cimdDoc{}); got != "" {
		t.Fatalf("valid document: %q", got)
	}
	cases := map[string]struct {
		doc  cimdDoc
		want string
	}{
		"/redirect":  {cimdDoc{status: 302, header: map[string]string{"Location": "/ok"}}, "client_metadata_invalid"},
		"/html":      {cimdDoc{header: map[string]string{"Content-Type": "text/html"}}, "client_metadata_invalid"},
		"/large":     {cimdDoc{body: `{"x":"` + strings.Repeat("a", 5121) + `"}`}, "client_metadata_invalid"},
		"/badjson":   {cimdDoc{body: `{`}, "client_metadata_invalid"},
		"/noname":    {cimdDoc{body: `{"client_id":"X","redirect_uris":["http://127.0.0.1/callback"]}`}, "client_metadata_invalid"},
		"/secret":    {cimdDoc{body: `{"x":1}`}, "client_metadata_invalid"},
		"/gone":      {cimdDoc{status: 410}, "client_metadata_invalid"},
		"/down":      {cimdDoc{status: 503}, "client_metadata_unavailable"},
		"/ratelimit": {cimdDoc{status: 429}, "client_metadata_unavailable"},
	}
	for path, c := range cases {
		if got := try(path, &c.doc); got != c.want {
			t.Errorf("%s: %q, want %q", path, got, c.want)
		}
	}
	if got := try("/missing", nil); got != "client_metadata_invalid" {
		t.Errorf("404: %q", got)
	}
	privateKeyOnly := h.cimd.url("/pkjwt")
	if got := try("/pkjwt", &cimdDoc{body: `{"client_id":"` + privateKeyOnly + `","client_name":"A","redirect_uris":["http://127.0.0.1/callback"],"token_endpoint_auth_method":"private_key_jwt"}`}); got != "invalid_client" {
		t.Errorf("private_key_jwt only: %q", got)
	}

	// A 5xx with a stored document keeps using it.
	h.cimd.set("/ok", cimdDoc{status: 500})
	h.advance(2 * time.Hour)
	if got := try("/ok", nil); got != "" {
		t.Errorf("500 with cache: %q", got)
	}

	// A client_id mismatch on revalidation deletes the client's grants.
	f := h.newCIMDClient("/mismatch")
	h.code(f)
	h.cimd.set("/mismatch", cimdDoc{body: document("https://other.example/meta", "http://127.0.0.1/callback")})
	h.advance(2 * time.Hour)
	if got := try("/mismatch", nil); got != "client_metadata_invalid" {
		t.Errorf("mismatch: %q", got)
	}
	var n int
	_ = h.d.DB.QueryRow(t.Context(), `SELECT count(*) FROM oauth_grants g JOIN oauth_clients c ON c.id = g.client_id WHERE c.client_id = $1`, f.clientID).Scan(&n)
	if n != 0 {
		t.Errorf("grants after mismatch: %d", n)
	}
}

func TestCIMDBlockedAddress(t *testing.T) {
	h := newHarness(t, harnessOpts{cimdClient: safehttp.New(safehttp.Options{})})
	h.signIn("a@example.com")
	f := h.newCIMDClient("/client")
	if got := consentError(h.authorize(f.params())); got != "client_metadata_invalid" {
		t.Fatalf("loopback CIMD host: %q", got)
	}
}

func TestCIMDCaching(t *testing.T) {
	h := newHarness(t, harnessOpts{})
	h.signIn("a@example.com")
	f := h.newCIMDClient("/client")
	id := f.clientID
	h.cimd.set("/client", cimdDoc{body: document(id, "http://127.0.0.1/callback"), header: map[string]string{"Cache-Control": "max-age=300", "ETag": `"v1"`}})
	requestHandle(t, h.authorize(f.params()))
	requestHandle(t, h.authorize(f.params()))
	if n := h.cimd.hits.Load(); n != 1 {
		t.Fatalf("hits within TTL: %d", n)
	}
	h.advance(6 * time.Minute)
	h.cimd.set("/client", cimdDoc{status: 304, header: map[string]string{"Cache-Control": "max-age=300"}})
	requestHandle(t, h.authorize(f.params()))
	if n := h.cimd.hits.Load(); n != 2 || h.cimd.header().Get("If-None-Match") != `"v1"` {
		t.Fatalf("revalidate: hits %d, If-None-Match %q", n, h.cimd.header().Get("If-None-Match"))
	}
	requestHandle(t, h.authorize(f.params()))
	if n := h.cimd.hits.Load(); n != 2 {
		t.Fatalf("after 304: hits %d", n)
	}

	g := h.newCIMDClient("/nostore")
	h.cimd.set("/nostore", cimdDoc{body: document(g.clientID, "http://127.0.0.1/callback"), header: map[string]string{"Cache-Control": "no-store"}})
	before := h.cimd.hits.Load()
	requestHandle(t, h.authorize(g.params()))
	requestHandle(t, h.authorize(g.params()))
	if n := h.cimd.hits.Load() - before; n != 2 {
		t.Fatalf("no-store fetches: %d", n)
	}
}

func TestAuthorizeValidation(t *testing.T) {
	h := newHarness(t, harnessOpts{})
	h.signIn("a@example.com")
	f := h.newCIMDClient("/client")

	// Errors that must not redirect.
	bad := f.params()
	bad.Set("client_id", "https://")
	bad.Set("redirect_uri", "https://evil.example/cb")
	if got := consentError(h.authorize(bad)); got != "invalid_client" {
		t.Fatalf("bad client and redirect: %q", got)
	}
	bad = f.params()
	bad.Set("redirect_uri", "https://evil.example/cb")
	if got := consentError(h.authorize(bad)); got != "invalid_redirect_uri" {
		t.Fatalf("bad redirect: %q", got)
	}
	bad = f.params()
	bad.Add("client_id", f.clientID)
	if got := consentError(h.authorize(bad)); got != "invalid_client" {
		t.Fatalf("repeated client_id: %q", got)
	}

	redirected := func(mod func(url.Values), wantErr string, wantState bool) {
		t.Helper()
		p := f.params()
		mod(p)
		res := h.authorize(p)
		loc, _ := url.Parse(res.Header.Get("Location"))
		q := loc.Query()
		if res.StatusCode != 302 || !strings.HasPrefix(loc.String(), "http://127.0.0.1:5555/callback?") || q.Get("error") != wantErr || q.Get("iss") != h.srv.URL {
			t.Fatalf("%s: %d %s", wantErr, res.StatusCode, loc)
		}
		if q.Has("state") != wantState || (wantState && q.Get("state") != "s1") {
			t.Fatalf("%s: state %q", wantErr, q["state"])
		}
	}
	redirected(func(p url.Values) { p.Del("code_challenge") }, "invalid_request", true)
	redirected(func(p url.Values) { p.Set("code_challenge_method", "plain") }, "invalid_request", true)
	redirected(func(p url.Values) { p.Del("code_challenge_method") }, "invalid_request", true)
	redirected(func(p url.Values) { p.Set("code_challenge", strings.Repeat("a", 42)) }, "invalid_request", true)
	redirected(func(p url.Values) { p.Set("response_type", "token") }, "unsupported_response_type", true)
	redirected(func(p url.Values) { p.Set("resource", "https://evil/mcp") }, "invalid_target", true)
	redirected(func(p url.Values) { p["scope"] = []string{"a", "b"} }, "invalid_request", true)
	redirected(func(p url.Values) { p.Set("state", strings.Repeat("s", 1025)) }, "invalid_request", false)
	redirected(func(p url.Values) { p.Set("request_uri", "urn:x") }, "request_not_supported", true)
	redirected(func(p url.Values) { p.Set("response_mode", "fragment") }, "invalid_request", true)

	// The resource is matched after lowercasing scheme and host.
	p := f.params()
	p.Set("resource", strings.ToUpper(h.srv.URL[:8])+h.srv.URL[8:]+"/mcp")
	requestHandle(t, h.authorize(p))

	// Signed out: sign in first, then come back.
	h.signOut()
	res := h.authorize(f.params())
	loc := res.Header.Get("Location")
	if res.StatusCode != 303 || !strings.HasPrefix(loc, "/login?return=%2Foauth%2Fauthorize%3F") {
		t.Fatalf("signed out: %d %q", res.StatusCode, loc)
	}
}

func TestConsent(t *testing.T) {
	h := newHarness(t, harnessOpts{})
	h.signIn("a@example.com")
	f := h.newCIMDClient("/client")
	handle := requestHandle(t, h.authorize(f.params()))

	res := h.get("/app-api/oauth/consent?request=" + url.QueryEscape(handle))
	var got struct {
		Request struct {
			Client struct {
				ClientID, Name, Host string
				Verified             bool
			}
			Redirect struct {
				URI, Host string
				Loopback  bool
			}
			Account              struct{ Email string }
			PreviouslyAuthorized bool
		}
	}
	decode(t, res, &got)
	r := got.Request
	u, _ := url.Parse(f.clientID)
	if res.Header.Get("Cache-Control") != "no-store" || r.Client.Name != "Test Agent" || r.Client.Host != u.Host || !r.Client.Verified ||
		r.Redirect.Host != "127.0.0.1:5555" || !r.Redirect.Loopback || r.Account.Email != "a@example.com" || r.PreviouslyAuthorized {
		t.Fatalf("consent details: %+v", r)
	}

	// CSRF and Origin are required.
	req, _ := http.NewRequest("POST", h.srv.URL+"/app-api/oauth/consent", strings.NewReader(`{"request":"`+handle+`","approve":true}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", h.srv.URL)
	if res := h.do(req); res.StatusCode != 403 {
		t.Fatalf("no CSRF: %d", res.StatusCode)
	}
	req, _ = http.NewRequest("POST", h.srv.URL+"/app-api/oauth/consent", strings.NewReader(`{"request":"`+handle+`","approve":true}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "https://evil.example")
	req.Header.Set(appapi.CSRFHeader, h.csrf())
	if res := h.do(req); res.StatusCode != 403 {
		t.Fatalf("wrong Origin: %d", res.StatusCode)
	}

	q := h.approve(handle)
	if q.Get("code") == "" || q.Get("state") != "s1" || q.Get("iss") != h.srv.URL {
		t.Fatalf("approve: %v", q)
	}
	if res := h.consent(handle, true); res.StatusCode != 404 {
		t.Fatalf("second decision: %d", res.StatusCode)
	}

	// Now previously authorized; deny redirects with access_denied.
	handle = requestHandle(t, h.authorize(f.params()))
	decode(t, h.get("/app-api/oauth/consent?request="+url.QueryEscape(handle)), &got)
	if !got.Request.PreviouslyAuthorized {
		t.Fatal("previouslyAuthorized false after a grant")
	}
	res = h.consent(handle, false)
	var out struct{ RedirectTo string }
	decode(t, res, &out)
	if !strings.Contains(out.RedirectTo, "error=access_denied") || !strings.Contains(out.RedirectTo, "state=s1") {
		t.Fatalf("deny: %q", out.RedirectTo)
	}

	// Another user's session, and an expired request.
	handle = requestHandle(t, h.authorize(f.params()))
	h.signIn("b@example.com")
	if res := h.get("/app-api/oauth/consent?request=" + url.QueryEscape(handle)); res.StatusCode != 404 {
		t.Fatalf("other user: %d", res.StatusCode)
	}
	h.advance(-11 * time.Minute)
	handle = requestHandle(t, h.authorize(f.params()))
	if res := h.get("/app-api/oauth/consent?request=" + url.QueryEscape(handle)); res.StatusCode != 404 {
		t.Fatalf("expired: %d", res.StatusCode)
	}
}

func TestTokenExchange(t *testing.T) {
	h := newHarness(t, withMCP())
	h.signIn("a@example.com")
	f := h.newCIMDClient("/client")

	status, tk := h.exchange(f, h.code(f))
	if status != 200 || tk.TokenType != "Bearer" || tk.ExpiresIn != 3600 || tk.RefreshToken == "" || tk.Scope != "" {
		t.Fatalf("happy path: %d %+v", status, tk)
	}
	if res := h.bearer(tk.AccessToken); res.StatusCode != 200 {
		t.Fatalf("tools/list: %d", res.StatusCode)
	}

	fail := func(name string, mod func(url.Values), wantStatus int, wantErr string) {
		t.Helper()
		v := url.Values{
			"grant_type": {"authorization_code"}, "code": {h.code(f)}, "redirect_uri": {f.redirectURI},
			"code_verifier": {f.verifier}, "client_id": {f.clientID},
		}
		mod(v)
		res := h.form("/oauth/token", v)
		var e tokens
		decode(t, res, &e)
		if res.StatusCode != wantStatus || e.Error != wantErr {
			t.Errorf("%s: %d %q, want %d %q", name, res.StatusCode, e.Error, wantStatus, wantErr)
		}
	}
	fail("wrong verifier", func(v url.Values) { v.Set("code_verifier", strings.Repeat("x", 43)) }, 400, "invalid_grant")
	fail("missing verifier", func(v url.Values) { v.Del("code_verifier") }, 400, "invalid_request")
	fail("wrong port", func(v url.Values) { v.Set("redirect_uri", "http://127.0.0.1:5556/callback") }, 400, "invalid_grant")
	g := h.newCIMDClient("/other")
	h.code(g) // registers the other client
	fail("other client", func(v url.Values) { v.Set("client_id", g.clientID) }, 400, "invalid_grant")
	fail("resource", func(v url.Values) { v.Set("resource", "https://other.example/mcp") }, 400, "invalid_target")
	fail("unknown client", func(v url.Values) { v.Set("client_id", "https://nobody.example/x") }, 401, "invalid_client")
	fail("secret", func(v url.Values) { v.Set("client_secret", "x") }, 401, "invalid_client")
	fail("no grant type", func(v url.Values) { v.Del("grant_type") }, 400, "invalid_request")
	fail("grant type", func(v url.Values) { v.Set("grant_type", "password") }, 400, "unsupported_grant_type")
	fail("repeated", func(v url.Values) { v.Add("code", "x") }, 400, "invalid_request")

	// An expired code.
	code := h.code(f)
	if _, err := h.d.DB.Exec(t.Context(), `UPDATE oauth_authorization_codes SET expires_at = now() - interval '1 second' WHERE token_hash = sha256($1::text::bytea)`, code); err != nil {
		t.Fatal(err)
	}
	if status, e := h.exchange(f, code); status != 400 || e.Error != "invalid_grant" {
		t.Errorf("expired code: %d %q", status, e.Error)
	}

	// Reuse revokes the family.
	code = h.code(f)
	_, first := h.exchange(f, code)
	if status, e := h.exchange(f, code); status != 400 || e.Error != "invalid_grant" {
		t.Fatalf("reuse: %d %q", status, e.Error)
	}
	if res := h.bearer(first.AccessToken); res.StatusCode != 401 {
		t.Fatalf("first access token after reuse: %d", res.StatusCode)
	}
	if status, e := h.refresh(f, first.RefreshToken); status != 400 || e.Error != "invalid_grant" {
		t.Fatalf("first refresh token after reuse: %d %q", status, e.Error)
	}

	// x/oauth2's AuthStyleAutoDetect tries HTTP Basic first, then the form.
	code = h.code(f)
	v := url.Values{"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {f.redirectURI}, "code_verifier": {f.verifier}}
	req, _ := http.NewRequest("POST", h.srv.URL+"/oauth/token", strings.NewReader(v.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetBasicAuth(f.clientID, "")
	res, _ := h.srv.Client().Do(req)
	res.Body.Close()
	if res.StatusCode != 401 || res.Header.Get("WWW-Authenticate") != `Basic realm="handbill"` {
		t.Fatalf("basic: %d %q", res.StatusCode, res.Header.Get("WWW-Authenticate"))
	}
	if status, _ := h.exchange(f, code); status != 200 {
		t.Fatalf("form after basic: %d", status)
	}

	// Wrong content type.
	req, _ = http.NewRequest("POST", h.srv.URL+"/oauth/token", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	res, _ = h.srv.Client().Do(req)
	res.Body.Close()
	if res.StatusCode != 400 {
		t.Fatalf("json body: %d", res.StatusCode)
	}
}

func TestRefresh(t *testing.T) {
	h := newHarness(t, withMCP())
	h.signIn("a@example.com")
	f := h.newCIMDClient("/client")
	_, tk := h.exchange(f, h.code(f))

	status, next := h.refresh(f, tk.RefreshToken)
	if status != 200 || next.AccessToken == "" || next.RefreshToken == "" || next.RefreshToken == tk.RefreshToken {
		t.Fatalf("rotate: %d %+v", status, next)
	}
	if res := h.bearer(next.AccessToken); res.StatusCode != 200 {
		t.Fatalf("new access token: %d", res.StatusCode)
	}
	// Reusing the old refresh token revokes the family.
	if status, e := h.refresh(f, tk.RefreshToken); status != 400 || e.Error != "invalid_grant" {
		t.Fatalf("reuse: %d %q", status, e.Error)
	}
	if res := h.bearer(next.AccessToken); res.StatusCode != 401 {
		t.Fatalf("access after reuse: %d", res.StatusCode)
	}
	if status, _ := h.refresh(f, next.RefreshToken); status != 400 {
		t.Fatalf("refresh after reuse: %d", status)
	}

	refreshErr := func(name string, v url.Values, want string) {
		t.Helper()
		res := h.form("/oauth/token", v)
		var e tokens
		decode(t, res, &e)
		if e.Error != want {
			t.Errorf("%s: %d %q, want %q", name, res.StatusCode, e.Error, want)
		}
	}
	_, tk = h.exchange(f, h.code(f))
	refreshErr("scope", url.Values{"grant_type": {"refresh_token"}, "refresh_token": {tk.RefreshToken}, "client_id": {f.clientID}, "scope": {"foo"}}, "invalid_scope")
	refreshErr("resource", url.Values{"grant_type": {"refresh_token"}, "refresh_token": {tk.RefreshToken}, "client_id": {f.clientID}, "resource": {"https://x/mcp"}}, "invalid_target")
	g := h.newCIMDClient("/other")
	h.code(g)
	refreshErr("other client", url.Values{"grant_type": {"refresh_token"}, "refresh_token": {tk.RefreshToken}, "client_id": {g.clientID}}, "invalid_grant")
	h.advance(31 * 24 * time.Hour)
	refreshErr("expired", url.Values{"grant_type": {"refresh_token"}, "refresh_token": {tk.RefreshToken}, "client_id": {f.clientID}}, "invalid_grant")
	h.now.Store(nil)

	// offline_access is recorded and echoed.
	p := f.params()
	p.Set("scope", "offline_access other")
	q := h.approve(requestHandle(t, h.authorize(p)))
	if _, tk := h.exchange(f, q.Get("code")); tk.Scope != "offline_access" {
		t.Fatalf("scope: %q", tk.Scope)
	}

	// A client without refresh_token in grant_types gets no refresh token.
	id := h.cimd.url("/norefresh")
	b, _ := json.Marshal(map[string]any{"client_id": id, "client_name": "N", "redirect_uris": []string{"http://127.0.0.1/callback"}, "grant_types": []string{"authorization_code"}})
	h.cimd.set("/norefresh", cimdDoc{body: string(b)})
	n := flow{clientID: id, redirectURI: "http://127.0.0.1:5555/callback"}
	n.verifier, n.challenge = pkce()
	if _, tk := h.exchange(n, h.code(n)); tk.AccessToken == "" || tk.RefreshToken != "" {
		t.Fatalf("no-refresh client: %+v", tk)
	}
}

func TestRevocation(t *testing.T) {
	h := newHarness(t, withMCP())
	h.signIn("a@example.com")
	f := h.newCIMDClient("/client")
	revoke := func(v url.Values) int {
		res := h.form("/oauth/revoke", v)
		return res.StatusCode
	}

	_, tk := h.exchange(f, h.code(f))
	if s := revoke(url.Values{"token": {tk.RefreshToken}, "client_id": {f.clientID}}); s != 200 {
		t.Fatalf("revoke refresh: %d", s)
	}
	if res := h.bearer(tk.AccessToken); res.StatusCode != 401 {
		t.Fatal("family survived refresh-token revocation")
	}

	_, a := h.exchange(f, h.code(f))
	if s := revoke(url.Values{"token": {a.AccessToken}, "client_id": {f.clientID}, "token_type_hint": {"access_token"}}); s != 200 {
		t.Fatalf("revoke access: %d", s)
	}
	if res := h.bearer(a.AccessToken); res.StatusCode != 401 {
		t.Fatal("access token survived revocation")
	}
	if s, _ := h.refresh(f, a.RefreshToken); s != 200 {
		t.Fatalf("refresh after access revocation: %d", s)
	}

	if s := revoke(url.Values{"token": {"unknown"}, "client_id": {f.clientID}}); s != 200 {
		t.Fatalf("unknown token: %d", s)
	}
	g := h.newCIMDClient("/other")
	h.code(g)
	_, b := h.exchange(f, h.code(f))
	if s := revoke(url.Values{"token": {b.AccessToken}, "client_id": {g.clientID}}); s != 200 {
		t.Fatalf("other client's token: %d", s)
	}
	if res := h.bearer(b.AccessToken); res.StatusCode != 200 {
		t.Fatal("another client revoked a token")
	}
	if s := revoke(url.Values{"token": {b.AccessToken}, "client_id": {"https://nobody.example/x"}}); s != 401 {
		t.Fatalf("unknown client: %d", s)
	}
}

func TestDCR(t *testing.T) {
	off := newHarness(t, harnessOpts{})
	res := off.do(jsonReq(t, off.srv.URL+"/oauth/register", `{"redirect_uris":["http://127.0.0.1/cb"]}`))
	if res.StatusCode != 404 {
		t.Fatalf("DCR off: %d", res.StatusCode)
	}

	h := newHarness(t, harnessOpts{dcr: true, modules: []server.Module{mcp.Register}})
	for body, want := range map[string]string{
		`{"redirect_uris":["http://example.com/cb"]}`: "invalid_redirect_uri",
		`{"redirect_uris":["myapp://cb"]}`:            "invalid_redirect_uri",
		`{"redirect_uris":[]}`:                        "invalid_redirect_uri",
		`{"redirect_uris":["http://127.0.0.1/cb"],"token_endpoint_auth_method":"client_secret_basic"}`: "invalid_client_metadata",
		`{"redirect_uris":["http://127.0.0.1/cb"],"grant_types":["client_credentials"]}`:               "invalid_client_metadata",
		`{"redirect_uris":["http://127.0.0.1/cb"],"response_types":["token"]}`:                         "invalid_client_metadata",
	} {
		res := h.do(jsonReq(t, h.srv.URL+"/oauth/register", body))
		var e tokens
		decode(t, res, &e)
		if res.StatusCode != 400 || e.Error != want {
			t.Errorf("%s: %d %q, want %q", body, res.StatusCode, e.Error, want)
		}
	}
	res = h.do(jsonReq(t, h.srv.URL+"/oauth/register", `{"redirect_uris":["http://127.0.0.1/callback"],"client_name":"Dyn"}`))
	var reg struct {
		ClientID   string   `json:"client_id"`
		GrantTypes []string `json:"grant_types"`
	}
	decode(t, res, &reg)
	if res.StatusCode != 201 || reg.ClientID == "" || len(reg.GrantTypes) != 2 {
		t.Fatalf("register: %d %+v", res.StatusCode, reg)
	}
	h.signIn("a@example.com")
	f := flow{clientID: reg.ClientID, redirectURI: "http://127.0.0.1:5555/callback"}
	f.verifier, f.challenge = pkce()
	handle := requestHandle(t, h.authorize(f.params()))
	var got struct {
		Request struct {
			Client struct {
				Host     string
				Verified bool
			}
		}
	}
	decode(t, h.get("/app-api/oauth/consent?request="+url.QueryEscape(handle)), &got)
	if got.Request.Client.Verified || got.Request.Client.Host != "127.0.0.1" {
		t.Fatalf("DCR consent client: %+v", got.Request.Client)
	}
	code := h.approve(handle).Get("code")

	// Turning DCR off afterwards.
	h.d.Config.OAuthDCREnabled = false
	res = h.authorize(f.params())
	if loc, _ := url.Parse(res.Header.Get("Location")); res.StatusCode != 302 || loc.Query().Get("error") != "unauthorized_client" {
		t.Fatalf("authorize with DCR off: %d %s", res.StatusCode, res.Header.Get("Location"))
	}
	if status, e := h.exchange(f, code); status != 401 || e.Error != "invalid_client" {
		t.Fatalf("token with DCR off: %d %q", status, e.Error)
	}
	h.d.Config.OAuthDCREnabled = true
	if status, _ := h.exchange(f, code); status != 200 {
		t.Fatalf("token with DCR on: %d", status)
	}
}

func jsonReq(t *testing.T, u, body string) *http.Request {
	t.Helper()
	req, _ := http.NewRequest("POST", u, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	return req
}

func TestGrants(t *testing.T) {
	h := newHarness(t, withMCP())
	h.signIn("a@example.com")
	f := h.newCIMDClient("/client")
	_, tk := h.exchange(f, h.code(f))

	var list struct {
		Grants []struct {
			ID     string
			Client struct {
				ClientID, Name string
				Verified       bool
			}
			LastUsedAt *time.Time
		}
	}
	decode(t, h.get("/app-api/oauth/grants"), &list)
	if len(list.Grants) != 1 || list.Grants[0].Client.ClientID != f.clientID || list.Grants[0].LastUsedAt == nil {
		t.Fatalf("grants: %+v", list)
	}
	id := list.Grants[0].ID

	del := func(id string) int {
		req, _ := http.NewRequest("DELETE", h.srv.URL+"/app-api/oauth/grants/"+id, nil)
		req.Header.Set("Origin", h.srv.URL)
		req.Header.Set(appapi.CSRFHeader, h.csrf())
		res := h.do(req)
		_, _ = io.Copy(io.Discard, res.Body)
		return res.StatusCode
	}
	h.signIn("b@example.com")
	if s := del(id); s != 404 {
		t.Fatalf("other user's grant: %d", s)
	}
	h.signOut()
	h.setSession(h.userID("a@example.com"))
	if s := del(id); s != 204 {
		t.Fatalf("delete: %d", s)
	}
	if res := h.bearer(tk.AccessToken); res.StatusCode != 401 {
		t.Fatal("token survived grant deletion")
	}
	if s, _ := h.refresh(f, tk.RefreshToken); s != 400 {
		t.Fatalf("refresh after grant deletion: %d", s)
	}
}
