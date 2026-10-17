// Package safehttp is the only way to call user-supplied URLs (webhooks,
// image imports, merchant callbacks). It prevents SSRF by checking the IP
// address actually being dialled, after DNS resolution, on every
// connection, including those opened for redirects. That defeats DNS
// rebinding and redirect-to-internal tricks (CLAUDE.md security rules).
package safehttp

import (
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"slices"
	"strconv"
	"syscall"
	"time"
)

// Errors. Request errors are wrapped in *url.Error; match with errors.Is.
var (
	ErrBlockedAddress   = errors.New("safehttp: destination address is not allowed")
	ErrBlockedPort      = errors.New("safehttp: destination port is not allowed")
	ErrScheme           = errors.New("safehttp: only http and https URLs are allowed")
	ErrTooManyRedirects = errors.New("safehttp: too many redirects")
	ErrResponseTooLarge = errors.New("safehttp: response body too large")
)

// Options configures New. Zero fields take safe defaults.
type Options struct {
	Timeout          time.Duration // whole request including body read; default 10s
	MaxResponseBytes int64         // default 10 MiB
	MaxRedirects     int           // default 5; negative disables redirects
	AllowedPorts     []int         // default 80, 443, 8080, 8443
	// AllowPrefixes re-allows specific blocked ranges. Production code
	// should leave it empty; tests use it for httptest servers.
	AllowPrefixes []netip.Prefix
}

func (o *Options) defaults() {
	if o.Timeout <= 0 {
		o.Timeout = 10 * time.Second
	}
	if o.MaxResponseBytes <= 0 {
		o.MaxResponseBytes = 10 << 20
	}
	if o.MaxRedirects == 0 {
		o.MaxRedirects = 5
	}
	if len(o.AllowedPorts) == 0 {
		o.AllowedPorts = []int{80, 443, 8080, 8443}
	}
}

// New returns an http.Client hardened against SSRF.
func New(o Options) *http.Client {
	o.defaults()
	g := guard{blocked: blockedPrefixes(), allow: o.AllowPrefixes, ports: o.AllowedPorts}

	dialer := &net.Dialer{
		Timeout:   5 * time.Second,
		KeepAlive: 30 * time.Second,
		Control:   g.control,
	}
	transport := &http.Transport{
		Proxy:                 nil, // never use env proxies: they would bypass the IP check
		DialContext:           dialer.DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   4,
		IdleConnTimeout:       60 * time.Second,
		TLSHandshakeTimeout:   5 * time.Second,
		ResponseHeaderTimeout: o.Timeout,
		ExpectContinueTimeout: time.Second,
	}
	return &http.Client{
		Timeout:   o.Timeout,
		Transport: &limitTransport{next: transport, guard: g, max: o.MaxResponseBytes},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if o.MaxRedirects < 0 || len(via) > o.MaxRedirects {
				return ErrTooManyRedirects
			}
			return checkScheme(req.URL)
		},
	}
}

// ValidateURL checks a user-supplied URL before it is stored (e.g. a
// webhook endpoint), so obvious mistakes are rejected at the boundary.
// Dial-time checks in New remain the real enforcement because DNS can
// change after validation.
func ValidateURL(raw string, o Options) (*url.URL, error) {
	o.defaults()
	if len(raw) > 2048 {
		return nil, fmt.Errorf("%w: URL too long", ErrScheme)
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("safehttp: parse URL: %w", err)
	}
	if err := checkScheme(u); err != nil {
		return nil, err
	}
	if u.User != nil {
		return nil, fmt.Errorf("%w: credentials in URL", ErrScheme)
	}
	g := guard{blocked: blockedPrefixes(), allow: o.AllowPrefixes, ports: o.AllowedPorts}
	port := 80
	if u.Scheme == "https" {
		port = 443
	}
	if p := u.Port(); p != "" {
		n, err := strconv.Atoi(p)
		if err != nil {
			return nil, fmt.Errorf("%w: %q", ErrBlockedPort, p)
		}
		port = n
	}
	if err := g.checkPort(port); err != nil {
		return nil, err
	}
	if ip, err := netip.ParseAddr(u.Hostname()); err == nil {
		if err := g.checkAddr(ip); err != nil {
			return nil, err
		}
	}
	return u, nil
}

func checkScheme(u *url.URL) error {
	if (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		return fmt.Errorf("%w: %q", ErrScheme, u.Redacted())
	}
	return nil
}

type guard struct {
	blocked []netip.Prefix
	allow   []netip.Prefix
	ports   []int
}

