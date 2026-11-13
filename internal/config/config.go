// Package config loads and validates the server configuration from the
// environment. The variables form the deployment contract documented in
// docs/design.md#configuration; the Helm chart sets exactly these.
package config

import (
	"errors"
	"fmt"
	"net"
	"net/mail"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// MinSecretKeyLen is the minimum length of SECRET_KEY in bytes.
const MinSecretKeyLen = 32

// Config is the validated server configuration.
type Config struct {
	// ListenAddr is the TCP address the HTTP server binds (LISTEN_ADDR).
	ListenAddr string
	// AppOrigin is the app's origin, e.g. https://dash.example.com (APP_ORIGIN).
	// It has a scheme and host and no path, query, or fragment.
	AppOrigin *url.URL
	// AppHost is AppOrigin's hostname, lowercased, without port.
	AppHost string
	// SitesDomain is the parent domain of site hosts, e.g. sites.example.com
	// (SITES_DOMAIN), lowercased, without port.
	SitesDomain string
	// SecretKey keys HMAC-signed values (site cookies, upload URLs).
	SecretKey []byte

	// Database is the parsed pool configuration: DATABASE_URL when set,
	// otherwise the libpq environment variables (PGHOST, PGPORT, ...).
	Database *pgxpool.Config

	Storage Storage
	// SMTP is nil when SMTP_HOST is empty (notifications disabled).
	SMTP *SMTP
	// Google is nil when Google sign-in is not configured.
	Google *Google

	// OAuthDCREnabled serves /oauth/register (OAUTH_DCR_ENABLED).
	OAuthDCREnabled bool
	// InviteTTL is the lifetime of invites and ownership transfers (INVITE_TTL).
	InviteTTL time.Duration
}

// StorageType selects the storage adapter.
type StorageType string

const (
	StorageLocal StorageType = "local"
	StorageS3    StorageType = "s3"
	StorageGCS   StorageType = "gcs"
)

// Storage configures the storage adapter. Only the fields for Type are set.
type Storage struct {
	Type StorageType
	// LocalDir is the root directory for the local adapter (STORAGE_LOCAL_DIR).
	LocalDir string
	S3       S3
	GCS      GCS
}

// S3 configures the S3 adapter. Credentials come from the AWS SDK default chain.
type S3 struct {
	Bucket string
	Region string
	// Endpoint overrides the S3 endpoint (e.g. MinIO); empty uses AWS.
	Endpoint string
	// Prefix is prepended to every object key; no leading or trailing slash.
	Prefix         string
	ForcePathStyle bool
}

// GCS configures the GCS adapter. Credentials come from Application Default Credentials.
type GCS struct {
	Bucket string
	// Prefix is prepended to every object key; no leading or trailing slash.
	Prefix string
}

// SMTPTLS selects how the SMTP connection is secured.
type SMTPTLS string

const (
	SMTPStartTLS SMTPTLS = "starttls"
	SMTPTLSOnly  SMTPTLS = "tls"
	SMTPNoTLS    SMTPTLS = "none"
)

// SMTP configures outgoing mail.
type SMTP struct {
	Host     string
	Port     int
	Username string
	Password string
	From     *mail.Address
	TLS      SMTPTLS
}

// Google configures Google OIDC sign-in.
type Google struct {
	ClientID     string
	ClientSecret string
}

// Load reads the configuration through getenv (os.Getenv in production) and
// validates it. The returned error lists every invalid variable.
//
// When DATABASE_URL is empty, pgx reads the libpq variables from the process
// environment, not through getenv.
func Load(getenv func(string) string) (*Config, error) {
	l := loader{getenv: getenv}
	c := &Config{
		ListenAddr:      l.str("LISTEN_ADDR", ":8080"),
		OAuthDCREnabled: l.boolean("OAUTH_DCR_ENABLED", false),
		InviteTTL:       l.duration("INVITE_TTL", 14*24*time.Hour),
	}
	c.AppOrigin, c.AppHost = l.appOrigin()
	c.SitesDomain = l.sitesDomain()
	if c.AppHost != "" && c.SitesDomain != "" &&
		(c.AppHost == c.SitesDomain || strings.HasSuffix(c.AppHost, "."+c.SitesDomain)) {
		l.fail("APP_ORIGIN", "host %q must not be SITES_DOMAIN or a subdomain of it", c.AppHost)
	}
	c.SecretKey = l.secretKey()
	c.Database = l.database()
	c.Storage = l.storage()
	c.SMTP = l.smtp()
	c.Google = l.google()
	if err := l.err(); err != nil {
		return nil, err
	}
	return c, nil
}

type loader struct {
	getenv func(string) string
	errs   []string
}

func (l *loader) fail(name, format string, args ...any) {
	l.errs = append(l.errs, name+": "+fmt.Sprintf(format, args...))
}

func (l *loader) err() error {
	if len(l.errs) == 0 {
		return nil
	}
	return errors.New("invalid configuration:\n  " + strings.Join(l.errs, "\n  "))
}

func (l *loader) str(name, def string) string {
	if v := strings.TrimSpace(l.getenv(name)); v != "" {
		return v
	}
	return def
}

func (l *loader) required(name string) string {
	v := strings.TrimSpace(l.getenv(name))
	if v == "" {
		l.fail(name, "is required")
	}
	return v
}

func (l *loader) boolean(name string, def bool) bool {
	v := strings.TrimSpace(l.getenv(name))
	if v == "" {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		l.fail(name, "must be a boolean (true or false), got %q", v)
	}
	return b
}

func (l *loader) duration(name string, def time.Duration) time.Duration {
	v := strings.TrimSpace(l.getenv(name))
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		l.fail(name, "must be a positive Go duration such as 336h, got %q", v)
	}
	return d
}

