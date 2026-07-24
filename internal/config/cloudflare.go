package config

import (
	"net"
	"strings"
	"sync"
	"sync/atomic"
)

// cloudflare.go — "my site is behind Cloudflare (or another CDN)" support.
//
// When a site is proxied through Cloudflare, every request reaches the origin
// from one of Cloudflare's edge IPs, so the origin's immediate peer is NEVER the
// real visitor. Without this, VayuShield (and the auth lockout, rate limiter,
// reputation engine — everything keyed on the client IP) sees the whole audience
// as a handful of Cloudflare IPs: that pool instantly exceeds the per-IP rate
// limit, auto-block jails the Cloudflare IP, and every real visitor is thrown the
// "Just a moment" throttle page in a loop. Enabling Cloudflare trust makes the
// origin read the real visitor IP from Cloudflare's CF-Connecting-IP header —
// but ONLY when the peer is genuinely a Cloudflare edge IP, so a direct attacker
// cannot spoof the header to evade rate limiting.

// cloudflareCIDRs are Cloudflare's published edge ranges
// (https://www.cloudflare.com/ips/). Embedded so trust works offline and needs
// no fetch; refreshed with releases. A stale entry only means a brand-new
// Cloudflare range is briefly not auto-trusted — add it to TRUSTED_PROXIES if so.
var cloudflareCIDRs = []string{
	// IPv4
	"173.245.48.0/20", "103.21.244.0/22", "103.22.200.0/22", "103.31.4.0/22",
	"141.101.64.0/18", "108.162.192.0/18", "190.93.240.0/20", "188.114.96.0/20",
	"197.234.240.0/22", "198.41.128.0/17", "162.158.0.0/15", "104.16.0.0/13",
	"104.24.0.0/14", "172.64.0.0/13", "131.0.72.0/22",
	// IPv6
	"2400:cb00::/32", "2606:4700::/32", "2803:f800::/32", "2405:b500::/32",
	"2405:8100::/32", "2a06:98c0::/29", "2c0f:f248::/32",
}

var (
	cloudflareNetsOnce sync.Once
	cloudflareNets     []*net.IPNet
	trustCloudflare    atomic.Bool
)

func cloudflareNetworks() []*net.IPNet {
	cloudflareNetsOnce.Do(func() {
		for _, c := range cloudflareCIDRs {
			if _, n, err := net.ParseCIDR(strings.TrimSpace(c)); err == nil {
				cloudflareNets = append(cloudflareNets, n)
			}
		}
	})
	return cloudflareNets
}

// SetTrustCloudflare turns Cloudflare-edge trust on or off at runtime (from the
// TRUST_CLOUDFLARE env at boot, or the "behind Cloudflare/CDN" panel toggle,
// applied live with no restart).
func SetTrustCloudflare(on bool) { trustCloudflare.Store(on) }

// TrustCloudflareEnabled reports whether Cloudflare-edge trust is on.
func TrustCloudflareEnabled() bool { return trustCloudflare.Load() }

// IsCloudflareIP reports whether ip is within Cloudflare's published edge ranges.
func IsCloudflareIP(ip net.IP) bool {
	if ip == nil {
		return false
	}
	for _, n := range cloudflareNetworks() {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}
