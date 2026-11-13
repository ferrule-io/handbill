package safehttp

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"
)

func newFakeResolver(t *testing.T, hostMap map[string][]netip.Addr) *net.Resolver {
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("ListenPacket failed: %v", err)
	}
	t.Cleanup(func() { pc.Close() })

	go func() {
		buf := make([]byte, 512)
		for {
			n, clientAddr, err := pc.ReadFrom(buf)
			if err != nil {
				return
			}
			req := buf[:n]
			if len(req) < 12 {
				continue
			}

			qEnd := 12
			var labels []string
			for qEnd < len(req) && req[qEnd] != 0 {
				lLen := int(req[qEnd])
				qEnd++
				if qEnd+lLen > len(req) {
					break
				}
				labels = append(labels, string(req[qEnd:qEnd+lLen]))
				qEnd += lLen
			}
			qEnd++ // skip null byte
			if qEnd+4 > len(req) {
				continue
			}
			qName := strings.Join(labels, ".")
			qType := (uint16(req[qEnd]) << 8) | uint16(req[qEnd+1])
			qEnd += 4 // skip QTYPE and QCLASS

			resp := make([]byte, 0, 512)
			resp = append(resp, req[0], req[1])  // ID
			resp = append(resp, 0x81, 0x80)      // Flags: standard response, no error
			resp = append(resp, 0x00, 0x01)      // QDCOUNT: 1
			resp = append(resp, 0x00, 0x00)      // ANCOUNT (updated below)
			resp = append(resp, 0x00, 0x00)      // NSCOUNT: 0
			resp = append(resp, 0x00, 0x00)      // ARCOUNT: 0
			resp = append(resp, req[12:qEnd]...) // Question section

			addrs := hostMap[qName]
			var anCount uint16
			for _, addr := range addrs {
				if qType == 1 && addr.Is4() { // A
					anCount++
					resp = append(resp, 0xc0, 0x0c)             // Name pointer
					resp = append(resp, 0x00, 0x01)             // TYPE: A
					resp = append(resp, 0x00, 0x01)             // CLASS: IN
					resp = append(resp, 0x00, 0x00, 0x00, 0x3c) // TTL: 60s
					resp = append(resp, 0x00, 0x04)             // RDLENGTH: 4
					v4 := addr.As4()
					resp = append(resp, v4[:]...)
				} else if qType == 28 && addr.Is6() { // AAAA
					anCount++
					resp = append(resp, 0xc0, 0x0c)             // Name pointer
					resp = append(resp, 0x00, 0x1c)             // TYPE: AAAA
					resp = append(resp, 0x00, 0x01)             // CLASS: IN
					resp = append(resp, 0x00, 0x00, 0x00, 0x3c) // TTL: 60s
					resp = append(resp, 0x00, 0x10)             // RDLENGTH: 16
					v6 := addr.As16()
					resp = append(resp, v6[:]...)
				}
			}
			resp[6] = byte(anCount >> 8)
			resp[7] = byte(anCount)

			pc.WriteTo(resp, clientAddr)
		}
	}()

	return &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "udp", pc.LocalAddr().String())
		},
	}
}

func TestPositivePathWithTLSServer(t *testing.T) {
	expectedBody := `{"client_id":"https://example.com/oauth"}`
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Test-Header") != "present" {
			http.Error(w, "missing header", http.StatusBadRequest)
			return
		}
		if r.Header.Get("User-Agent") != "custom-agent/1.0" {
			http.Error(w, "wrong user-agent", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(expectedBody))
	}))
	defer ts.Close()

	client := New(Options{
		AllowLoopback:   true,
		UserAgent:       "custom-agent/1.0",
		TLSClientConfig: ts.Client().Transport.(*http.Transport).TLSClientConfig,
	})

	hdr := make(http.Header)
	hdr.Set("X-Test-Header", "present")

	resp, err := client.Get(context.Background(), ts.URL, hdr)
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected status 200, got %d", resp.StatusCode)
	}
	if string(resp.Body) != expectedBody {
		t.Fatalf("expected body %q, got %q", expectedBody, string(resp.Body))
	}
	if resp.Header.Get("Content-Type") != "application/json" {
		t.Fatalf("expected Content-Type application/json, got %q", resp.Header.Get("Content-Type"))
	}
}