func (l *loader) appOrigin() (*url.URL, string) {
	v := l.required("APP_ORIGIN")
	if v == "" {
		return nil, ""
	}
	u, err := url.Parse(v)
	switch {
	case err != nil:
		l.fail("APP_ORIGIN", "is not a URL: %v", err)
	case u.Scheme != "http" && u.Scheme != "https":
		l.fail("APP_ORIGIN", "must start with https:// or http://, got %q", v)
	case u.Host == "" || u.Hostname() == "":
		l.fail("APP_ORIGIN", "must include a host, got %q", v)
	case u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "":
		l.fail("APP_ORIGIN", "must be an origin (scheme://host[:port]) without path, query, or credentials, got %q", v)
	default:
		origin := &url.URL{Scheme: u.Scheme, Host: strings.ToLower(u.Host)}
		return origin, strings.ToLower(u.Hostname())
	}
	return nil, ""
}

func (l *loader) sitesDomain() string {
	v := strings.ToLower(strings.TrimSuffix(l.required("SITES_DOMAIN"), "."))
	if v == "" {
		return ""
	}
	if !validHostname(v) || !strings.Contains(v, ".") {
		l.fail("SITES_DOMAIN", "must be a domain name such as sites.example.com (no scheme, port, or path), got %q", v)
		return ""
	}
	return v
}

// validHostname reports whether s is a DNS name of letters, digits, and hyphens.
func validHostname(s string) bool {
	if len(s) > 253 {
		return false
	}
	for label := range strings.SplitSeq(s, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, r := range label {
			if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' {
				return false
			}
		}
	}
	return true
}

func (l *loader) secretKey() []byte {
	v := l.getenv("SECRET_KEY")
	if v == "" {
		l.fail("SECRET_KEY", "is required (at least %d bytes; generate with `openssl rand -base64 48`)", MinSecretKeyLen)
		return nil
	}
	if len(v) < MinSecretKeyLen {
		l.fail("SECRET_KEY", "must be at least %d bytes, got %d", MinSecretKeyLen, len(v))
		return nil
	}
	return []byte(v)
}

func (l *loader) database() *pgxpool.Config {
	dsn := strings.TrimSpace(l.getenv("DATABASE_URL"))
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		// pgx's error text embeds the connection string with best-effort
		// password masking; never risk echoing a password into logs.
		if dsn == "" {
			l.fail("PGHOST/PGPORT/PGUSER/PGPASSWORD/PGDATABASE/PGSSLMODE", "cannot be parsed into a connection configuration")
		} else {
			l.fail("DATABASE_URL", "cannot be parsed; expected postgres://user:password@host:port/dbname?sslmode=... or libpq key=value pairs")
		}
		return nil
	}
	return cfg
}

