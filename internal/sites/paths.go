package sites

import (
	"mime"
	"path"
	"strings"
	"unicode"
	"unicode/utf8"
)

// CleanPath validates a site file path and returns its canonical form.
// Paths are relative, slash-separated, and case-sensitive. Leading "./"
// segments are dropped; absolute paths, "." and ".." segments, empty
// segments (including a trailing slash), backslashes, NUL and other C0 and
// C1 control characters, invisible format characters (Unicode Cf: bidi
// overrides and isolates, zero-width characters, the BOM) that make a path
// display as another, invalid UTF-8, and the reserved top-level "_auth"
// directory (docs/design.md#origins-and-urls) are rejected with an
// *InputError.
func CleanPath(p string) (string, error) {
	orig := p
	for strings.HasPrefix(p, "./") {
		p = p[2:]
	}
	switch {
	case p == "":
		return "", inputErrorf("invalid path %q: empty", orig)
	case len(p) > MaxPathLen:
		return "", inputErrorf("invalid path %q: longer than %d bytes", orig, MaxPathLen)
	case !utf8.ValidString(p):
		return "", inputErrorf("invalid path %q: not UTF-8", orig)
	case strings.HasPrefix(p, "/"):
		return "", inputErrorf("invalid path %q: must be relative", orig)
	case strings.Contains(p, `\`):
		return "", inputErrorf("invalid path %q: contains a backslash", orig)
	case strings.ContainsFunc(p, unicode.IsControl):
		return "", inputErrorf("invalid path %q: contains a control character", orig)
	case strings.ContainsFunc(p, func(r rune) bool { return unicode.Is(unicode.Cf, r) }):
		return "", inputErrorf("invalid path %q: contains an invisible format character", orig)
	}
	for seg := range strings.SplitSeq(p, "/") {
		switch seg {
		case "":
			return "", inputErrorf("invalid path %q: empty segment", orig)
		case ".", "..":
			return "", inputErrorf("invalid path %q: contains a %q segment", orig, seg)
		}
	}
	if p == "_auth" || strings.HasPrefix(p, "_auth/") {
		return "", inputErrorf("invalid path %q: /_auth/ is reserved", orig)
	}
	return p, nil
}

// webTypes pins the content types of common web files so they do not
// depend on the host's mime database.
var webTypes = map[string]string{
	".html":        "text/html",
	".htm":         "text/html",
	".css":         "text/css",
	".js":          "text/javascript",
	".mjs":         "text/javascript",
	".cjs":         "text/javascript",
	".json":        "application/json",
	".map":         "application/json",
	".webmanifest": "application/manifest+json",
	".txt":         "text/plain",
	".md":          "text/markdown",
	".csv":         "text/csv",
	".xml":         "application/xml",
	".svg":         "image/svg+xml",
	".png":         "image/png",
	".jpg":         "image/jpeg",
	".jpeg":        "image/jpeg",
	".gif":         "image/gif",
	".webp":        "image/webp",
	".avif":        "image/avif",
	".ico":         "image/x-icon",
	".bmp":         "image/bmp",
	".woff":        "font/woff",
	".woff2":       "font/woff2",
	".ttf":         "font/ttf",
	".otf":         "font/otf",
	".wasm":        "application/wasm",
	".pdf":         "application/pdf",
	".mp4":         "video/mp4",
	".webm":        "video/webm",
	".mp3":         "audio/mpeg",
	".wav":         "audio/wav",
	".ogg":         "audio/ogg",
	".zip":         "application/zip",
}

// textual types that get "; charset=utf-8" besides text/*.
var utf8Types = map[string]bool{
	"application/json":          true,
	"application/manifest+json": true,
	"application/xml":           true,
	"image/svg+xml":             true,
	"application/javascript":    true,
}

// ContentTypeFor returns the content type for a path from its extension
// (case-insensitive): a fixed table of web types, then the mime package,
// then application/octet-stream. Textual types carry charset=utf-8.
func ContentTypeFor(p string) string {
	ext := strings.ToLower(path.Ext(p))
	ct := webTypes[ext]
	if ct == "" && ext != "" {
		ct = mime.TypeByExtension(ext)
	}
	if ct == "" {
		return "application/octet-stream"
	}
	return withCharset(ct)
}

func withCharset(ct string) string {
	base, params, err := mime.ParseMediaType(ct)
	if err != nil {
		return ct
	}
	if _, ok := params["charset"]; ok || !(strings.HasPrefix(base, "text/") || utf8Types[base]) {
		return ct
	}
	params["charset"] = "utf-8"
	return mime.FormatMediaType(base, params)
}

// contentType returns the caller's content type when given (validated) or
// the one derived from the path.
func contentType(p, given string) (string, error) {
	given = strings.TrimSpace(given)
	if given == "" {
		return ContentTypeFor(p), nil
	}
	base, params, err := mime.ParseMediaType(given)
	if err != nil || len(given) > 255 || !strings.Contains(base, "/") {
		return "", inputErrorf("invalid content type %q for %q", given, p)
	}
	return withCharset(mime.FormatMediaType(base, params)), nil
}
