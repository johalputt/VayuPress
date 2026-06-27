// Package safefetch is the single, SSRF-hardened HTTP fetcher used for every
// server-side outbound request that carries an operator- or author-influenced
// URL (embed resolution, remote-image import, oEmbed/OpenGraph metadata).
//
// It consolidates the SSRF guard previously expressed only as a transport in
// cmd/vayupress/middleware.go (ADR-0009) and adds the protections rich-media
// fetching needs (ADR-0070): a scheme allowlist, a hard response-size cap, a
// total-time budget, and per-redirect-hop re-validation so a public URL cannot
// redirect into the private network.
//
// The guard works by resolving the host and dialing a *validated* IP directly,
// which closes the DNS-rebinding / TOCTOU window between "resolve" and
// "connect" that a re-resolving dialer leaves open.
package safefetch

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

// Default limits. Callers may override via Options.
const (
	defaultMaxBytes = 16 << 20 // 16 MiB — generous for images, tiny for JSON.
	defaultTimeout  = 10 * time.Second
	maxRedirects    = 5
)

// ErrTooLarge is returned when a response body exceeds the configured cap.
var ErrTooLarge = errors.New("safefetch: response exceeds maximum size")

// ErrBlockedAddress is returned when a host resolves only to private/reserved
// addresses, or a redirect targets a disallowed scheme.
var ErrBlockedAddress = errors.New("safefetch: refusing to connect to blocked address")

// Options configures a Client. The zero value is usable via New(Options{}).
type Options struct {
	MaxBytes       int64         // hard cap on bytes read from the body (default 16 MiB)
	Timeout        time.Duration // total time budget for the request (default 10s)
	AllowedSchemes []string      // permitted URL schemes (default {"https","http"})
	UserAgent      string        // User-Agent header (default a VayuPress identifier)
}

// Client performs SSRF-safe HTTP GETs. It is safe for concurrent use.
type Client struct {
	httpc    *http.Client
	maxBytes int64
	schemes  map[string]bool
	ua       string
	// hostGuard is the pre-flight SSRF barrier applied to every request host.
	// It defaults to validatePublicHost. Tests that intentionally target a
	// loopback httptest server (to exercise body/size handling) set it to nil
	// alongside swapping httpc.Transport, the same way the dial-time guard is
	// bypassed. Production callers never touch it.
	hostGuard func(context.Context, string) error
}

// Result is a fetched response with its body already read (and size-capped).
type Result struct {
	Body        []byte
	ContentType string // raw Content-Type header value
	FinalURL    string // URL after any redirects
	Status      int
}

// New builds a Client from Options, applying defaults for any zero field.
func New(opts Options) *Client {
	if opts.MaxBytes <= 0 {
		opts.MaxBytes = defaultMaxBytes
	}
	if opts.Timeout <= 0 {
		opts.Timeout = defaultTimeout
	}
	if len(opts.AllowedSchemes) == 0 {
		opts.AllowedSchemes = []string{"https", "http"}
	}
	if opts.UserAgent == "" {
		opts.UserAgent = "VayuPress/safefetch (+https://vayupress.com)"
	}
	schemes := make(map[string]bool, len(opts.AllowedSchemes))
	for _, s := range opts.AllowedSchemes {
		schemes[strings.ToLower(s)] = true
	}

	c := &Client{maxBytes: opts.MaxBytes, schemes: schemes, ua: opts.UserAgent, hostGuard: validatePublicHost}
	c.httpc = &http.Client{
		Timeout:   opts.Timeout,
		Transport: c.transport(),
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= maxRedirects {
				return fmt.Errorf("safefetch: stopped after %d redirects", maxRedirects)
			}
			if !c.schemes[strings.ToLower(req.URL.Scheme)] {
				return fmt.Errorf("%w: redirect to disallowed scheme %q", ErrBlockedAddress, req.URL.Scheme)
			}
			return nil
		},
	}
	return c
}

