// Package siteserver serves hosted sites on <id>.SITES_DOMAIN and runs the
// site sign-in handoff (docs/design.md#site-sign-in-handoff):
//
//   - GET /auth/site on the app host checks the app session and the viewer
//     role, then issues a single-use ticket to the site host;
//   - GET /_auth/callback on the site host redeems the ticket and sets the
//     __Host-site cookie;
//   - GET /_auth/share on the site host serves a script-only page that
//     moves the public-link token (docs/design.md#public-links) from the
//     fragment into POST /_auth/share, which redeems it for a cookie
//     without a user;
//   - every other GET on the site host is authorized from that cookie
//     against the site's current access state and streams the requested
//     file of the current version from storage. Service worker scripts are
//     refused, so site content cannot intercept /_auth/ navigations or
//     outlive revocation in a visitor's browser.
package siteserver

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/ferrule-io/handbill/internal/db"
	"github.com/ferrule-io/handbill/internal/identity"
	"github.com/ferrule-io/handbill/internal/rbac"
	"github.com/ferrule-io/handbill/internal/server"
	"github.com/ferrule-io/handbill/internal/siteid"
	"github.com/ferrule-io/handbill/internal/sites"
	"github.com/ferrule-io/handbill/internal/storage"
	"github.com/ferrule-io/handbill/internal/token"
)

const (
	// CookieName is the site host's authorization cookie. The __Host-
	// prefix makes it host-only, Secure, and Path=/.
	CookieName = "__Host-site"
	// CookieTTL is the site cookie's lifetime. The cookie is re-checked
	// against the database on every request, and while the app session
	// lives an expired cookie is renewed by an invisible handoff.
	CookieTTL = 12 * time.Hour
	// TicketTTL is how long a handoff ticket can be redeemed.
	TicketTTL = 60 * time.Second
	// maxReturnPath caps the stored return path.
	maxReturnPath = 2048
)

// Authorized reports whether a verified site cookie admits its holder to
// the site described by a: member cookies need a role that can view, and
// public-link cookies (no user) need the link enabled at the cookie's
// generation, so disabling or rotating the link (which bumps the
// generation) refuses every earlier cookie on its next request.
func Authorized(c token.SiteCookie, a sites.Access) bool {
	if c.SiteID != a.SiteID {
		return false
	}
	if c.UserID != uuid.Nil {
		return rbac.Can(a.Role, rbac.View)
	}
	return a.PublicLinkEnabled && c.LinkGen > 0 && c.LinkGen == a.PublicLinkGen
}

// Register installs GET /auth/site on the app host and the file server,
// /_auth/callback, /_auth/share, and the reserved /_auth/ namespace on site
// hosts.
func Register(r *server.Routes, d *server.Deps) error {
	h := newHandler(d)
	if d.Config.AppOrigin.Scheme == "https" {
		// Site hosts share the app's scheme (config.SiteOrigin).
		r.UseSites(func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Strict-Transport-Security", "max-age=31536000")
				next.ServeHTTP(w, r)
			})
		})
	}
	r.App.HandleFunc("GET /auth/site", h.handoff)
	r.Sites.HandleFunc("GET /", h.serve)
	// Reserved on every site host; never served from site content.
	r.Sites.HandleFunc("GET /_auth/", h.reserved)
	r.Sites.HandleFunc("GET /_auth/callback", h.callback)
	r.Sites.HandleFunc("GET /_auth/share", h.shareOpener)
	r.Sites.HandleFunc("POST /_auth/share", h.share)
	return nil
}

type handler struct {
	deps   *server.Deps
	sites  *sites.Service
	signer *token.Signer
	now    func() time.Time
}

func newHandler(d *server.Deps) *handler {
	return &handler{deps: d, sites: sites.NewService(d), signer: token.NewSigner(d.Config.SecretKey), now: time.Now}
}

// ValidReturnPath reports whether p is a same-origin path to return to: a
// single leading slash (not "//", which browsers read as another host), no
// backslashes or control characters, and no scheme or host.
func ValidReturnPath(p string) bool {
	if len(p) == 0 || len(p) > maxReturnPath || p[0] != '/' || strings.HasPrefix(p, "//") {
		return false
	}
	if strings.ContainsFunc(p, func(r rune) bool { return r == '\\' || r < 0x20 || r == 0x7f }) {
		return false
	}
	u, err := url.Parse(p)
	return err == nil && u.Scheme == "" && u.Host == "" && u.User == nil
}