// control runs after DNS resolution with the literal IP:port being dialled.
func (g guard) control(_, address string, _ syscall.RawConn) error {
	ap, err := netip.ParseAddrPort(address)
	if err != nil {
		return fmt.Errorf("%w: %q", ErrBlockedAddress, address)
	}
	if err := g.checkPort(int(ap.Port())); err != nil {
		return err
	}
	return g.checkAddr(ap.Addr())
}

func (g guard) checkPort(p int) error {
	if !slices.Contains(g.ports, p) {
		return fmt.Errorf("%w: %d", ErrBlockedPort, p)
	}
	return nil
}

func (g guard) checkAddr(ip netip.Addr) error {
	ip = ip.Unmap().WithZone("")
	for _, p := range g.allow {
		if p.Contains(ip) {
			return nil
		}
	}
	if !ip.IsGlobalUnicast() || ip.IsPrivate() {
		return fmt.Errorf("%w: %s", ErrBlockedAddress, ip)
	}
	for _, p := range g.blocked {
		if p.Contains(ip) {
			return fmt.Errorf("%w: %s", ErrBlockedAddress, ip)
		}
	}
	return nil
}

// blockedPrefixes lists special-purpose ranges beyond what netip's
// IsGlobalUnicast/IsPrivate already exclude (loopback, link-local incl.
// 169.254.169.254 metadata, multicast, unspecified, RFC 1918, ULA).
func blockedPrefixes() []netip.Prefix {
	return []netip.Prefix{
		netip.MustParsePrefix("0.0.0.0/8"),       // "this network"
		netip.MustParsePrefix("100.64.0.0/10"),   // CGNAT; also some cloud metadata
		netip.MustParsePrefix("192.0.0.0/24"),    // IETF protocol assignments
		netip.MustParsePrefix("192.0.2.0/24"),    // TEST-NET-1
		netip.MustParsePrefix("198.18.0.0/15"),   // benchmarking
		netip.MustParsePrefix("198.51.100.0/24"), // TEST-NET-2
		netip.MustParsePrefix("203.0.113.0/24"),  // TEST-NET-3
		netip.MustParsePrefix("240.0.0.0/4"),     // reserved, incl. broadcast
		netip.MustParsePrefix("::/96"),           // IPv4-compatible (deprecated)
		netip.MustParsePrefix("64:ff9b::/96"),    // NAT64: can embed private IPv4
		netip.MustParsePrefix("64:ff9b:1::/48"),  // local-use NAT64
		netip.MustParsePrefix("100::/64"),        // discard-only
		netip.MustParsePrefix("2001::/32"),       // Teredo: can embed private IPv4
		netip.MustParsePrefix("2001:db8::/32"),   // documentation
		netip.MustParsePrefix("2002::/16"),       // 6to4: can embed private IPv4
		netip.MustParsePrefix("fec0::/10"),       // site-local (deprecated)
		netip.MustParsePrefix("fd00:ec2::/32"),   // AWS IMDS over IPv6 (inside ULA; explicit for clarity)
	}
}

// limitTransport enforces the scheme on the first request and caps bodies.
type limitTransport struct {
	next  http.RoundTripper
	guard guard
	max   int64
}

func (t *limitTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if err := checkScheme(req.URL); err != nil {
		return nil, err
	}
	resp, err := t.next.RoundTrip(req)
	if err != nil {
		return nil, fmt.Errorf("safehttp: %w", err)
	}
	if resp.ContentLength > t.max {
		_ = resp.Body.Close()
		return nil, fmt.Errorf("%w: Content-Length %d > %d", ErrResponseTooLarge, resp.ContentLength, t.max)
	}
	resp.Body = &limitedBody{rc: resp.Body, remaining: t.max}
	return resp, nil
}

// limitedBody fails (instead of silently truncating) past the limit.
type limitedBody struct {
	rc        io.ReadCloser
	remaining int64
}

func (b *limitedBody) Read(p []byte) (int, error) {
	if b.remaining < 0 {
		return 0, ErrResponseTooLarge
	}
	if int64(len(p)) > b.remaining+1 {
		p = p[:b.remaining+1]
	}
	n, err := b.rc.Read(p)
	b.remaining -= int64(n)
	if b.remaining < 0 {
		return n + int(b.remaining), ErrResponseTooLarge
	}
	return n, err // io.EOF must stay unwrapped for readers
}

func (b *limitedBody) Close() error { return b.rc.Close() }