func TestNotModifiedReturned(t *testing.T) {
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("If-None-Match") != `"v1"` {
			http.Error(w, "missing If-None-Match", http.StatusBadRequest)
			return
		}
		w.Header().Set("ETag", `"v1"`)
		w.WriteHeader(http.StatusNotModified)
	}))
	defer ts.Close()

	client := New(Options{AllowLoopback: true, TLSClientConfig: ts.Client().Transport.(*http.Transport).TLSClientConfig})
	hdr := make(http.Header)
	hdr.Set("If-None-Match", `"v1"`)
	resp, err := client.Get(context.Background(), ts.URL, hdr)
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if resp.StatusCode != http.StatusNotModified {
		t.Fatalf("expected status 304, got %d", resp.StatusCode)
	}
	if len(resp.Body) != 0 {
		t.Fatalf("expected empty body, got %q", resp.Body)
	}
	if resp.Header.Get("ETag") != `"v1"` {
		t.Fatalf("expected ETag, got %q", resp.Header.Get("ETag"))
	}
}

func TestLoopbackBlockedByDefault(t *testing.T) {
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	// Default client: AllowLoopback is false
	client := New(Options{
		TLSClientConfig: ts.Client().Transport.(*http.Transport).TLSClientConfig,
	})

	_, err := client.Get(context.Background(), ts.URL, nil)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, ErrBlockedAddress) {
		t.Fatalf("expected ErrBlockedAddress, got %v", err)
	}
}

func TestDialTimeHostnameBlockingWithFakeResolver(t *testing.T) {
	hostMap := map[string][]netip.Addr{
		"loopback.ssrf.test":       {netip.MustParseAddr("127.0.0.1")},
		"metadata.ssrf.test":       {netip.MustParseAddr("169.254.169.254")},
		"mapped-private.ssrf.test": {netip.MustParseAddr("::ffff:10.0.0.1")},
		"dual-homed.ssrf.test":     {netip.MustParseAddr("8.8.8.8"), netip.MustParseAddr("127.0.0.1")},
	}
	resolver := newFakeResolver(t, hostMap)

	client := New(Options{
		Resolver: resolver,
	})

	targets := []struct {
		name string
		url  string
	}{
		{"127.0.0.1 resolution", "https://loopback.ssrf.test"},
		{"169.254.169.254 resolution", "https://metadata.ssrf.test"},
		{"::ffff:10.0.0.1 resolution", "https://mapped-private.ssrf.test"},
		{"dual-homed with loopback", "https://dual-homed.ssrf.test"},
	}

	for _, tt := range targets {
		t.Run(tt.name, func(t *testing.T) {
			_, err := client.Get(context.Background(), tt.url, nil)
			if err == nil {
				t.Fatalf("expected ErrBlockedAddress, got nil for %s", tt.url)
			}
			if !errors.Is(err, ErrBlockedAddress) {
				t.Fatalf("expected ErrBlockedAddress, got %v", err)
			}
			if err != ErrBlockedAddress {
				t.Fatalf("expected direct ErrBlockedAddress equality, got %v", err)
			}
		})
	}
}

func TestRedirectRefusal(t *testing.T) {
	redirectStatuses := []int{
		http.StatusMovedPermanently,  // 301
		http.StatusFound,             // 302
		http.StatusSeeOther,          // 303
		http.StatusTemporaryRedirect, // 307
		http.StatusPermanentRedirect, // 308
		http.StatusNotModified,       // 304
	}

	for _, status := range redirectStatuses {
		t.Run(http.StatusText(status), func(t *testing.T) {
			ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if status == http.StatusNotModified {
					w.WriteHeader(status)
					return
				}
				http.Redirect(w, r, "https://example.com/target", status)
			}))
			defer ts.Close()

			client := New(Options{
				AllowLoopback:   true,
				TLSClientConfig: ts.Client().Transport.(*http.Transport).TLSClientConfig,
			})

			_, err := client.Get(context.Background(), ts.URL, nil)
			if err == nil {
				t.Fatalf("expected ErrRedirect, got nil")
			}
			if !errors.Is(err, ErrRedirect) {
				t.Fatalf("expected ErrRedirect, got %v", err)
			}
			if err != ErrRedirect {
				t.Fatalf("expected direct ErrRedirect equality, got %v", err)
			}
		})
	}
}