func (h *handler) serve(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	siteID := server.SiteID(ctx)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	// Browsers send "Service-Worker: script" when fetching a service
	// worker's script. A worker registered by site content would control
	// /_auth/ navigations (stealing tickets and public-link tokens) and keep
	// serving cached content after its visitor's access is revoked, so no
	// site may register one.
	if r.Header.Get("Service-Worker") != "" {
		plain(w, http.StatusForbidden, "Service workers are not allowed on hosted sites.\n")
		return
	}
	access, ok, err := h.authorize(r, siteID)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	if !ok {
		h.toHandoff(w, r, siteID)
		return
	}
	if !access.CurrentVersionID.Valid {
		plain(w, http.StatusNotFound, "Nothing has been published to this site yet.\n")
		return
	}
	version := access.CurrentVersionID.UUID
	fi, err := h.sites.FindFile(ctx, version, candidates(r.URL.Path)...)
	status := http.StatusOK
	if errors.Is(err, sites.ErrNotFound) {
		status = http.StatusNotFound
		fi, err = h.sites.FindFile(ctx, version, "404.html")
		if errors.Is(err, sites.ErrNotFound) {
			plain(w, http.StatusNotFound, "404 page not found\n")
			return
		}
	}
	if err != nil {
		h.fail(w, r, err)
		return
	}
	if status == http.StatusOK {
		if target, ok := canonicalPath(r.URL.Path, fi.Path); ok {
			// Directory indexes live at their slash form so the page's
			// relative links resolve inside the directory; a stray slash
			// after a file name is dropped.
			target = (&url.URL{Path: target}).EscapedPath()
			if r.URL.RawQuery != "" {
				target += "?" + r.URL.RawQuery
			}
			w.Header().Set("Cache-Control", "private, no-cache")
			http.Redirect(w, r, target, http.StatusMovedPermanently)
			return
		}
	}
	h.serveFile(w, r, siteID, fi, status)
}

// authorize verifies the site cookie and checks it against the site's
// current access state.
func (h *handler) authorize(r *http.Request, siteID string) (sites.Access, bool, error) {
	c, err := r.Cookie(CookieName)
	if err != nil {
		return sites.Access{}, false, nil
	}
	sc, err := h.signer.ParseSiteCookie(c.Value, h.now())
	if err != nil || sc.SiteID != siteID {
		return sites.Access{}, false, nil
	}
	a, err := h.sites.Access(r.Context(), siteID, sc.UserID)
	if errors.Is(err, sites.ErrNotFound) {
		return sites.Access{}, false, nil
	}
	if err != nil {
		return sites.Access{}, false, err
	}
	return a, Authorized(sc, a), nil
}

// candidates maps a request path to site paths, in order: "/" →
// index.html; "/dir/" → dir/index.html, then the file dir (a stray slash
// after a file name); "/foo" → foo, foo/index.html, foo.html.
func candidates(urlPath string) []string {
	p := strings.TrimPrefix(urlPath, "/")
	if p == "" {
		return []string{"index.html"}
	}
	if strings.HasSuffix(p, "/") {
		return []string{p + "index.html", strings.TrimSuffix(p, "/")}
	}
	return []string{p, p + "/index.html", p + ".html"}
}

// canonicalPath reports where a request for urlPath that resolved to the
// site file filePath should redirect: a directory index (dir/index.html,
// whether requested as "/dir", "/dir/index.html", or "/dir/index.html/")
// to "/dir/", and a file requested with a trailing slash to the file.
// "/foo" answered by foo.html is not redirected.
func canonicalPath(urlPath, filePath string) (string, bool) {
	if filePath == "index.html" || strings.HasSuffix(filePath, "/index.html") {
		dir := "/" + strings.TrimSuffix(filePath, "index.html")
		return dir, urlPath != dir
	}
	if strings.HasSuffix(urlPath, "/") {
		return "/" + filePath, true
	}
	return "", false
}

