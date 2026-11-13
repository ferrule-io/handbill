package mcp_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/auth"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/ferrule-io/handbill/internal/appapi"
	agentauth "github.com/ferrule-io/handbill/internal/auth"
	"github.com/ferrule-io/handbill/internal/db"
	"github.com/ferrule-io/handbill/internal/mcp"
	"github.com/ferrule-io/handbill/internal/oauth"
	"github.com/ferrule-io/handbill/internal/safehttp"
	"github.com/ferrule-io/handbill/internal/server"
	"github.com/ferrule-io/handbill/internal/sites/sitestest"
	"github.com/ferrule-io/handbill/internal/token"
	"github.com/ferrule-io/handbill/internal/upload"
)

// TestSDKClientEndToEnd drives the full OAuth flow with the Go SDK's own
// client (CIMD, PRM and AS discovery, PKCE, iss checking) against the app
// behind TLS, then uses the tools, including an archive upload.
func TestSDKClientEndToEnd(t *testing.T) {
	d := sitestest.Deps(t)

	// The client's metadata document, on a separate TLS host.
	var cimdURL string
	cimd := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"client_id": cimdURL, "client_name": "SDK Test", "redirect_uris": []string{"http://127.0.0.1/callback"},
			"grant_types": []string{"authorization_code", "refresh_token"}, "token_endpoint_auth_method": "none",
		})
	}))
	t.Cleanup(cimd.Close)
	cimdURL = cimd.URL + "/client.json"
	pool := x509.NewCertPool()
	pool.AddCert(cimd.Certificate())
	cimdClient := safehttp.New(safehttp.Options{AllowLoopback: true, TLSClientConfig: &tls.Config{RootCAs: pool}, MaxBodyBytes: 5 << 10})

	// The app, as https://example.com:<port> (WebAuthn refuses IP RP IDs).
	ts := httptest.NewUnstartedServer(nil)
	ts.StartTLS()
	t.Cleanup(ts.Close)
	addr := ts.Listener.Addr().String()
	_, port, _ := net.SplitHostPort(addr)
	ts.URL = "https://example.com:" + port
	ts.Client().Transport.(*http.Transport).DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, addr)
	}
	d.Config.AppOrigin, _ = url.Parse(ts.URL)
	d.Config.AppHost = "example.com"
	h, err := server.New(d, agentauth.Register, appapi.Register, oauth.Module(oauth.Options{CIMD: cimdClient}), mcp.Register, upload.Register)
	if err != nil {
		t.Fatal(err)
	}
	ts.Config.Handler = h

	// A signed-in browser.
	user := sitestest.User(t, d, "agent@example.com")
	session, hash := token.New()
	if _, err := db.New(d.DB).CreateSession(t.Context(), db.CreateSessionParams{ID: uuid.Must(uuid.NewV7()), TokenHash: hash, UserID: user, UserAgent: "test", ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	jar, _ := cookiejar.New(nil)
	jar.SetCookies(d.Config.AppOrigin, []*http.Cookie{{Name: agentauth.SessionCookie, Value: session, Path: "/", Secure: true}})
	browser := &http.Client{Jar: jar, Transport: ts.Client().Transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

	// The fetcher plays the browser: open the authorization URL, approve on
	// the consent page, and hand the redirect's parameters back.
	fetcher := func(ctx context.Context, args *auth.AuthorizationArgs) (*auth.AuthorizationResult, error) {
		res, err := browser.Get(args.URL)
		if err != nil {
			return nil, err
		}
		res.Body.Close()
		loc, _ := url.Parse(res.Header.Get("Location"))
		if res.StatusCode != http.StatusSeeOther || loc.Path != "/consent" || !loc.Query().Has("request") {
			t.Fatalf("authorize: %d %s", res.StatusCode, loc)
		}
		res, err = browser.Get(ts.URL + "/app-api/me")
		if err != nil {
			return nil, err
		}
		var me struct {
			CSRFToken string `json:"csrfToken"`
		}
		_ = json.NewDecoder(res.Body).Decode(&me)
		res.Body.Close()
		body, _ := json.Marshal(map[string]any{"request": loc.Query().Get("request"), "approve": true})
		req, _ := http.NewRequest("POST", ts.URL+"/app-api/oauth/consent", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", ts.URL)
		req.Header.Set(appapi.CSRFHeader, me.CSRFToken)
		res, err = browser.Do(req)
		if err != nil {
			return nil, err
		}
		var out struct{ RedirectTo string }
		_ = json.NewDecoder(res.Body).Decode(&out)
		res.Body.Close()
		back, _ := url.Parse(out.RedirectTo)
		if back.Host != "127.0.0.1:43123" {
			t.Fatalf("redirect: %s", out.RedirectTo)
		}
		q := back.Query()
		return &auth.AuthorizationResult{Code: q.Get("code"), State: q.Get("state"), Iss: q.Get("iss")}, nil
	}
	oh, err := auth.NewAuthorizationCodeHandler(&auth.AuthorizationCodeHandlerConfig{
		ClientIDMetadataDocumentConfig: &auth.ClientIDMetadataDocumentConfig{URL: cimdURL},
		RedirectURL:                    "http://127.0.0.1:43123/callback",
		AuthorizationCodeFetcher:       fetcher,
		Client:                         ts.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	cs, err := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "sdk-test"}, nil).Connect(t.Context(),
		&mcpsdk.StreamableClientTransport{Endpoint: ts.URL + "/mcp", HTTPClient: ts.Client(), OAuthHandler: oh}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })

	tools, err := cs.ListTools(t.Context(), nil)
	if err != nil || len(tools.Tools) != 15 {
		t.Fatalf("ListTools: %v %d", err, len(tools.Tools))
	}
	call := func(name string, args map[string]any, out any) {
		t.Helper()
		res, err := cs.CallTool(t.Context(), &mcpsdk.CallToolParams{Name: name, Arguments: args})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		text := res.Content[0].(*mcpsdk.TextContent).Text
		if res.IsError {
			t.Fatalf("%s: %s", name, text)
		}
		if err := json.Unmarshal([]byte(text), out); err != nil {
			t.Fatal(err)
		}
	}
	var site struct{ ID, URL string }
	call("create_site", map[string]any{"title": "SDK site"}, &site)
	var pub struct{ URL string }
	call("publish_site", map[string]any{"site": site.ID, "mode": "replace", "files": []map[string]any{
		{"path": "index.html", "content": `<a href="about.html">About</a>`}, {"path": "about.html", "content": "About"},
	}}, &pub)
	var files struct{ Files []struct{ Path string } }
	call("list_files", map[string]any{"site": site.ID}, &files)
	var file struct{ Content string }
	call("read_file", map[string]any{"site": site.ID, "path": "about.html"}, &file)
	if len(files.Files) != 2 || file.Content != "About" {
		t.Fatalf("read back: %+v %+v", files, file)
	}

	// Staged publish through an upload URL, as an agent with curl would.
	var dep struct {
		DeploymentID string `json:"deployment_id"`
	}
	call("create_deployment", map[string]any{"site": site.ID, "mode": "replace"}, &dep)
	var up struct {
		UploadURL string `json:"upload_url"`
	}
	call("get_upload_url", map[string]any{"site": site.ID, "deployment_id": dep.DeploymentID}, &up)
	req, _ := http.NewRequest("PUT", up.UploadURL, bytes.NewReader(tarGzArchive(t, map[string]string{"dist/index.html": "v2", "dist/a/b.css": "b"})))
	res, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("upload: %d %s", res.StatusCode, b)
	}
	var fin struct {
		Version struct {
			FileCount int `json:"file_count"`
		}
	}
	call("finalize_deployment", map[string]any{"site": site.ID, "deployment_id": dep.DeploymentID}, &fin)
	call("read_file", map[string]any{"site": site.ID, "path": "a/b.css"}, &file)
	if fin.Version.FileCount != 2 || file.Content != "b" {
		t.Fatalf("finalize: %+v %q", fin, file.Content)
	}

	// A session cookie is no credential for /mcp.
	req, _ = http.NewRequest("POST", ts.URL+"/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	req.Header.Set("Content-Type", "application/json")
	res, err = browser.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 401 {
		t.Fatalf("cookie on /mcp: %d", res.StatusCode)
	}
}

func tarGzArchive(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, body := range files {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		_, _ = tw.Write([]byte(body))
	}
	_ = tw.Close()
	_ = gz.Close()
	return buf.Bytes()
}
