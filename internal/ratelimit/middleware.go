package ratelimit

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"github.com/ferrule-io/handbill/internal/identity"
	"github.com/ferrule-io/handbill/internal/metrics"
	"github.com/ferrule-io/handbill/internal/server"
)

// Register installs the limits of Rules on both muxes unless
// RATE_LIMIT_ENABLED=false. The session middleware (internal/auth) must
// run first for ByUser rules to see the user; cmd/handbill registers
// modules in file-name order, and module_auth.go sorts before
// module_ratelimit.go.
func Register(r *server.Routes, d *server.Deps) error {
	if !d.Config.Operations.RateLimit {
		return nil
	}
	l, err := New(Rules, d.Config.Operations.TrustedProxies, time.Now)
	if err != nil {
		return err
	}
	r.UseApp(l.App(r.App))
	r.UseSites(l.Sites())
	return nil
}

// Limiter applies a rule table.
type Limiter struct {
	trusted []netip.Prefix
	app     map[string]*compiled
	sites   *compiled
}

type compiled struct {
	rule Rule
	key  *limiter
	ip   *limiter // nil unless rule.IP is set
}

// New compiles rules; trusted lists the proxies whose X-Forwarded-For is
// believed, and now is the clock.
func New(rules []Rule, trusted []netip.Prefix, now func() time.Time) (*Limiter, error) {
	l := &Limiter{trusted: trusted, app: map[string]*compiled{}}
	for _, r := range rules {
		if r.Limit.PerMinute <= 0 || r.Limit.Burst < 1 || (r.IP != nil && (r.IP.PerMinute <= 0 || r.IP.Burst < 1)) {
			return nil, fmt.Errorf("ratelimit: rule %s: limits need a positive rate and burst", r.Name)
		}
		c := &compiled{rule: r, key: newLimiter(r.Limit, now)}
		if r.IP != nil {
			c.ip = newLimiter(*r.IP, now)
		}
		if r.Sites {
			if l.sites != nil || len(r.Patterns) > 0 {
				return nil, fmt.Errorf("ratelimit: rule %s: one sites rule, without patterns", r.Name)
			}
			l.sites = c
			continue
		}
		for _, p := range r.Patterns {
			if _, dup := l.app[p]; dup {
				return nil, fmt.Errorf("ratelimit: pattern %q is in two rules", p)
			}
			l.app[p] = c
		}
	}
	return l, nil
}

// App limits app-host requests by the pattern mux serves them with.
func (l *Limiter) App(mux *http.ServeMux) server.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, pattern := mux.Handler(r)
			if c := l.app[pattern]; c != nil && !l.allow(c, w, r) {
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// Sites limits every site-host request.
func (l *Limiter) Sites() server.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if l.sites != nil && !l.allow(l.sites, w, r) {
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// allow takes a token from each of the request's buckets, or answers 429.
func (l *Limiter) allow(c *compiled, w http.ResponseWriter, r *http.Request) bool {
	ip := ipKey(ClientIP(r, l.trusted))
	if c.ip != nil {
		if ok, wait := c.ip.allow(ip); !ok {
			reject(c.rule.Name, w, r, wait)
			return false
		}
	}
	if ok, wait := c.key.allow(l.key(c.rule.Key, r, ip)); !ok {
		reject(c.rule.Name, w, r, wait)
		return false
	}
	return true
}

func (l *Limiter) key(k Key, r *http.Request, ip string) string {
	switch k {
	case ByUser:
		if u, ok := identity.FromContext(r.Context()); ok {
			return "user:" + u.ID.String()
		}
	case ByBearer:
		scheme, tok, _ := strings.Cut(r.Header.Get("Authorization"), " ")
		if tok = strings.TrimSpace(tok); strings.EqualFold(scheme, "Bearer") && tok != "" {
			sum := sha256.Sum256([]byte(tok))
			return "bearer:" + hex.EncodeToString(sum[:16])
		}
	}
	return ip
}

func reject(rule string, w http.ResponseWriter, r *http.Request, wait time.Duration) {
	metrics.RateLimited.WithLabelValues(rule).Inc()
	secs := int(math.Ceil(wait.Seconds()))
	if secs < 1 {
		secs = 1
	}
	h := w.Header()
	h.Set("Retry-After", strconv.Itoa(secs))
	h.Set("Cache-Control", "no-store")
	h.Set("X-Content-Type-Options", "nosniff")
	msg := fmt.Sprintf("Too many requests. Try again in %d seconds.", secs)
	var body any
	switch {
	case strings.HasPrefix(r.URL.Path, "/app-api/"):
		body = map[string]any{"error": map[string]string{"code": "rate_limited", "message": msg}}
	case r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/oauth/"):
		body = map[string]string{"error": "rate_limited", "error_description": msg}
	default:
		h.Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(msg + "\n"))
		return
	}
	h.Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusTooManyRequests)
	_ = json.NewEncoder(w).Encode(body)
}
