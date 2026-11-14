package config

import (
	"net"
	"net/netip"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Limits caps what one publish or request may contain
// (docs/design.md#configuration).
type Limits struct {
	// MaxFileSize caps one file, uploaded directly or extracted from an
	// archive (MAX_FILE_SIZE).
	MaxFileSize int64
	// MaxSiteSize caps the total bytes of one version's files, so of a
	// site as served (MAX_SITE_SIZE).
	MaxSiteSize int64
	// MaxSiteFiles caps the files in one version (MAX_SITE_FILES).
	MaxSiteFiles int
	// MaxArchiveSize caps one archive's uncompressed file bytes and its
	// compressed upload (MAX_ARCHIVE_SIZE).
	MaxArchiveSize int64
	// MaxArchiveEntries caps the entries, files and directories, in one
	// archive (MAX_ARCHIVE_ENTRIES).
	MaxArchiveEntries int
	// MaxMCPBody caps an /mcp request body (MAX_MCP_BODY).
	MaxMCPBody int64
	// MaxJSONBody caps a JSON request body under /app-api/ (MAX_JSON_BODY).
	MaxJSONBody int64
}

// DefaultLimits are the limits when no variable is set.
func DefaultLimits() Limits {
	return Limits{
		MaxFileSize:       512 << 20,
		MaxSiteSize:       1 << 30,
		MaxSiteFiles:      10_000,
		MaxArchiveSize:    512 << 20,
		MaxArchiveEntries: 10_000,
		MaxMCPBody:        16 << 20,
		MaxJSONBody:       1 << 20,
	}
}

// Operations configures how the server runs alongside its replicas.
type Operations struct {
	// RateLimit enables the per-replica rate limits (RATE_LIMIT_ENABLED).
	RateLimit bool
	// TrustedProxies are the proxies whose X-Forwarded-For is believed
	// (TRUSTED_PROXIES); empty means the client is RemoteAddr.
	TrustedProxies []netip.Prefix
	// MetricsAddr serves Prometheus /metrics on its own listener
	// (METRICS_ADDR); empty disables it.
	MetricsAddr string
	// MaintenanceInterval is how often one replica deletes expired rows
	// and unused blobs (MAINTENANCE_INTERVAL).
	MaintenanceInterval time.Duration
	// BlobGCGrace is how old an unreferenced blob must be before it is
	// deleted (BLOB_GC_GRACE, at least MinBlobGCGrace).
	BlobGCGrace time.Duration
	// ShutdownTimeout is how long in-flight requests may finish after
	// SIGTERM (SHUTDOWN_TIMEOUT).
	ShutdownTimeout time.Duration
}

// MinBlobGCGrace keeps blob GC clear of uploads in progress.
const MinBlobGCGrace = time.Hour

func (l *loader) limits() Limits {
	d := DefaultLimits()
	c := Limits{
		MaxFileSize:       l.bytes("MAX_FILE_SIZE", d.MaxFileSize),
		MaxSiteSize:       l.bytes("MAX_SITE_SIZE", d.MaxSiteSize),
		MaxSiteFiles:      l.count("MAX_SITE_FILES", d.MaxSiteFiles),
		MaxArchiveSize:    l.bytes("MAX_ARCHIVE_SIZE", d.MaxArchiveSize),
		MaxArchiveEntries: l.count("MAX_ARCHIVE_ENTRIES", d.MaxArchiveEntries),
		MaxMCPBody:        l.bytes("MAX_MCP_BODY", d.MaxMCPBody),
		MaxJSONBody:       l.bytes("MAX_JSON_BODY", d.MaxJSONBody),
	}
	if c.MaxFileSize > 0 && c.MaxSiteSize > 0 && c.MaxFileSize > c.MaxSiteSize {
		l.fail("MAX_FILE_SIZE", "must not exceed MAX_SITE_SIZE (%d bytes)", c.MaxSiteSize)
	}
	if c.MaxFileSize > 0 && c.MaxArchiveSize > 0 && c.MaxFileSize > c.MaxArchiveSize {
		l.fail("MAX_FILE_SIZE", "must not exceed MAX_ARCHIVE_SIZE (%d bytes)", c.MaxArchiveSize)
	}
	return c
}

func (l *loader) operations(listenAddr string) Operations {
	o := Operations{
		RateLimit:           l.boolean("RATE_LIMIT_ENABLED", true),
		TrustedProxies:      l.prefixes("TRUSTED_PROXIES"),
		MetricsAddr:         l.str("METRICS_ADDR", ""),
		MaintenanceInterval: l.duration("MAINTENANCE_INTERVAL", 15*time.Minute),
		BlobGCGrace:         l.duration("BLOB_GC_GRACE", 24*time.Hour),
		ShutdownTimeout:     l.duration("SHUTDOWN_TIMEOUT", 25*time.Second),
	}
	if o.BlobGCGrace > 0 && o.BlobGCGrace < MinBlobGCGrace {
		l.fail("BLOB_GC_GRACE", "must be at least %v, got %v", MinBlobGCGrace, o.BlobGCGrace)
	}
	if a := o.MetricsAddr; a != "" {
		if _, port, err := net.SplitHostPort(a); err != nil || port == "" {
			l.fail("METRICS_ADDR", "must be a host:port such as :9090, got %q", a)
		} else if a == listenAddr {
			l.fail("METRICS_ADDR", "must differ from LISTEN_ADDR: /metrics is never served on the public listener")
		}
	}
	return o
}

var bytesRE = regexp.MustCompile(`^([0-9]+)\s*(B|KiB|MiB|GiB|TiB)?$`)

// bytes parses a positive byte count: a plain integer or one with a binary
// unit suffix (64MiB, 1GiB).
func (l *loader) bytes(name string, def int64) int64 {
	v := strings.TrimSpace(l.getenv(name))
	if v == "" {
		return def
	}
	if n, ok := parseBytes(v); ok {
		return n
	}
	l.fail(name, "must be a positive byte size such as 67108864 or 64MiB, got %q", v)
	return 0
}

func parseBytes(v string) (int64, bool) {
	m := bytesRE.FindStringSubmatch(v)
	if m == nil {
		return 0, false
	}
	n, err := strconv.ParseInt(m[1], 10, 64)
	if err != nil || n <= 0 {
		return 0, false
	}
	shift := map[string]uint{"": 0, "B": 0, "KiB": 10, "MiB": 20, "GiB": 30, "TiB": 40}[m[2]]
	if n > (1<<62)>>shift {
		return 0, false
	}
	return n << shift, true
}

// count parses a positive integer.
func (l *loader) count(name string, def int) int {
	v := strings.TrimSpace(l.getenv(name))
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		l.fail(name, "must be a positive integer, got %q", v)
		return 0
	}
	return n
}

// prefixes parses a comma-separated list of CIDRs; a bare address is a
// single-host prefix.
func (l *loader) prefixes(name string) []netip.Prefix {
	var out []netip.Prefix
	for _, f := range strings.Split(l.getenv(name), ",") {
		f = strings.TrimSpace(f)
		if f == "" {
			continue
		}
		p, err := netip.ParsePrefix(f)
		if err != nil {
			a, aerr := netip.ParseAddr(f)
			if aerr != nil {
				l.fail(name, "must be a comma-separated list of CIDRs such as 10.0.0.0/8, got %q", f)
				continue
			}
			p = netip.PrefixFrom(a, a.BitLen())
		}
		out = append(out, p.Masked())
	}
	return out
}