// serveFile streams fi with status. A 200 honors If-None-Match and a single
// byte range (206, or 416 when unsatisfiable); 404.html is always whole.
func (h *handler) serveFile(w http.ResponseWriter, r *http.Request, siteID string, fi sites.FileInfo, status int) {
	hdr := w.Header()
	hdr.Set("Cache-Control", "private, no-cache")
	etag := `"` + fi.SHA256 + `"`
	var start, length int64 = 0, fi.Size
	ranged := false
	if status == http.StatusOK {
		hdr.Set("ETag", etag)
		hdr.Set("Accept-Ranges", "bytes")
		if etagMatch(r.Header.Get("If-None-Match"), etag) {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		if rh := r.Header.Get("Range"); rh != "" {
			if ir := r.Header.Get("If-Range"); ir == "" || ir == etag {
				s, n, ok, unsatisfiable := parseRange(rh, fi.Size)
				if unsatisfiable {
					hdr.Set("Content-Range", "bytes */"+strconv.FormatInt(fi.Size, 10))
					hdr.Set("Content-Length", "0")
					w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
					return
				}
				if ok {
					start, length, ranged = s, n, true
				}
			}
		}
	}
	var body io.ReadCloser
	if r.Method != http.MethodHead {
		var obj *storage.Object
		var err error
		if ranged {
			obj, err = h.sites.OpenBlobRange(r.Context(), siteID, fi.SHA256, start, length)
		} else {
			obj, err = h.sites.OpenBlob(r.Context(), siteID, fi.SHA256)
		}
		if err != nil {
			hdr.Del("ETag")
			hdr.Del("Accept-Ranges")
			h.fail(w, r, err)
			return
		}
		defer obj.Body.Close()
		body = obj.Body
	}
	hdr.Set("Content-Type", fi.ContentType)
	hdr.Set("Content-Length", strconv.FormatInt(length, 10))
	if ranged {
		hdr.Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, start+length-1, fi.Size))
		status = http.StatusPartialContent
	}
	w.WriteHeader(status)
	if body != nil {
		_, _ = io.Copy(w, body)
	}
}

// parseRange parses a single "bytes=" range against size. ok=false means
// serve the whole file (absent, malformed, multi-range, or non-bytes
// header); unsatisfiable=true means answer 416.
func parseRange(header string, size int64) (start, length int64, ok, unsatisfiable bool) {
	spec, found := strings.CutPrefix(header, "bytes=")
	if !found {
		return 0, 0, false, false
	}
	spec = strings.TrimSpace(spec)
	if strings.Contains(spec, ",") {
		return 0, 0, false, false
	}
	a, b, found := strings.Cut(spec, "-")
	if !found {
		return 0, 0, false, false
	}
	num := func(s string) (int64, bool) {
		for i := range len(s) {
			if s[i] < '0' || s[i] > '9' {
				return 0, false
			}
		}
		n, err := strconv.ParseInt(s, 10, 64)
		return n, err == nil
	}
	if a == "" {
		n, valid := num(b)
		if b == "" || !valid {
			return 0, 0, false, false
		}
		if n == 0 || size == 0 {
			return 0, 0, false, true
		}
		n = min(n, size)
		return size - n, n, true, false
	}
	start, valid := num(a)
	if !valid {
		return 0, 0, false, false
	}
	end := size - 1
	if b != "" {
		if end, valid = num(b); !valid {
			return 0, 0, false, false
		}
	}
	if start >= size {
		return 0, 0, false, true
	}
	if end < start {
		return 0, 0, false, false
	}
	end = min(end, size-1)
	return start, end - start + 1, true, false
}

// etagMatch implements If-None-Match's weak comparison.
func etagMatch(header, etag string) bool {
	for t := range strings.SplitSeq(header, ",") {
		t = strings.TrimSpace(t)
		if t == "*" || strings.TrimPrefix(t, "W/") == etag {
			return true
		}
	}
	return false
}

// toHandoff sends an unauthorized visitor to the app to sign in and get a
// ticket for this site and path.
func (h *handler) toHandoff(w http.ResponseWriter, r *http.Request, siteID string) {
	q := url.Values{"site": {siteID}, "return": {r.URL.RequestURI()}}
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, h.deps.Config.AppOrigin.String()+"/auth/site?"+q.Encode(), http.StatusFound)
}

// handoff (app host) issues a ticket for a site the signed-in user can view.
func (h *handler) handoff(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	q := r.URL.Query()
	siteID := q.Get("site")
	ret := q.Get("return")
	if ret == "" {
		ret = "/"
	}
	if !siteid.Valid(siteID) || !ValidReturnPath(ret) {
		page(w, http.StatusBadRequest, "Invalid sign-in link", "This link to a site is malformed. Open the site’s address again.", "")
		return
	}
	user, ok := identity.FromContext(r.Context())
	if !ok {
		http.Redirect(w, r, "/login?return="+url.QueryEscape(r.URL.RequestURI()), http.StatusFound)
		return
	}
	role, err := h.sites.Role(r.Context(), siteID, user.ID)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	if !rbac.Can(role, rbac.View) {
		// Same answer whether or not the site exists: the site page explains
		// that the signed-in account has no access.
		http.Redirect(w, r, "/site?id="+siteID, http.StatusFound)
		return
	}
	plaintext, hash := token.New()
	_, err = db.New(h.deps.DB).CreateSiteTicket(r.Context(), db.CreateSiteTicketParams{
		ID:         uuid.Must(uuid.NewV7()),
		TokenHash:  hash,
		SiteID:     siteID,
		UserID:     user.ID,
		ReturnPath: ret,
		ExpiresAt:  h.now().Add(TicketTTL),
	})
	if err != nil {
		h.fail(w, r, err)
		return
	}
	http.Redirect(w, r, h.deps.Config.SiteOrigin(siteID)+"/_auth/callback?ticket="+url.QueryEscape(plaintext), http.StatusFound)
}