func TestSchemeAndUserinfoRefusal(t *testing.T) {
	client := New(Options{})

	invalidURLs := []struct {
		name string
		url  string
	}{
		{"http scheme", "http://example.com"},
		{"ftp scheme", "ftp://example.com"},
		{"file scheme", "file:///etc/passwd"},
		{"gopher scheme", "gopher://example.com"},
		{"empty scheme", "//example.com"},
		{"relative url", "/path/to/something"},
		{"user in url", "https://user@example.com"},
		{"user and pass in url", "https://user:pass@example.com"},
		{"pass only in url", "https://:pass@example.com"},
		{"empty userinfo in url", "https://@example.com"},
		{"encoded userinfo in url", "https://user%40example.com@host.com"},
		{"no host", "https://"},
		{"path only no host", "https:///path"},
		{"port 0", "https://example.com:0"},
		{"port 65536", "https://example.com:65536"},
		{"port negative", "https://example.com:-1"},
		{"port not a number", "https://example.com:abc"},
		{"trailing colon without port", "https://example.com:"},
		{"ipv6 trailing colon without port", "https://[::1]:"},
	}

	for _, tt := range invalidURLs {
		t.Run(tt.name, func(t *testing.T) {
			_, err := client.Get(context.Background(), tt.url, nil)
			if err == nil {
				t.Fatalf("expected ErrScheme, got nil for %s", tt.url)
			}
			if !errors.Is(err, ErrScheme) {
				t.Fatalf("expected ErrScheme for %q, got %v", tt.url, err)
			}
			if err != ErrScheme {
				t.Fatalf("expected direct ErrScheme equality for %q, got %v", tt.url, err)
			}
		})
	}
}

func TestSizeLimit(t *testing.T) {
	t.Run("uncompressed under limit", func(t *testing.T) {
		payload := bytes.Repeat([]byte("A"), 1024)
		ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(payload)
		}))
		defer ts.Close()

		client := New(Options{
			AllowLoopback:   true,
			MaxBodyBytes:    1024,
			TLSClientConfig: ts.Client().Transport.(*http.Transport).TLSClientConfig,
		})

		resp, err := client.Get(context.Background(), ts.URL, nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(resp.Body) != 1024 {
			t.Fatalf("expected 1024 bytes, got %d", len(resp.Body))
		}
	})

	t.Run("uncompressed over limit by one byte", func(t *testing.T) {
		payload := bytes.Repeat([]byte("A"), 1025)
		ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(payload)
		}))
		defer ts.Close()

		client := New(Options{
			AllowLoopback:   true,
			MaxBodyBytes:    1024,
			TLSClientConfig: ts.Client().Transport.(*http.Transport).TLSClientConfig,
		})

		_, err := client.Get(context.Background(), ts.URL, nil)
		if err == nil {
			t.Fatal("expected ErrTooLarge, got nil")
		}
		if !errors.Is(err, ErrTooLarge) {
			t.Fatalf("expected ErrTooLarge, got %v", err)
		}
		if err != ErrTooLarge {
			t.Fatalf("expected direct ErrTooLarge equality, got %v", err)
		}
	})

	t.Run("default 64 KiB limit", func(t *testing.T) {
		const maxDefault = 64 * 1024
		payload := bytes.Repeat([]byte("B"), maxDefault+1)
		ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(payload)
		}))
		defer ts.Close()

		client := New(Options{
			AllowLoopback:   true,
			TLSClientConfig: ts.Client().Transport.(*http.Transport).TLSClientConfig,
		})

		_, err := client.Get(context.Background(), ts.URL, nil)
		if err == nil {
			t.Fatal("expected ErrTooLarge, got nil")
		}
		if !errors.Is(err, ErrTooLarge) {
			t.Fatalf("expected ErrTooLarge, got %v", err)
		}
	})

	t.Run("gzip bomb with Content-Encoding gzip", func(t *testing.T) {
		// Create a compressed payload that decompresses into 1 MB of zeros
		// Compressed size will be around 1 KB.
		var compressed bytes.Buffer
		gw := gzip.NewWriter(&compressed)
		chunk := make([]byte, 64*1024)
		for range 16 { // 16 * 64 KiB = 1 MiB
			_, _ = gw.Write(chunk)
		}
		_ = gw.Close()

		ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Encoding", "gzip")
			w.Header().Set("Content-Type", "application/octet-stream")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(compressed.Bytes())
		}))
		defer ts.Close()

		client := New(Options{
			AllowLoopback:   true,
			MaxBodyBytes:    64 * 1024, // 64 KiB limit
			TLSClientConfig: ts.Client().Transport.(*http.Transport).TLSClientConfig,
		})

		_, err := client.Get(context.Background(), ts.URL, nil)
		if err == nil {
			t.Fatal("expected ErrTooLarge for gzip bomb, got nil")
		}
		if !errors.Is(err, ErrTooLarge) {
			t.Fatalf("expected ErrTooLarge, got %v", err)
		}
		if err != ErrTooLarge {
			t.Fatalf("expected direct ErrTooLarge equality, got %v", err)
		}
	})

	t.Run("valid gzip under limit is decompressed", func(t *testing.T) {
		original := []byte("hello, decompressed gzip world!")
		var compressed bytes.Buffer
		gw := gzip.NewWriter(&compressed)
		_, _ = gw.Write(original)
		_ = gw.Close()

		ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Encoding", "gzip")
			w.Header().Set("Content-Type", "text/plain")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(compressed.Bytes())
		}))
		defer ts.Close()

		client := New(Options{
			AllowLoopback:   true,
			TLSClientConfig: ts.Client().Transport.(*http.Transport).TLSClientConfig,
		})

		resp, err := client.Get(context.Background(), ts.URL, nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !bytes.Equal(resp.Body, original) {
			t.Fatalf("expected %q, got %q", string(original), string(resp.Body))
		}
	})
}