// transport returns the SSRF-safe transport used by the GET Client. It is the
// strictest configuration (no allow-listed internal hosts): the pre-flight
// validatePublicHost guard already rejects loopback/private hosts before any
// connection, and the dialer re-enforces that against the pinned IP on every
// redirect hop.
func (c *Client) transport() *http.Transport {
	return SafeTransport(TransportOptions{})
}

// TransportOptions configures SafeTransport.
type TransportOptions struct {
	// AllowHosts lists hostnames / IP literals that may resolve to a private or
	// reserved address. Use it only for trusted, operator-configured internal
	// services — e.g. a loopback Meilisearch or Ollama endpoint. Matching is
	// exact and case-insensitive on the request host. Leave empty for the
	// strictest behaviour (refuse every private/reserved destination).
	AllowHosts []string
	// DialTimeout overrides the per-connection dial timeout (default 5s).
	DialTimeout time.Duration
}

// SafeTransport returns an *http.Transport suitable for an arbitrary-method
// http.Client (POST webhooks, update downloads, internal service calls) that
// still enforces the SSRF guard: it resolves the host, refuses
// private/reserved destinations (except AllowHosts), pins the validated IP at
// dial time to close the DNS-rebinding / TOCTOU window, and never honours an
// environment proxy.
//
// This is the single source of truth for the server-side outbound dialer — it
// replaces the weaker, re-resolving transport that previously lived in
// cmd/vayupress/middleware.go (ADR-0009).
func SafeTransport(opts TransportOptions) *http.Transport {
	allow := make(map[string]bool, len(opts.AllowHosts))
	for _, h := range opts.AllowHosts {
		allow[strings.ToLower(strings.TrimSpace(h))] = true
	}
	dt := opts.DialTimeout
	if dt <= 0 {
		dt = 5 * time.Second
	}
	base := &net.Dialer{Timeout: dt, KeepAlive: 30 * time.Second}
	dial := func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, err
		}
		// Trusted internal service (e.g. localhost Meilisearch/Ollama): dial as
		// given, without the public-IP requirement.
		if allow[strings.ToLower(host)] {
			return base.DialContext(ctx, network, addr)
		}
		ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
		if err != nil {
			return nil, err
		}
		// Dial the first public IP directly so the connection cannot land on a
		// different (private) address than the one we validated.
		for _, ipa := range ips {
			if isPrivateOrReservedIP(ipa.IP) {
				continue
			}
			return base.DialContext(ctx, network, net.JoinHostPort(ipa.IP.String(), port))
		}
		return nil, fmt.Errorf("%w: host %q has no public address", ErrBlockedAddress, host)
	}
	return &http.Transport{
		Proxy:                 nil, // never honour a proxy for guarded fetches
		DialContext:           dial,
		DialTLSContext:        nil,
		TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12},
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   5 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
}

// IsPrivateOrReservedIP reports whether ip must never be the target of a
// server-side outbound connection (loopback, link-local, multicast,
// unspecified, RFC1918/ULA private, cloud metadata endpoints, CGNAT, Class-E,
// and other reserved ranges). Exported so callers share one definition rather
// than maintaining drift-prone copies. A nil IP is treated as blocked.
func IsPrivateOrReservedIP(ip net.IP) bool { return isPrivateOrReservedIP(ip) }

// Get fetches rawURL with the SSRF guard, scheme allowlist, size cap, and time
// budget applied. The returned Result.Body is at most MaxBytes long.
func (c *Client) Get(ctx context.Context, rawURL string) (*Result, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	if !c.schemes[strings.ToLower(req.URL.Scheme)] {
		return nil, fmt.Errorf("%w: scheme %q not allowed", ErrBlockedAddress, req.URL.Scheme)
	}
	// Pre-flight SSRF barrier: resolve the request host and reject it unless it
	// has at least one public address (and no IP-literal host that is itself
	// private/reserved). This fails fast — before any connection is opened — and
	// is the validation the dial-time guard then re-enforces against the *pinned*
	// IP on every redirect hop (closing the DNS-rebind window). Validating here
	// makes the host an allow-checked value rather than raw, untrusted input.
	if c.hostGuard != nil {
		if err := c.hostGuard(req.Context(), req.URL.Hostname()); err != nil {
			return nil, err
		}
	}
	req.Header.Set("User-Agent", c.ua)
	req.Header.Set("Accept-Encoding", "identity") // size cap must apply to real bytes

	resp, err := c.httpc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	// Read one byte past the cap so we can distinguish "exactly at cap" from
	// "over cap" and reject the latter rather than silently truncating.
	limited := io.LimitReader(resp.Body, c.maxBytes+1)
	body, err := io.ReadAll(limited)
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > c.maxBytes {
		return nil, ErrTooLarge
	}
	return &Result{
		Body:        body,
		ContentType: resp.Header.Get("Content-Type"),
		FinalURL:    resp.Request.URL.String(),
		Status:      resp.StatusCode,
	}, nil
}

