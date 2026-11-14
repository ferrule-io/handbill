package ratelimit

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ferrule-io/handbill/internal/identity"
)

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func newClock() *clock { return &clock{t: time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)} }

func (c *clock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

func TestBurstAndRefill(t *testing.T) {
	c := newClock()
	l := newLimiter(Limit{PerMinute: 60, Burst: 3}, c.now)
	for i := range 3 {
		if ok, _ := l.allow("a"); !ok {
			t.Fatalf("request %d of the burst refused", i+1)
		}
	}
	ok, wait := l.allow("a")
	if ok || wait != time.Second {
		t.Fatalf("over the burst: ok=%v wait=%v, want refused for 1s", ok, wait)
	}
	// Other keys have their own bucket.
	if ok, _ := l.allow("b"); !ok {
		t.Fatal("another key was refused")
	}
	// One token per second refills.
	c.advance(500 * time.Millisecond)
	if ok, wait := l.allow("a"); ok || wait != 500*time.Millisecond {
		t.Fatalf("half refilled: ok=%v wait=%v", ok, wait)
	}
	c.advance(500 * time.Millisecond)
	if ok, _ := l.allow("a"); !ok {
		t.Fatal("refilled token refused")
	}
	if ok, _ := l.allow("a"); ok {
		t.Fatal("second token after one second allowed")
	}
	// Refill stops at the burst.
	c.advance(time.Hour)
	for i := range 3 {
		if ok, _ := l.allow("a"); !ok {
			t.Fatalf("request %d after an idle hour refused", i+1)
		}
	}
	if ok, _ := l.allow("a"); ok {
		t.Fatal("burst exceeded after an idle hour")
	}
}

func TestSweepDropsOnlyFullBuckets(t *testing.T) {
	c := newClock()
	l := newLimiter(Limit{PerMinute: 1, Burst: 2}, c.now)
	l.allow("idle")
	l.allow("busy")
	l.allow("busy")
	c.advance(sweepEvery + time.Second) // idle refilled; busy has 1+ tokens of 2
	l.allow("other")
	l.mu.Lock()
	_, idle := l.buckets["idle"]
	_, busy := l.buckets["busy"]
	l.mu.Unlock()
	if idle || !busy {
		t.Fatalf("after sweep: idle kept=%v busy kept=%v", idle, busy)
	}
	// The kept bucket kept its state: one refilled token, then empty.
	if ok, _ := l.allow("busy"); !ok {
		t.Fatal("busy refused its refilled token")
	}
	if ok, _ := l.allow("busy"); ok {
		t.Fatal("sweep reset a partly used bucket")
	}
}

func req(remote string, xff ...string) *http.Request {
	r := httptest.NewRequest("GET", "http://dash.example.com/", nil)
	r.RemoteAddr = remote
	for _, v := range xff {
		r.Header.Add("X-Forwarded-For", v)
	}
	return r
}

func TestClientIP(t *testing.T) {
	proxies := []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8"), netip.MustParsePrefix("fd00::/8")}
	cases := []struct {
		name    string
		r       *http.Request
		trusted []netip.Prefix
		want    string
	}{
		{"no proxies: RemoteAddr", req("203.0.113.9:4000", "198.51.100.1"), nil, "203.0.113.9"},
		{"untrusted peer's header ignored", req("203.0.113.9:4000", "198.51.100.1"), proxies, "203.0.113.9"},
		{"trusted proxy", req("10.1.2.3:4000", "198.51.100.1"), proxies, "198.51.100.1"},
		{"spoofed left entries ignored", req("10.1.2.3:4000", "1.1.1.1, 2.2.2.2, 198.51.100.1"), proxies, "198.51.100.1"},
		{"chain of trusted proxies", req("10.1.2.3:4000", "6.6.6.6, 198.51.100.1, 10.9.9.9"), proxies, "198.51.100.1"},
		{"spoof split across headers", req("10.1.2.3:4000", "6.6.6.6", "198.51.100.1, 10.9.9.9"), proxies, "198.51.100.1"},
		{"spoofed trusted address on the left", req("10.1.2.3:4000", "10.0.0.1, 198.51.100.1"), proxies, "198.51.100.1"},
		{"garbage stops at last trusted hop", req("10.1.2.3:4000", "198.51.100.1, garbage"), proxies, "10.1.2.3"},
		{"all trusted: leftmost", req("10.1.2.3:4000", "10.5.5.5"), proxies, "10.5.5.5"},
		{"trusted proxy without header", req("10.1.2.3:4000"), proxies, "10.1.2.3"},
		{"ipv6 peer", req("[2001:db8::1]:4000"), proxies, "2001:db8::1"},
		{"ipv6 behind proxy", req("[fd00::5]:80", "2001:db8::7"), proxies, "2001:db8::7"},
		{"mapped ipv4 peer", req("[::ffff:10.1.2.3]:80", "198.51.100.1"), proxies, "198.51.100.1"},
		{"xff entry with port", req("10.1.2.3:4000", "198.51.100.1:5555"), proxies, "198.51.100.1"},
	}
	for _, tc := range cases {
		if got := ClientIP(tc.r, tc.trusted); got.String() != tc.want {
			t.Errorf("%s: got %s, want %s", tc.name, got, tc.want)
		}
	}
}

func TestIPKeyGroupsIPv6By64(t *testing.T) {
	a, b := netip.MustParseAddr("2001:db8:1:2::1"), netip.MustParseAddr("2001:db8:1:2:ffff::9")
	if ipKey(a) != ipKey(b) || ipKey(a) != "ip:2001:db8:1:2::/64" {
		t.Fatalf("ipKey: %s %s", ipKey(a), ipKey(b))
	}
	if ipKey(netip.MustParseAddr("2001:db8:1:3::1")) == ipKey(a) {
		t.Fatal("different /64s share a key")
	}
	if ipKey(netip.MustParseAddr("192.0.2.1")) == ipKey(netip.MustParseAddr("192.0.2.2")) {
		t.Fatal("IPv4 addresses share a key")
	}
}

// fixture serves a mux behind the limiter's middleware.
type fixture struct {
	clock *clock
	app   http.Handler
	sites http.Handler
}

func newFixture(t *testing.T, rules []Rule, trusted []netip.Prefix) *fixture {
	t.Helper()
	c := newClock()
	l, err := New(rules, trusted, c.now)
	if err != nil {
		t.Fatal(err)
	}
	ok := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("ok")) })
	mux := http.NewServeMux()
	for _, p := range []string{"POST /app-api/auth/recovery/signin", "POST /oauth/token", "GET /oauth/authorize", "POST /mcp", "POST /app-api/invites/accept", "GET /"} {
		mux.Handle(p, ok)
	}
	return &fixture{clock: c, app: l.App(mux)(mux), sites: l.Sites()(ok)}
}