func (l *loader) storage() Storage {
	s := Storage{Type: StorageType(strings.ToLower(l.str("STORAGE_TYPE", string(StorageLocal))))}
	switch s.Type {
	case StorageLocal:
		s.LocalDir = l.str("STORAGE_LOCAL_DIR", "/data")
	case StorageS3:
		s.S3 = S3{
			Bucket:         l.required("S3_BUCKET"),
			Region:         l.required("S3_REGION"),
			Endpoint:       strings.TrimSpace(l.getenv("S3_ENDPOINT")),
			Prefix:         strings.Trim(strings.TrimSpace(l.getenv("S3_PREFIX")), "/"),
			ForcePathStyle: l.boolean("S3_FORCE_PATH_STYLE", false),
		}
		if e := s.S3.Endpoint; e != "" {
			if u, err := url.Parse(e); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
				l.fail("S3_ENDPOINT", "must be an http(s) URL, got %q", e)
			}
		}
	case StorageGCS:
		s.GCS = GCS{
			Bucket: l.required("GCS_BUCKET"),
			Prefix: strings.Trim(strings.TrimSpace(l.getenv("GCS_PREFIX")), "/"),
		}
	default:
		l.fail("STORAGE_TYPE", "must be local, s3, or gcs, got %q", s.Type)
	}
	return s
}

func (l *loader) smtp() *SMTP {
	host := strings.TrimSpace(l.getenv("SMTP_HOST"))
	if host == "" {
		return nil
	}
	s := &SMTP{
		Host:     host,
		Username: l.getenv("SMTP_USERNAME"),
		Password: l.getenv("SMTP_PASSWORD"),
		TLS:      SMTPTLS(strings.ToLower(l.str("SMTP_TLS", string(SMTPStartTLS)))),
	}
	if strings.ContainsAny(host, ":/") {
		l.fail("SMTP_HOST", "must be a hostname without port or scheme, got %q", host)
	}
	defPort := 587
	switch s.TLS {
	case SMTPStartTLS:
	case SMTPTLSOnly:
		defPort = 465
	case SMTPNoTLS:
		defPort = 25
	default:
		l.fail("SMTP_TLS", "must be starttls, tls, or none, got %q", s.TLS)
	}
	s.Port = defPort
	if v := strings.TrimSpace(l.getenv("SMTP_PORT")); v != "" {
		p, err := strconv.Atoi(v)
		if err != nil || p < 1 || p > 65535 {
			l.fail("SMTP_PORT", "must be a port number, got %q", v)
		}
		s.Port = p
	}
	if (s.Username == "") != (s.Password == "") {
		l.fail("SMTP_USERNAME", "and SMTP_PASSWORD must be set together")
	}
	if from := l.required("SMTP_FROM"); from != "" {
		addr, err := mail.ParseAddress(from)
		if err != nil {
			l.fail("SMTP_FROM", "must be an email address such as \"handbill <dash@example.com>\", got %q", from)
		}
		s.From = addr
	}
	return s
}

func (l *loader) google() *Google {
	id := strings.TrimSpace(l.getenv("GOOGLE_CLIENT_ID"))
	secret := strings.TrimSpace(l.getenv("GOOGLE_CLIENT_SECRET"))
	if id == "" && secret == "" {
		return nil
	}
	if id == "" || secret == "" {
		l.fail("GOOGLE_CLIENT_ID", "and GOOGLE_CLIENT_SECRET must be set together")
		return nil
	}
	return &Google{ClientID: id, ClientSecret: secret}
}

// SiteOrigin is the origin of a site host, using APP_ORIGIN's scheme and port,
// e.g. https://abcdefghijkl.sites.example.com.
func (c *Config) SiteOrigin(siteID string) string {
	host := siteID + "." + c.SitesDomain
	if p := c.AppOrigin.Port(); p != "" {
		host = net.JoinHostPort(host, p)
	}
	return c.AppOrigin.Scheme + "://" + host
}
