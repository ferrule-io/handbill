package oauth

import (
	"net/url"
	"strings"
)

// parseRedirectURI parses a redirect URI that may be registered (CIMD or
// DCR): absolute, at most maxRedirectURILen bytes, no fragment or userinfo,
// and either https with a host or a loopback http URI (127.0.0.1, [::1],
// or localhost; any port). A query is allowed.
func parseRedirectURI(s string) (*url.URL, bool) {
	if s == "" || len(s) > maxRedirectURILen || strings.Contains(s, "#") {
		return nil, false
	}
	u, err := url.Parse(s)
	if err != nil || !u.IsAbs() || u.User != nil || u.Opaque != "" || u.Fragment != "" {
		return nil, false
	}
	switch u.Scheme {
	case "https":
		return u, u.Hostname() != ""
	case "http":
		return u, isLoopbackHost(u.Hostname())
	}
	return nil, false
}

func isLoopbackHost(h string) bool { return h == "127.0.0.1" || h == "::1" || h == "localhost" }

func validRedirectURI(s string) bool {
	_, ok := parseRedirectURI(s)
	return ok
}

// isLoopbackRedirect reports whether a valid redirect URI is a loopback one.
func isLoopbackRedirect(s string) bool {
	u, ok := parseRedirectURI(s)
	return ok && u.Scheme == "http"
}

// matchRedirect reports whether a requested redirect URI matches one of
// the registered ones. A loopback registration matches the same hostname,
// path, and query on any port (OAuth 2.1 §8.4.2, RFC 8252 §7.3), which
// clients that pick a random port (Claude Code registers
// http://localhost/callback) need. Anything else must be byte-equal.
func matchRedirect(registered []string, requested string) bool {
	q, ok := parseRedirectURI(requested)
	if !ok {
		return false
	}
	for _, r := range registered {
		if r == requested {
			return true
		}
		ru, ok := parseRedirectURI(r)
		if !ok || ru.Scheme != "http" || q.Scheme != "http" {
			continue
		}
		if ru.Hostname() == q.Hostname() && ru.EscapedPath() == q.EscapedPath() && ru.RawQuery == q.RawQuery && ru.ForceQuery == q.ForceQuery {
			return true
		}
	}
	return false
}
