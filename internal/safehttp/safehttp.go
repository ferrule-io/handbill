package safehttp

import (
	"compress/gzip"
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"syscall"
	"time"
)

var (
	ErrBlockedAddress = errors.New("safehttp: blocked address")
	ErrScheme         = errors.New("safehttp: scheme must be https")
	ErrTooLarge       = errors.New("safehttp: response body exceeds limit")
	ErrRedirect       = errors.New("safehttp: redirect not allowed")
)

type Options struct {
	Timeout      time.Duration // whole request incl. body read; default 5s
	MaxBodyBytes int64         // default 64 KiB
	UserAgent    string
	// AllowLoopback / AllowPrivate exist ONLY for tests; default false.
	AllowLoopback bool
	AllowPrivate  bool
	Resolver      *net.Resolver // optional, for tests
	// TLSClientConfig is optional, for tests (e.g. httptest TLS server root CAs).
	TLSClientConfig *tls.Config
}

type Response struct {
	StatusCode int
	Header     http.Header
	Body       []byte
}

type Client struct {
	opts       Options
	httpClient *http.Client
}

func New(o Options) *Client {
	c := &Client{
		opts: o,
	}

	dialTimeout := 5 * time.Second
	if o.Timeout > 0 && o.Timeout < dialTimeout {
		dialTimeout = o.Timeout
	}

	var tlsConfig *tls.Config
	if o.TLSClientConfig != nil {
		tlsConfig = o.TLSClientConfig.Clone()
	} else {
		tlsConfig = &tls.Config{}
	}

	transport := &http.Transport{
		Proxy:                 nil, // No proxy (ignore HTTP(S)_PROXY env)
		DialContext:           c.dialContext,
		TLSClientConfig:       tlsConfig,
		ForceAttemptHTTP2:     true,
		DisableKeepAlives:     true, // No keep-alive reuse across hosts needed
		DisableCompression:    false,
		TLSHandshakeTimeout:   dialTimeout,
		ResponseHeaderTimeout: dialTimeout,
		ExpectContinueTimeout: 1 * time.Second,
	}

	c.httpClient = &http.Client{
		Transport: transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return ErrRedirect
		},
	}

	return c
}

func (c *Client) dialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}

	var ips []netip.Addr

	if parsedIP, err := netip.ParseAddr(host); err == nil {
		if isBlocked(parsedIP, c.opts) {
			return nil, ErrBlockedAddress
		}
		ips = []netip.Addr{parsedIP}
	} else {
		resolver := c.opts.Resolver
		if resolver == nil {
			resolver = net.DefaultResolver
		}

		resolved, err := resolver.LookupNetIP(ctx, "ip", host)
		if err != nil {
			return nil, err
		}
		if len(resolved) == 0 {
			return nil, &net.DNSError{Err: "no such host", Name: host}
		}

		for _, ip := range resolved {
			if isBlocked(ip, c.opts) {
				return nil, ErrBlockedAddress
			}
		}
		ips = resolved
	}

	dialer := &net.Dialer{
		Control: func(network, address string, rawConn syscall.RawConn) error {
			h, _, err := net.SplitHostPort(address)
			if err != nil {
				return err
			}
			pip, err := netip.ParseAddr(h)
			if err != nil {
				return err
			}
			if isBlocked(pip, c.opts) {
				return ErrBlockedAddress
			}
			return nil
		},
	}

	var lastErr error
	for _, ip := range ips {
		target := net.JoinHostPort(ip.String(), port)
		conn, err := dialer.DialContext(ctx, network, target)
		if err == nil {
			return conn, nil
		}
		if errors.Is(err, ErrBlockedAddress) {
			return nil, ErrBlockedAddress
		}
		lastErr = err
	}

	if lastErr != nil {
		return nil, lastErr
	}
	return nil, ErrBlockedAddress
}

func validateURL(rawURL string) (*url.URL, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, ErrScheme
	}

	if strings.ToLower(u.Scheme) != "https" {
		return nil, ErrScheme
	}

	if u.User != nil || strings.Contains(u.Host, "@") {
		return nil, ErrScheme
	}

	host := u.Host
	if host == "" {
		return nil, ErrScheme
	}

	hostname := u.Hostname()
	if hostname == "" {
		return nil, ErrScheme
	}

	portStr := u.Port()
	if portStr != "" {
		port, err := strconv.Atoi(portStr)
		if err != nil || port < 1 || port > 65535 {
			return nil, ErrScheme
		}
	} else {
		if strings.HasSuffix(host, ":") {
			return nil, ErrScheme
		}
	}

	return u, nil
}

func (c *Client) Get(ctx context.Context, rawURL string, header http.Header) (*Response, error) {
	u, err := validateURL(rawURL)
	if err != nil {
		return nil, err
	}

	if ctx == nil {
		ctx = context.Background()
	}

	timeout := c.opts.Timeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	reqCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}

	if c.opts.UserAgent != "" {
		req.Header.Set("User-Agent", c.opts.UserAgent)
	}
	for k, vv := range header {
		req.Header[k] = append([]string(nil), vv...)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		if errors.Is(err, ErrBlockedAddress) {
			return nil, ErrBlockedAddress
		}
		if errors.Is(err, ErrRedirect) {
			return nil, ErrRedirect
		}
		return nil, err
	}

	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		resp.Body.Close()
		return nil, ErrRedirect
	}

	maxBytes := c.opts.MaxBodyBytes
	if maxBytes <= 0 {
		maxBytes = 64 * 1024 // 64 KiB
	}

	var bodyReader io.Reader = resp.Body
	var gzReader *gzip.Reader
	if strings.EqualFold(resp.Header.Get("Content-Encoding"), "gzip") {
		var gzErr error
		gzReader, gzErr = gzip.NewReader(resp.Body)
		if gzErr != nil {
			resp.Body.Close()
			return nil, gzErr
		}
		defer gzReader.Close()
		bodyReader = gzReader
	}

	lr := io.LimitReader(bodyReader, maxBytes+1)
	body, readErr := io.ReadAll(lr)
	resp.Body.Close()
	if readErr != nil {
		return nil, readErr
	}

	if int64(len(body)) > maxBytes {
		return nil, ErrTooLarge
	}

	respHeader := resp.Header.Clone()
	if gzReader != nil {
		respHeader.Del("Content-Encoding")
		respHeader.Del("Content-Length")
	}

	return &Response{
		StatusCode: resp.StatusCode,
		Header:     respHeader,
		Body:       body,
	}, nil
}
