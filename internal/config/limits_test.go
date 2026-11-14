package config

import (
	"net/netip"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestLimitsAndOperationsDefaults(t *testing.T) {
	c, err := Load(env(base()))
	if err != nil {
		t.Fatal(err)
	}
	if c.Limits != DefaultLimits() {
		t.Errorf("Limits = %+v", c.Limits)
	}
	want := Operations{RateLimit: true, MaintenanceInterval: 15 * time.Minute, BlobGCGrace: 24 * time.Hour, ShutdownTimeout: 25 * time.Second}
	if o := c.Operations; o.RateLimit != want.RateLimit || o.TrustedProxies != nil || o.MetricsAddr != "" ||
		o.MaintenanceInterval != want.MaintenanceInterval || o.BlobGCGrace != want.BlobGCGrace || o.ShutdownTimeout != want.ShutdownTimeout {
		t.Errorf("Operations = %+v", o)
	}
}

func TestLimitsAndOperationsSet(t *testing.T) {
	c, err := Load(env(with(base(),
		"MAX_FILE_SIZE", "8MiB",
		"MAX_SITE_SIZE", "2GiB",
		"MAX_SITE_FILES", "500",
		"MAX_ARCHIVE_SIZE", "1073741824",
		"MAX_ARCHIVE_ENTRIES", "20000",
		"MAX_MCP_BODY", "32 MiB",
		"MAX_JSON_BODY", "64KiB",
		"RATE_LIMIT_ENABLED", "false",
		"TRUSTED_PROXIES", " 10.0.0.0/8, 192.168.1.7 ,fd00::/8",
		"METRICS_ADDR", "127.0.0.1:9090",
		"MAINTENANCE_INTERVAL", "1h",
		"BLOB_GC_GRACE", "2h",
		"SHUTDOWN_TIMEOUT", "5s",
	)))
	if err != nil {
		t.Fatal(err)
	}
	want := Limits{8 << 20, 2 << 30, 500, 1 << 30, 20000, 32 << 20, 64 << 10}
	if c.Limits != want {
		t.Errorf("Limits = %+v, want %+v", c.Limits, want)
	}
	o := c.Operations
	proxies := []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8"), netip.MustParsePrefix("192.168.1.7/32"), netip.MustParsePrefix("fd00::/8")}
	if o.RateLimit || !slices.Equal(o.TrustedProxies, proxies) || o.MetricsAddr != "127.0.0.1:9090" ||
		o.MaintenanceInterval != time.Hour || o.BlobGCGrace != 2*time.Hour || o.ShutdownTimeout != 5*time.Second {
		t.Errorf("Operations = %+v", o)
	}
}

func TestLimitsAndOperationsInvalid(t *testing.T) {
	_, err := Load(env(with(base(),
		"MAX_FILE_SIZE", "64MB",
		"MAX_SITE_SIZE", "0",
		"MAX_SITE_FILES", "-1",
		"MAX_ARCHIVE_SIZE", "99999999999TiB",
		"MAX_ARCHIVE_ENTRIES", "many",
		"MAX_MCP_BODY", "1.5MiB",
		"MAX_JSON_BODY", "-5",
		"RATE_LIMIT_ENABLED", "sometimes",
		"TRUSTED_PROXIES", "10.0.0.0/8,proxy.internal",
		"METRICS_ADDR", "9090",
		"MAINTENANCE_INTERVAL", "0s",
		"BLOB_GC_GRACE", "10m",
		"SHUTDOWN_TIMEOUT", "soon",
	)))
	if err == nil {
		t.Fatal("Load succeeded")
	}
	for _, name := range []string{
		"MAX_FILE_SIZE", "MAX_SITE_SIZE", "MAX_SITE_FILES", "MAX_ARCHIVE_SIZE", "MAX_ARCHIVE_ENTRIES",
		"MAX_MCP_BODY", "MAX_JSON_BODY", "RATE_LIMIT_ENABLED", `"proxy.internal"`, "METRICS_ADDR",
		"MAINTENANCE_INTERVAL", "BLOB_GC_GRACE: must be at least 1h0m0s", "SHUTDOWN_TIMEOUT",
	} {
		if !strings.Contains(err.Error(), name) {
			t.Errorf("error does not mention %s:\n%v", name, err)
		}
	}
}

func TestLimitsCrossChecks(t *testing.T) {
	_, err := Load(env(with(base(), "MAX_FILE_SIZE", "2GiB", "MAX_SITE_SIZE", "1GiB", "METRICS_ADDR", ":8080")))
	if err == nil {
		t.Fatal("Load succeeded")
	}
	for _, want := range []string{"MAX_FILE_SIZE: must not exceed MAX_SITE_SIZE", "MAX_FILE_SIZE: must not exceed MAX_ARCHIVE_SIZE", "METRICS_ADDR: must differ from LISTEN_ADDR"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not mention %q:\n%v", want, err)
		}
	}
}

func TestParseBytes(t *testing.T) {
	for in, want := range map[string]int64{"1": 1, "10B": 10, "3KiB": 3 << 10, "64MiB": 64 << 20, "1GiB": 1 << 30, "2TiB": 2 << 40} {
		if got, ok := parseBytes(in); !ok || got != want {
			t.Errorf("parseBytes(%q) = %d, %v; want %d", in, got, ok, want)
		}
	}
	for _, in := range []string{"", "0", "0MiB", "1MB", "1.5GiB", "-1", "MiB", "1 GB", "9223372036854775807GiB"} {
		if got, ok := parseBytes(in); ok {
			t.Errorf("parseBytes(%q) = %d, want invalid", in, got)
		}
	}
}