// callback (site host) redeems a ticket, sets the site cookie, and returns
// to the requested path.
func (h *handler) callback(w http.ResponseWriter, r *http.Request) {
	siteID := server.SiteID(r.Context())
	hdr := w.Header()
	hdr.Set("Cache-Control", "no-store")
	hdr.Set("Referrer-Policy", "no-referrer")
	hdr.Set("X-Content-Type-Options", "nosniff")
	t := r.URL.Query().Get("ticket")
	if t == "" {
		page(w, http.StatusBadRequest, "Sign-in link incomplete", "This sign-in link is missing its ticket.", "/")
		return
	}
	row, err := db.New(h.deps.DB).ConsumeSiteTicket(r.Context(), token.Hash(t))
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		h.fail(w, r, err)
		return
	}
	if err != nil || row.SiteID != siteID {
		page(w, http.StatusBadRequest, "Sign-in link expired", "This sign-in link was already used or has expired.", "/")
		return
	}
	ret := row.ReturnPath
	if !ValidReturnPath(ret) {
		ret = "/"
	}
	clearSiteStorage(w)
	h.setCookie(w, token.SiteCookie{SiteID: siteID, UserID: row.UserID})
	http.Redirect(w, r, ret, http.StatusFound)
}

// clearSiteStorage asks the browser to drop the site origin's storage
// (including any service worker registered before workers were refused)
// whenever a site cookie is issued. "storage" leaves cookies alone, so the
// cookie set in the same response survives.
func clearSiteStorage(w http.ResponseWriter) {
	w.Header().Set("Clear-Site-Data", `"storage"`)
}

// maxShareForm caps the POST /_auth/share body: one token field.
const maxShareForm = 1 << 10

// shareOpener (site host) answers the public link,
// /_auth/share#token=<token>. The token travels in the fragment so it never
// reaches server, proxy, or CDN logs; this page's only script moves it into
// a same-origin POST /_auth/share and clears it from the address bar.
func (h *handler) shareOpener(w http.ResponseWriter, r *http.Request) {
	hdr := w.Header()
	hdr.Set("Content-Type", "text/html; charset=utf-8")
	hdr.Set("Content-Security-Policy", shareOpenerCSP)
	hdr.Set("Referrer-Policy", "no-referrer")
	hdr.Set("X-Content-Type-Options", "nosniff")
	hdr.Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = io.WriteString(w, shareOpenerPage)
	}
}

// share (site host) redeems this site's public link from the opener page's
// form: it sets a site cookie without a user, carrying the link's
// generation, and redirects to "/". Every failure (missing or unknown
// token, another site's link, a disabled link) gets the same neutral page.
// It needs no Origin check: forcing a browser to redeem a link only gives
// it the anonymous access the link grants.
func (h *handler) share(w http.ResponseWriter, r *http.Request) {
	siteID := server.SiteID(r.Context())
	hdr := w.Header()
	hdr.Set("Cache-Control", "no-store")
	hdr.Set("Referrer-Policy", "no-referrer")
	hdr.Set("X-Content-Type-Options", "nosniff")
	r.Body = http.MaxBytesReader(w, r.Body, maxShareForm)
	var tok string
	if err := r.ParseForm(); err == nil {
		tok = r.PostForm.Get("token")
	}
	inactive := func() {
		page(w, http.StatusNotFound, "This link isn’t active",
			"It may have been turned off or replaced with a new one. Ask the person who shared it for the current link.", "")
	}
	if tok == "" {
		inactive()
		return
	}
	link, err := db.New(h.deps.DB).GetPublicLinkByToken(r.Context(), token.Hash(tok))
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		h.fail(w, r, err)
		return
	}
	if err != nil || link.SiteID != siteID || !link.Enabled {
		inactive()
		return
	}
	clearSiteStorage(w)
	h.setCookie(w, token.SiteCookie{SiteID: siteID, LinkGen: link.Generation})
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// setCookie issues the site cookie for c, expiring CookieTTL from now.
func (h *handler) setCookie(w http.ResponseWriter, c token.SiteCookie) {
	c.Expires = h.now().Add(CookieTTL)
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    h.signer.SiteCookie(c),
		Path:     "/",
		Expires:  c.Expires,
		MaxAge:   int(CookieTTL / time.Second),
		Secure:   true,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}