func TestTimeout(t *testing.T) {
	t.Run("slow headers timeout", func(t *testing.T) {
		ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			time.Sleep(300 * time.Millisecond)
			w.WriteHeader(http.StatusOK)
		}))
		defer ts.Close()

		client := New(Options{
			AllowLoopback:   true,
			Timeout:         50 * time.Millisecond,
			TLSClientConfig: ts.Client().Transport.(*http.Transport).TLSClientConfig,
		})

		_, err := client.Get(context.Background(), ts.URL, nil)
		if err == nil {
			t.Fatal("expected timeout error, got nil")
		}
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("expected context.DeadlineExceeded, got %v", err)
		}
	})

	t.Run("slow body read timeout", func(t *testing.T) {
		ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			time.Sleep(300 * time.Millisecond)
			_, _ = w.Write([]byte("delayed body chunk"))
		}))
		defer ts.Close()

		client := New(Options{
			AllowLoopback:   true,
			Timeout:         50 * time.Millisecond,
			TLSClientConfig: ts.Client().Transport.(*http.Transport).TLSClientConfig,
		})

		_, err := client.Get(context.Background(), ts.URL, nil)
		if err == nil {
			t.Fatal("expected timeout error, got nil")
		}
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("expected context.DeadlineExceeded, got %v", err)
		}
	})
}

func TestExplicitPortsAllowed(t *testing.T) {
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	defer ts.Close()

	client := New(Options{
		AllowLoopback:   true,
		TLSClientConfig: ts.Client().Transport.(*http.Transport).TLSClientConfig,
	})

	// ts.URL already contains an explicit port in 1-65535
	resp, err := client.Get(context.Background(), ts.URL, nil)
	if err != nil {
		t.Fatalf("unexpected error for explicit valid port: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
}

func TestNoProxyUsed(t *testing.T) {
	// Verify that Proxy on Transport is nil (ignores HTTP(S)_PROXY env)
	client := New(Options{})
	tr, ok := client.httpClient.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("expected *http.Transport, got %T", client.httpClient.Transport)
	}
	if tr.Proxy != nil {
		t.Fatal("expected nil Proxy on Transport, but it was non-nil")
	}
}

func TestUserAgentOverride(t *testing.T) {
	var receivedUA string
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedUA = r.Header.Get("User-Agent")
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	client := New(Options{
		AllowLoopback:   true,
		UserAgent:       "default-agent/1.0",
		TLSClientConfig: ts.Client().Transport.(*http.Transport).TLSClientConfig,
	})

	hdr := make(http.Header)
	hdr.Set("User-Agent", "caller-override/2.0")

	_, err := client.Get(context.Background(), ts.URL, hdr)
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if receivedUA != "caller-override/2.0" {
		t.Fatalf("expected caller-override/2.0, got %q", receivedUA)
	}
}
