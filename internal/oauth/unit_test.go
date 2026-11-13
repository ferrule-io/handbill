package oauth

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/ferrule-io/handbill/internal/config"
	"github.com/ferrule-io/handbill/internal/server"
)

func testModule(t *testing.T) *module {
	t.Helper()
	cfg, err := config.Load(func(k string) string {
		return map[string]string{
			"APP_ORIGIN": "https://dash.example.com", "SITES_DOMAIN": "sites.example.com",
			"SECRET_KEY": strings.Repeat("s", 32), "DATABASE_URL": "postgres://unused/x",
		}[k]
	})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)
	return &module{d: &server.Deps{Config: cfg}, now: func() time.Time { return now }}
}

func TestMatchRedirect(t *testing.T) {
	cases := []struct {
		registered []string
		requested  string
		want       bool
	}{
		{[]string{"http://localhost/callback"}, "http://localhost:5555/callback", true},
		{[]string{"http://localhost/callback"}, "http://localhost/callback", true},
		{[]string{"http://localhost/callback"}, "http://127.0.0.1:5555/callback", false},
		{[]string{"http://localhost/callback"}, "http://localhost:5555/other", false},
		{[]string{"http://localhost/callback"}, "https://localhost/callback", false},
		{[]string{"http://[::1]/cb"}, "http://[::1]:43123/cb", true},
		{[]string{"http://127.0.0.1/cb?x=1"}, "http://127.0.0.1:9/cb?x=1", true},
		{[]string{"http://127.0.0.1/cb?x=1"}, "http://127.0.0.1:9/cb?x=2", false},
		{[]string{"https://claude.ai/api/mcp/auth_callback"}, "https://claude.ai/api/mcp/auth_callback", true},
		{[]string{"https://claude.ai/api/mcp/auth_callback"}, "https://claude.ai/api/mcp/auth_callback/", false},
		{[]string{"https://claude.ai/api/mcp/auth_callback"}, "https://claude.ai/api/mcp/auth_callback?a=b", false},
		{[]string{"https://claude.ai:443/cb"}, "https://claude.ai:8443/cb", false},
		{[]string{"http://localhost/callback"}, "http://localhost:5555/callback#x", false},
	}
	for _, c := range cases {
		if got := matchRedirect(c.registered, c.requested); got != c.want {
			t.Errorf("matchRedirect(%q, %q) = %v, want %v", c.registered, c.requested, got, c.want)
		}
	}
}

func TestValidRedirectURI(t *testing.T) {
	for s, want := range map[string]bool{
		"https://example.com/cb":               true,
		"http://localhost/callback":            true,
		"http://127.0.0.1:33418/":              true,
		"http://[::1]/cb":                      true,
		"http://example.com/cb":                false,
		"myapp://cb":                           false,
		"https://example.com/cb#frag":          false,
		"https://user@example.com/cb":          false,
		"/relative":                            false,
		"https://" + strings.Repeat("a", 2048): false,
	} {
		if got := validRedirectURI(s); got != want {
			t.Errorf("validRedirectURI(%q) = %v, want %v", s, got, want)
		}
	}
}

func TestValidCIMDURL(t *testing.T) {
	m := testModule(t)
	for s, want := range map[string]bool{
		"https://claude.ai/oauth/claude-code-client-metadata": true,
		"https://client.example:8443/meta.json":               true,
		"https://1.2.3.4/meta":                                true, // safehttp refuses special addresses at dial time
		"http://client.example/meta":                          false,
		"https://user@client.example/meta":                    false,
		"https://client.example":                              false,
		"https://client.example/":                             false,
		"https://client.example/a/../b":                       false,
		"https://client.example/a/./b":                        false,
		"https://client.example/meta?x=1":                     false,
		"https://client.example/meta#f":                       false,
		"https://Client.example/meta":                         false, // not canonical
		"https://dash.example.com/meta":                       false, // APP_ORIGIN
		"https://sites.example.com/meta":                      false,
		"https://abcdefghijkl.sites.example.com/meta":         false,
		"https://client.example/" + strings.Repeat("a", 2048): false,
	} {
		if got := m.validCIMDURL(s); got != want {
			t.Errorf("validCIMDURL(%q) = %v, want %v", s, got, want)
		}
	}
}

