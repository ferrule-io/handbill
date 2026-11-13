package config

import (
	"strings"
	"testing"
	"time"
)

func env(kv map[string]string) func(string) string {
	return func(k string) string { return kv[k] }
}

func base() map[string]string {
	return map[string]string{
		"APP_ORIGIN":   "https://Dash.Example.com",
		"SITES_DOMAIN": "sites.example.com",
		"SECRET_KEY":   strings.Repeat("k", 32),
		"DATABASE_URL": "postgres://u:p@db:5432/agentdash?sslmode=disable",
	}
}

func with(m map[string]string, kv ...string) map[string]string {
	for i := 0; i < len(kv); i += 2 {
		m[kv[i]] = kv[i+1]
	}
	return m
}

func TestDefaults(t *testing.T) {
	c, err := Load(env(base()))
	if err != nil {
		t.Fatal(err)
	}
	if c.ListenAddr != ":8080" {
		t.Errorf("ListenAddr = %q", c.ListenAddr)
	}
	if c.AppOrigin.String() != "https://dash.example.com" || c.AppHost != "dash.example.com" {
		t.Errorf("AppOrigin = %q, AppHost = %q", c.AppOrigin, c.AppHost)
	}
	if c.Storage.Type != StorageLocal || c.Storage.LocalDir != "/data" {
		t.Errorf("Storage = %+v", c.Storage)
	}
	if c.InviteTTL != 336*time.Hour || c.OAuthDCREnabled {
		t.Errorf("InviteTTL = %v, DCR = %v", c.InviteTTL, c.OAuthDCREnabled)
	}
	if c.SMTP != nil || c.Google != nil {
		t.Errorf("SMTP = %+v, Google = %+v", c.SMTP, c.Google)
	}
	if c.Database.ConnConfig.Host != "db" || c.Database.ConnConfig.Database != "agentdash" {
		t.Errorf("Database = %+v", c.Database.ConnConfig)
	}
	if got := c.SiteOrigin("abcdefghijkl"); got != "https://abcdefghijkl.sites.example.com" {
		t.Errorf("SiteOrigin = %q", got)
	}
}

func TestDatabaseFromLibpqEnv(t *testing.T) {
	t.Setenv("PGHOST", "pg.internal")
	t.Setenv("PGPORT", "6543")
	t.Setenv("PGDATABASE", "dash")
	m := base()
	delete(m, "DATABASE_URL")
	c, err := Load(env(m))
	if err != nil {
		t.Fatal(err)
	}
	cc := c.Database.ConnConfig
	if cc.Host != "pg.internal" || cc.Port != 6543 || cc.Database != "dash" {
		t.Errorf("ConnConfig = %s:%d/%s", cc.Host, cc.Port, cc.Database)
	}
}

func TestFullConfig(t *testing.T) {
	c, err := Load(env(with(base(),
		"LISTEN_ADDR", "127.0.0.1:9000",
		"APP_ORIGIN", "http://dash.localhost:8080",
		"SITES_DOMAIN", "sites.localhost",
		"STORAGE_TYPE", "s3",
		"S3_BUCKET", "b", "S3_REGION", "us-east-1",
		"S3_ENDPOINT", "http://minio:9000", "S3_PREFIX", "/agent-dash/",
		"S3_FORCE_PATH_STYLE", "true",
		"SMTP_HOST", "smtp.example.com", "SMTP_TLS", "tls",
		"SMTP_USERNAME", "u", "SMTP_PASSWORD", "p",
		"SMTP_FROM", "agent-dash <dash@example.com>",
		"GOOGLE_CLIENT_ID", "id", "GOOGLE_CLIENT_SECRET", "secret",
		"OAUTH_DCR_ENABLED", "true",
		"INVITE_TTL", "48h",
	)))
	if err != nil {
		t.Fatal(err)
	}
	if c.SiteOrigin("abcdefghijkl") != "http://abcdefghijkl.sites.localhost:8080" {
		t.Errorf("SiteOrigin = %q", c.SiteOrigin("abcdefghijkl"))
	}
	if s := c.Storage.S3; s.Bucket != "b" || s.Prefix != "agent-dash" || !s.ForcePathStyle || s.Endpoint != "http://minio:9000" {
		t.Errorf("S3 = %+v", s)
	}
	if c.SMTP.Port != 465 || c.SMTP.From.Address != "dash@example.com" {
		t.Errorf("SMTP = %+v", c.SMTP)
	}
	if c.Google.ClientID != "id" || !c.OAuthDCREnabled || c.InviteTTL != 48*time.Hour {
		t.Errorf("Google = %+v, DCR = %v, TTL = %v", c.Google, c.OAuthDCREnabled, c.InviteTTL)
	}
}