// validatePublicHost resolves host and returns nil only when it maps to at
// least one public, non-reserved address. An empty host, an IP-literal host
// that is private/reserved, a resolution failure, or a host that resolves to
// only private/reserved addresses all return ErrBlockedAddress. This is a
// fail-fast pre-flight guard; the authoritative, rebind-safe check still runs
// in the transport dialer against the pinned IP.
func validatePublicHost(ctx context.Context, host string) error {
	if host == "" {
		return fmt.Errorf("%w: empty host", ErrBlockedAddress)
	}
	// IP-literal host: validate directly without a DNS lookup.
	if ip := net.ParseIP(host); ip != nil {
		if isPrivateOrReservedIP(ip) {
			return fmt.Errorf("%w: host %q is private/reserved", ErrBlockedAddress, host)
		}
		return nil
	}
	ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return fmt.Errorf("%w: cannot resolve host %q: %v", ErrBlockedAddress, host, err)
	}
	for _, ipa := range ips {
		if !isPrivateOrReservedIP(ipa.IP) {
			return nil // at least one public address — allowed
		}
	}
	return fmt.Errorf("%w: host %q has no public address", ErrBlockedAddress, host)
}

// reservedV4 holds IPv4 ranges that net.IP.IsPrivate does not flag but which a
// guarded fetch must still refuse.
var reservedV4 = func() []*net.IPNet {
	var nets []*net.IPNet
	for _, s := range []string{
		"100.64.0.0/10", // carrier-grade NAT (RFC 6598)
		"192.0.0.0/24",  // IETF protocol assignments (RFC 6890)
		"198.18.0.0/15", // benchmarking (RFC 2544)
		"240.0.0.0/4",   // reserved / Class E (RFC 1112)
	} {
		if _, n, err := net.ParseCIDR(s); err == nil {
			nets = append(nets, n)
		}
	}
	return nets
}()

// isPrivateOrReservedIP reports whether ip is one we must never connect to from
// a guarded fetch: loopback, link-local, multicast, unspecified, RFC1918/ULA
// private, or a known cloud metadata endpoint. A nil IP is treated as blocked.
func isPrivateOrReservedIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	if ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsMulticast() || ip.IsUnspecified() || ip.IsPrivate() {
		return true
	}
	// Cloud metadata services (AWS/GCP/Azure IMDS, Alibaba).
	if ip.Equal(net.ParseIP("169.254.169.254")) || ip.Equal(net.ParseIP("100.100.100.200")) {
		return true
	}
	// Ranges ip.IsPrivate() does not cover but which must never be reachable:
	// carrier-grade NAT (100.64.0.0/10), IETF protocol assignments
	// (192.0.0.0/24), benchmarking (198.18.0.0/15), and reserved/Class-E
	// (240.0.0.0/4). These can front internal infrastructure.
	if v4 := ip.To4(); v4 != nil {
		for _, cidr := range reservedV4 {
			if cidr.Contains(v4) {
				return true
			}
		}
	}
	// IPv6 unique-local addresses (fc00::/7).
	if v6 := ip.To16(); v6 != nil && ip.To4() == nil && (v6[0]&0xfe) == 0xfc {
		return true
	}
	return false
}