func TestValidateCIMD(t *testing.T) {
	const id = "https://client.example/meta"
	ok := `"client_id":"` + id + `","client_name":"App","redirect_uris":["http://localhost/cb"]`
	cases := map[string]string{
		`{` + ok + `}`: "",
		`{` + ok + `,"token_endpoint_auth_methods_supported":["none","private_key_jwt"]}`:                        "",
		`{` + ok + `,"grant_types":["authorization_code","urn:ietf:params:oauth:grant-type:jwt-bearer"]}`:        "",
		`{"client_id":"https://other.example/meta","client_name":"App","redirect_uris":["http://localhost/cb"]}`: errClientMetadataInvalid,
		`<html>`: errClientMetadataInvalid,
		`{"client_id":"` + id + `","client_name":" ","redirect_uris":["http://localhost/cb"]}`:                         errClientMetadataInvalid,
		`{"client_id":"` + id + `","client_name":"App","redirect_uris":["myapp://cb"]}`:                                errClientMetadataInvalid,
		`{"client_id":"` + id + `","client_name":"App"}`:                                                               errClientMetadataInvalid,
		`{` + ok + `,"client_secret":"x"}`:                                                                             errClientMetadataInvalid,
		`{` + ok + `,"jwks":{},"jwks_uri":"https://x/"}`:                                                               errClientMetadataInvalid,
		`{` + ok + `,"token_endpoint_auth_methods_supported":["client_secret_basic","none"]}`:                          errClientMetadataInvalid,
		`{` + ok + `,"token_endpoint_auth_method":"private_key_jwt"}`:                                                  errInvalidClient,
		`{` + ok + `,"token_endpoint_auth_method":"none","token_endpoint_auth_methods_supported":["private_key_jwt"]}`: errClientMetadataInvalid,
		`{` + ok + `,"grant_types":["client_credentials"]}`:                                                            errClientMetadataInvalid,
		`{` + ok + `,"response_types":["token"]}`:                                                                      errClientMetadataInvalid,
	}
	for doc, want := range cases {
		if _, got := validateCIMD(id, []byte(doc)); got != want {
			t.Errorf("validateCIMD(%s) = %q, want %q", doc, got, want)
		}
	}
	meta, _ := validateCIMD(id, []byte(`{"client_id":"`+id+`","client_name":"A\u0007pp","redirect_uris":["myapp://x","http://localhost/cb"]}`))
	if meta.name != "App" || len(meta.redirectURIs) != 1 {
		t.Errorf("cleaned metadata = %+v", meta)
	}
}

func TestCacheExpiry(t *testing.T) {
	m := testModule(t)
	now := m.now()
	for cc, want := range map[string]time.Duration{
		"public, max-age=300":       5 * time.Minute,
		"max-age=1":                 cimdMinTTL,
		"max-age=999999":            cimdMaxTTL,
		"max-age=99999999999999999": cimdMaxTTL,
		"no-store":                  0,
		"no-cache":                  0,
		"max-age=0":                 0,
		"":                          cimdDefaultTTL,
	} {
		h := http.Header{}
		if cc != "" {
			h.Set("Cache-Control", cc)
		}
		if got := m.cacheExpiry(h).Sub(now); got != want {
			t.Errorf("cacheExpiry(%q) = %v, want %v", cc, got, want)
		}
	}
}

func TestPKCE(t *testing.T) {
	// RFC 7636 appendix B.
	v := "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
	c := "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM"
	if !validVerifier(v) || !validChallenge(c) || !verifyPKCE(v, c) || verifyPKCE(v+"x", c) {
		t.Fatal("PKCE vector failed")
	}
	if validChallenge(strings.Repeat("a", 42)) || validVerifier(strings.Repeat("a", 42)) {
		t.Fatal("short values accepted")
	}
}