func TestSMTPPortDefaults(t *testing.T) {
	for tls, port := range map[string]int{"": 587, "starttls": 587, "tls": 465, "none": 25} {
		c, err := Load(env(with(base(), "SMTP_HOST", "mx", "SMTP_FROM", "a@b.c", "SMTP_TLS", tls)))
		if err != nil {
			t.Fatal(err)
		}
		if c.SMTP.Port != port {
			t.Errorf("SMTP_TLS=%q: port %d, want %d", tls, c.SMTP.Port, port)
		}
	}
}

func TestInvalid(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
		want []string
	}{
		{"missing required", map[string]string{}, []string{"APP_ORIGIN: is required", "SITES_DOMAIN: is required", "SECRET_KEY: is required"}},
		{"short secret", with(base(), "SECRET_KEY", "short"), []string{"SECRET_KEY: must be at least 32 bytes, got 5"}},
		{"origin with path", with(base(), "APP_ORIGIN", "https://dash.example.com/app"), []string{"APP_ORIGIN: must be an origin"}},
		{"origin without scheme", with(base(), "APP_ORIGIN", "dash.example.com"), []string{"APP_ORIGIN: must start with https://"}},
		{"sites domain with scheme", with(base(), "SITES_DOMAIN", "https://sites.example.com"), []string{"SITES_DOMAIN: must be a domain name"}},
		{"app under sites domain", with(base(), "APP_ORIGIN", "https://app.sites.example.com"), []string{"APP_ORIGIN: host \"app.sites.example.com\" must not be SITES_DOMAIN"}},
		{"bad database url", with(base(), "DATABASE_URL", "postgres://u:hunter2@[::1/db"), []string{"DATABASE_URL: cannot be parsed"}},
		{"bad storage type", with(base(), "STORAGE_TYPE", "ftp"), []string{"STORAGE_TYPE: must be local, s3, or gcs"}},
		{"s3 missing bucket", with(base(), "STORAGE_TYPE", "s3"), []string{"S3_BUCKET: is required", "S3_REGION: is required"}},
		{"s3 bad endpoint", with(base(), "STORAGE_TYPE", "s3", "S3_BUCKET", "b", "S3_REGION", "r", "S3_ENDPOINT", "minio:9000"), []string{"S3_ENDPOINT: must be an http(s) URL"}},
		{"gcs missing bucket", with(base(), "STORAGE_TYPE", "gcs"), []string{"GCS_BUCKET: is required"}},
		{"smtp missing from", with(base(), "SMTP_HOST", "mx"), []string{"SMTP_FROM: is required"}},
		{"smtp bad tls", with(base(), "SMTP_HOST", "mx", "SMTP_FROM", "a@b.c", "SMTP_TLS", "ssl"), []string{"SMTP_TLS: must be starttls, tls, or none"}},
		{"smtp bad port", with(base(), "SMTP_HOST", "mx", "SMTP_FROM", "a@b.c", "SMTP_PORT", "99999"), []string{"SMTP_PORT: must be a port number"}},
		{"smtp half credentials", with(base(), "SMTP_HOST", "mx", "SMTP_FROM", "a@b.c", "SMTP_USERNAME", "u"), []string{"SMTP_USERNAME: and SMTP_PASSWORD must be set together"}},
		{"google half", with(base(), "GOOGLE_CLIENT_ID", "id"), []string{"GOOGLE_CLIENT_ID: and GOOGLE_CLIENT_SECRET must be set together"}},
		{"bad bool", with(base(), "OAUTH_DCR_ENABLED", "yes"), []string{"OAUTH_DCR_ENABLED: must be a boolean"}},
		{"bad duration", with(base(), "INVITE_TTL", "14d"), []string{"INVITE_TTL: must be a positive Go duration"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Load(env(tt.env))
			if err == nil {
				t.Fatal("Load succeeded")
			}
			for _, w := range tt.want {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("error %q does not contain %q", err, w)
				}
			}
			if strings.Contains(err.Error(), "hunter2") {
				t.Errorf("error leaks the database password: %q", err)
			}
		})
	}
}
