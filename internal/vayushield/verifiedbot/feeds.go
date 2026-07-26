// SPDX-License-Identifier: Apache-2.0

package verifiedbot

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
)

// maxFeedBytes caps a single feed response so a hostile or broken endpoint
// cannot exhaust memory. Real crawler feeds are a few KiB to low hundreds of KiB.
const maxFeedBytes = 8 << 20 // 8 MiB

// googleFeed is the shape Google/Bing/OpenAI/Anthropic/Apple/Perplexity all use:
// {"prefixes":[{"ipv4Prefix":"1.2.3.0/24"},{"ipv6Prefix":"2001:db8::/32"}]}.
type googleFeed struct {
	Prefixes []struct {
		IPv4 string `json:"ipv4Prefix"`
		IPv6 string `json:"ipv6Prefix"`
		// A few feeds use "ipv4"/"ipv6" or "cidr" — accept those too.
		IPv4Alt string `json:"ipv4"`
		IPv6Alt string `json:"ipv6"`
		CIDR    string `json:"cidr"`
	} `json:"prefixes"`
}

// parsePrefixes tolerantly extracts CIDRs from a vendor feed. It accepts the
// Google "prefixes" object shape and a bare JSON array of IP/CIDR strings
// (DuckDuckGo-style), and treats a bare address as a /32 or /128 host route.
func parsePrefixes(data []byte) ([]netip.Prefix, error) {
	data = []byte(strings.TrimSpace(string(data)))
	if len(data) == 0 {
		return nil, fmt.Errorf("empty feed")
	}
	var out []netip.Prefix
	add := func(s string) {
		s = strings.TrimSpace(s)
		if s == "" {
			return
		}
		if strings.Contains(s, "/") {
			if p, err := netip.ParsePrefix(s); err == nil {
				out = append(out, p)
			}
			return
		}
		if a, err := netip.ParseAddr(s); err == nil {
			out = append(out, netip.PrefixFrom(a, a.BitLen()))
		}
	}

	switch data[0] {
	case '{':
		var gf googleFeed
		if err := json.Unmarshal(data, &gf); err != nil {
			return nil, err
		}
		for _, p := range gf.Prefixes {
			add(p.IPv4)
			add(p.IPv6)
			add(p.IPv4Alt)
			add(p.IPv6Alt)
			add(p.CIDR)
		}
	case '[':
		var arr []string
		if err := json.Unmarshal(data, &arr); err != nil {
			return nil, err
		}
		for _, s := range arr {
			add(s)
		}
	default:
		return nil, fmt.Errorf("unrecognised feed format")
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("feed contained no usable prefixes")
	}
	return out, nil
}

// cacheFile returns the disk-cache path for a feed URL (hashed, so any URL maps
// to a safe filename).
func (v *Verifier) cacheFile(url string) string {
	if v.cfg.CacheDir == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(url))
	return filepath.Join(v.cfg.CacheDir, "vbfeed-"+hex.EncodeToString(sum[:8])+".json")
}

// loadFromDisk seeds each feed vendor's CIDR set from the last-good cache on
// startup, so verification works immediately (and offline) before any network
// fetch — critical for not de-indexing right after a restart.
func (v *Verifier) loadFromDisk() {
	for i := range registry {
		vd := &registry[i]
		if vd.tier != tierFeed {
			continue
		}
		var prefixes []netip.Prefix
		for _, url := range vd.feeds {
			path := v.cacheFile(url)
			if path == "" {
				continue
			}
			data, err := os.ReadFile(path) //nolint:gosec // hashed, app-owned cache path
			if err != nil {
				continue
			}
			if pfx, err := parsePrefixes(data); err == nil {
				prefixes = append(prefixes, pfx...)
			}
		}
		if len(prefixes) > 0 {
			v.sets[vd.name].store(newCIDRSet(prefixes))
			v.logf("verifiedbot: %s loaded %d prefixes from cache", vd.name, len(prefixes))
		}
	}
}

// refreshAll fetches every feed vendor's IP ranges and swaps in the new set.
// On any per-URL error the last-good set is kept (fail-open), so a transient
// fetch failure — or a locked-down Tor Space with no clearnet egress — never
// starts challenging real crawlers.
func (v *Verifier) refreshAll(ctx context.Context) {
	if v.cfg.Client == nil {
		return // no HTTP client injected: rely on disk cache + FCrDNS + UA fallback
	}
	for i := range registry {
		vd := &registry[i]
		if vd.tier != tierFeed {
			continue
		}
		var prefixes []netip.Prefix
		gotAny := false
		for _, url := range vd.feeds {
			data, err := v.fetch(ctx, url)
			if err != nil {
				v.logf("verifiedbot: %s fetch %s failed: %v (keeping last-good)", vd.name, url, err)
				continue
			}
			pfx, err := parsePrefixes(data)
			if err != nil {
				v.logf("verifiedbot: %s parse %s failed: %v", vd.name, url, err)
				continue
			}
			prefixes = append(prefixes, pfx...)
			gotAny = true
			if path := v.cacheFile(url); path != "" {
				_ = os.MkdirAll(v.cfg.CacheDir, 0o750)
				_ = os.WriteFile(path, data, 0o600)
			}
		}
		if gotAny && len(prefixes) > 0 {
			v.sets[vd.name].store(newCIDRSet(prefixes))
			v.logf("verifiedbot: %s refreshed %d prefixes", vd.name, len(prefixes))
		}
	}
}

func (v *Verifier) fetch(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "VayuShield-VerifiedBot/1.0 (+https://vayupress.com)")
	req.Header.Set("Accept", "application/json")
	resp, err := v.cfg.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %d", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, maxFeedBytes))
}