func (f *fixture) do(h http.Handler, method, path, remote string, edit ...func(*http.Request)) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "http://dash.example.com"+path, nil)
	r.RemoteAddr = remote
	for _, e := range edit {
		e(r)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

var testRules = []Rule{
	{Name: "recovery", Patterns: []string{"POST /app-api/auth/recovery/signin"}, Key: ByIP, Limit: Limit{PerMinute: 6, Burst: 2}},
	{Name: "oauth_token", Patterns: []string{"POST /oauth/token"}, Key: ByIP, Limit: Limit{PerMinute: 60, Burst: 1}},
	{Name: "oauth_authorize", Patterns: []string{"GET /oauth/authorize"}, Key: ByIP, Limit: Limit{PerMinute: 60, Burst: 1}},
	{Name: "invite_accept", Patterns: []string{"POST /app-api/invites/accept"}, Key: ByUser, Limit: Limit{PerMinute: 60, Burst: 1}},
	{Name: "mcp", Patterns: []string{"POST /mcp"}, Key: ByBearer, Limit: Limit{PerMinute: 60, Burst: 2}, IP: &Limit{PerMinute: 60, Burst: 3}},
	{Name: "sites", Sites: true, Key: ByIP, Limit: Limit{PerMinute: 60, Burst: 1}},
}

func TestMiddlewareRejectsWithRetryAfter(t *testing.T) {
	f := newFixture(t, testRules, nil)
	for i := range 2 {
		if w := f.do(f.app, "POST", "/app-api/auth/recovery/signin", "198.51.100.1:1"); w.Code != 200 {
			t.Fatalf("request %d: %d", i+1, w.Code)
		}
	}
	w := f.do(f.app, "POST", "/app-api/auth/recovery/signin", "198.51.100.1:2")
	if w.Code != 429 || w.Header().Get("Retry-After") != "10" || w.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("over limit: %d %v", w.Code, w.Header())
	}
	var body struct {
		Error struct{ Code, Message string } `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || body.Error.Code != "rate_limited" {
		t.Fatalf("app-api body: %s", w.Body)
	}
	// Another client is unaffected; unlisted routes are never limited.
	if w := f.do(f.app, "POST", "/app-api/auth/recovery/signin", "198.51.100.2:1"); w.Code != 200 {
		t.Fatalf("other client: %d", w.Code)
	}
	for range 5 {
		if w := f.do(f.app, "GET", "/anything", "198.51.100.1:1"); w.Code != 200 {
			t.Fatalf("unlisted route: %d", w.Code)
		}
	}
	f.clock.advance(10 * time.Second)
	if w := f.do(f.app, "POST", "/app-api/auth/recovery/signin", "198.51.100.1:1"); w.Code != 200 {
		t.Fatalf("after Retry-After: %d", w.Code)
	}
}

func TestMiddlewareResponseStyles(t *testing.T) {
	f := newFixture(t, testRules, nil)
	f.do(f.app, "POST", "/oauth/token", "198.51.100.1:1")
	w := f.do(f.app, "POST", "/oauth/token", "198.51.100.1:1")
	var oauthBody map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &oauthBody); w.Code != 429 || err != nil || oauthBody["error"] != "rate_limited" || oauthBody["error_description"] == "" {
		t.Fatalf("oauth: %d %s", w.Code, w.Body)
	}
	f.do(f.app, "GET", "/oauth/authorize", "198.51.100.1:1")
	w = f.do(f.app, "GET", "/oauth/authorize", "198.51.100.1:1")
	if w.Code != 429 || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/plain") || w.Header().Get("Retry-After") != "1" {
		t.Fatalf("authorize: %d %v", w.Code, w.Header())
	}
	f.do(f.sites, "GET", "/index.html", "198.51.100.1:1")
	w = f.do(f.sites, "GET", "/app.css", "198.51.100.1:1")
	if w.Code != 429 || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/plain") {
		t.Fatalf("sites: %d %v", w.Code, w.Header())
	}
}

func TestMiddlewareKeysByUser(t *testing.T) {
	f := newFixture(t, testRules, nil)
	as := func(id uuid.UUID) func(*http.Request) {
		return func(r *http.Request) { *r = *r.WithContext(identity.WithUser(r.Context(), identity.User{ID: id})) }
	}
	alice, bob := uuid.New(), uuid.New()
	if w := f.do(f.app, "POST", "/app-api/invites/accept", "198.51.100.1:1", as(alice)); w.Code != 200 {
		t.Fatalf("alice: %d", w.Code)
	}
	if w := f.do(f.app, "POST", "/app-api/invites/accept", "203.0.113.5:1", as(alice)); w.Code != 429 {
		t.Fatalf("alice from another address: %d, want 429 (keyed by user)", w.Code)
	}
	if w := f.do(f.app, "POST", "/app-api/invites/accept", "198.51.100.1:1", as(bob)); w.Code != 200 {
		t.Fatalf("bob on alice's address: %d", w.Code)
	}
	// Signed out falls back to the address.
	if w := f.do(f.app, "POST", "/app-api/invites/accept", "198.51.100.1:1"); w.Code != 200 {
		t.Fatalf("signed out: %d", w.Code)
	}
	if w := f.do(f.app, "POST", "/app-api/invites/accept", "198.51.100.1:1"); w.Code != 429 {
		t.Fatalf("signed out again: %d", w.Code)
	}
}

func TestMiddlewareKeysByBearerAndIP(t *testing.T) {
	f := newFixture(t, testRules, nil)
	bearer := func(tok string) func(*http.Request) {
		return func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+tok) }
	}
	// The token's bucket (burst 2) is shared across addresses.
	f.do(f.app, "POST", "/mcp", "198.51.100.1:1", bearer("token-a"))
	f.do(f.app, "POST", "/mcp", "198.51.100.2:1", bearer("token-a"))
	if w := f.do(f.app, "POST", "/mcp", "198.51.100.3:1", bearer("token-a")); w.Code != 429 {
		t.Fatalf("token over its limit: %d", w.Code)
	}
	// Fresh made-up tokens still hit the per-address cap (burst 3).
	f.do(f.app, "POST", "/mcp", "203.0.113.1:1", bearer("t1"))
	f.do(f.app, "POST", "/mcp", "203.0.113.1:1", bearer("t2"))
	f.do(f.app, "POST", "/mcp", "203.0.113.1:1", bearer("t3"))
	if w := f.do(f.app, "POST", "/mcp", "203.0.113.1:1", bearer("t4")); w.Code != 429 {
		t.Fatalf("rotating tokens from one address: %d", w.Code)
	}
}

func TestMiddlewareTrustedProxySpoofing(t *testing.T) {
	proxy := []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}
	f := newFixture(t, testRules, proxy)
	xff := func(v string) func(*http.Request) {
		return func(r *http.Request) { r.Header.Set("X-Forwarded-For", v) }
	}
	// Behind the proxy, each real client has its own bucket.
	if w := f.do(f.app, "POST", "/oauth/token", "10.0.0.2:1", xff("198.51.100.1")); w.Code != 200 {
		t.Fatalf("client 1: %d", w.Code)
	}
	if w := f.do(f.app, "POST", "/oauth/token", "10.0.0.2:1", xff("198.51.100.2")); w.Code != 200 {
		t.Fatalf("client 2: %d", w.Code)
	}
	// Client 1 prepends forged addresses; the proxy appends the real one.
	if w := f.do(f.app, "POST", "/oauth/token", "10.0.0.2:1", xff("203.0.113.77, 198.51.100.1")); w.Code != 429 {
		t.Fatalf("forged X-Forwarded-For escaped the limit: %d", w.Code)
	}
	// A client talking to the server directly cannot claim to be anyone.
	f.do(f.app, "POST", "/oauth/token", "192.0.2.50:1", xff("198.51.100.9"))
	if w := f.do(f.app, "POST", "/oauth/token", "192.0.2.50:1", xff("198.51.100.10")); w.Code != 429 {
		t.Fatalf("direct client rotated X-Forwarded-For: %d", w.Code)
	}
}

func TestNewValidates(t *testing.T) {
	bad := [][]Rule{
		{{Name: "a", Patterns: []string{"GET /x"}, Limit: Limit{PerMinute: 0, Burst: 1}}},
		{{Name: "a", Patterns: []string{"GET /x"}, Limit: Limit{PerMinute: 1, Burst: 0}}},
		{{Name: "a", Patterns: []string{"GET /x"}, Limit: Limit{PerMinute: 1, Burst: 1}, IP: &Limit{}}},
		{{Name: "a", Patterns: []string{"GET /x"}, Limit: Limit{PerMinute: 1, Burst: 1}}, {Name: "b", Patterns: []string{"GET /x"}, Limit: Limit{PerMinute: 1, Burst: 1}}},
		{{Name: "s", Sites: true, Patterns: []string{"GET /"}, Limit: Limit{PerMinute: 1, Burst: 1}}},
	}
	for i, rules := range bad {
		if _, err := New(rules, nil, time.Now); err == nil {
			t.Errorf("rule set %d accepted", i)
		}
	}
	if _, err := New(Rules, nil, time.Now); err != nil {
		t.Fatalf("Rules: %v", err)
	}
}