// reserved answers every /_auth/ path no more specific route handles.
func (h *handler) reserved(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Referrer-Policy", "no-referrer")
	plain(w, http.StatusNotFound, "404 page not found\n")
}

func (h *handler) fail(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, context.Canceled) {
		return
	}
	h.deps.Logger.Error("site request failed", "path", server.LogPath(r.URL.Path), "err", err)
	plain(w, http.StatusInternalServerError, "internal server error\n")
}

func plain(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, body)
}

// pageHead is the shared head of the site host's own pages up to the title.
const pageHead = `<!doctype html>
<html lang="en"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="color-scheme" content="light dark">
`

const pageStyle = `<style>
:root{--bg:#f7f5f0;--fg:#1d1b18;--muted:#6b665d;--card:#fffdf9;--ring:#e4dfd4;--accent:#c2410c}
@media (prefers-color-scheme:dark){:root{--bg:#141311;--fg:#ece8e1;--muted:#9c968c;--card:#1c1a17;--ring:#2e2b26;--accent:#fb923c}}
*{box-sizing:border-box}body{margin:0;min-height:100vh;display:grid;place-items:center;background:var(--bg);color:var(--fg);
font:16px/1.55 ui-sans-serif,system-ui,-apple-system,"Segoe UI",sans-serif;padding:1.5rem}
main{max-width:26rem;width:100%;background:var(--card);border:1px solid var(--ring);border-radius:1rem;padding:2rem;box-shadow:0 1px 2px rgb(0 0 0/.04)}
p.eyebrow{margin:0 0 .5rem;font-size:.75rem;letter-spacing:.12em;text-transform:uppercase;color:var(--accent);font-weight:600}
h1{margin:0 0 .5rem;font-size:1.35rem;line-height:1.25;font-weight:600}p{margin:0;color:var(--muted)}
a{display:inline-block;margin-top:1.25rem;color:var(--fg);font-weight:600;text-underline-offset:3px}
</style>`

var pageTmpl = template.Must(template.New("page").Parse(pageHead + `<title>{{.Title}} · handbill</title>
` + pageStyle + `</head><body><main>
<p class="eyebrow">handbill</p><h1>{{.Title}}</h1><p>{{.Message}}</p>
{{if .Link}}<a href="{{.Link}}">Try again</a>{{end}}
</main></body></html>`))

// shareOpenerScript reads the token from the fragment, removes it from the
// address bar and history, and posts it to /_auth/share.
const shareOpenerScript = `(function(){var t=new URLSearchParams(location.hash.slice(1)).get("token");` +
	`history.replaceState(null,"",location.pathname+location.search);` +
	`if(!t){document.getElementById("msg").textContent="This link is incomplete. Open the whole link you were sent.";return}` +
	`var f=document.getElementById("share");f.elements.token.value=t;f.submit()})()`

var (
	shareOpenerPage = pageHead + "<title>Opening site · handbill</title>\n" + pageStyle + `</head><body><main>
<p class="eyebrow">handbill</p><h1>Opening site</h1><p id="msg">One moment…</p>
<noscript><p>Turn on JavaScript to open this link.</p></noscript>
<form id="share" method="post" action="/_auth/share"><input type="hidden" name="token"></form>
<script>` + shareOpenerScript + `</script>
</main></body></html>`
	// shareOpenerCSP allows exactly the opener's inline script and a form
	// post to this origin.
	shareOpenerCSP = "default-src 'none'; script-src " + scriptHash(shareOpenerScript) +
		"; style-src 'unsafe-inline'; form-action 'self'; base-uri 'none'; frame-ancestors 'none'"
)

// scriptHash is a CSP hash source for an inline script's exact text.
func scriptHash(script string) string {
	sum := sha256.Sum256([]byte(script))
	return "'sha256-" + base64.StdEncoding.EncodeToString(sum[:]) + "'"
}

// page renders a small self-contained HTML message (no scripts, no
// external resources).
func page(w http.ResponseWriter, status int, title, message, link string) {
	hdr := w.Header()
	hdr.Set("Content-Type", "text/html; charset=utf-8")
	hdr.Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'")
	hdr.Set("X-Content-Type-Options", "nosniff")
	hdr.Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = pageTmpl.Execute(w, struct{ Title, Message, Link string }{title, message, link})
}
