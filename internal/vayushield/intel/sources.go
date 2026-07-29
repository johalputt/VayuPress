// SPDX-License-Identifier: Apache-2.0

package intel

import (
	"encoding/json"
	"net/netip"
	"strings"
)

// sources.go — the feeds this build ships, and why each one is here.
//
// # What belongs in this file
//
// Two kinds of source, and the bar is different for each.
//
// DATACENTER feeds must be FIRST-PARTY: published by the network operator about
// their own address space. AWS saying which ranges are AWS is not an opinion, it
// is a fact from the only party who knows. That is why there is no aggregated
// "known VPN and hosting IPs" list here — those are compiled by third parties
// from inference, and inference about someone's connection is exactly the thing
// that must not silently cost a reader access.
//
// HOSTILE feeds must be CONSERVATIVE BY CONSTRUCTION. Spamhaus DROP qualifies
// because of what it is: netblocks that are hijacked or under criminal control,
// where the publisher's own guidance is not to route to them at all. Community
// abuse-report aggregations do not qualify, however useful — they carry a real
// false-positive rate, and this tier can deny.
//
// # Licensing is the operator's to accept
//
// These are third-party lists with third-party terms, and some restrict
// commercial use. Every feed ships DISABLED, and the panel names the publisher
// so an operator opts in to a source they have chosen rather than to a URL
// somebody embedded.

// DefaultFeeds returns the shipped feed definitions, all disabled until an
// operator turns one on.
func DefaultFeeds() []Feed {
	return []Feed{
		{
			ID:   "aws",
			Name: "AWS published ranges",
			URL:  "https://ip-ranges.amazonaws.com/ip-ranges.json",
			Kind: KindDatacenter,
			Note: "Published by AWS about its own address space. Datacenter membership is " +
				"evidence a visitor is automated, never proof — a VPN exit and a corporate " +
				"egress both live here.",
			Parse: parseAWS,
		},
		{
			ID:   "gcp",
			Name: "Google Cloud published ranges",
			URL:  "https://www.gstatic.com/ipranges/cloud.json",
			Kind: KindDatacenter,
			Note: "Published by Google about its own cloud address space. Note this is CLOUD " +
				"only — it deliberately does not include Googlebot, which is verified separately " +
				"and must never be scored as automation.",
			Parse: parseGCP,
		},
		{
			ID:    "digitalocean",
			Name:  "DigitalOcean published ranges",
			URL:   "https://www.digitalocean.com/geo/google.csv",
			Kind:  KindDatacenter,
			Note:  "Published by DigitalOcean about its own address space.",
			Parse: parseCSVFirstField,
		},
		{
			ID:   "spamhaus-drop",
			Name: "Spamhaus DROP",
			URL:  "https://www.spamhaus.org/drop/drop.txt",
			Kind: KindHostile,
			Note: "Netblocks Spamhaus lists as hijacked or under criminal control — their own " +
				"guidance is not to route to them at all. Conservative by construction, which is " +
				"why it is the only list here allowed to deny. Check the licence for your use.",
			Parse: parseDROP,
		},
	}
}

// parseAWS reads the ip-ranges.json shape: two arrays, one per address family,
// each entry naming its prefix under a family-specific key.
func parseAWS(b []byte) ([]netip.Prefix, error) {
	var doc struct {
		Prefixes []struct {
			IPPrefix string `json:"ip_prefix"`
		} `json:"prefixes"`
		IPv6Prefixes []struct {
			IPv6Prefix string `json:"ipv6_prefix"`
		} `json:"ipv6_prefixes"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		return nil, err
	}
	out := make([]netip.Prefix, 0, len(doc.Prefixes)+len(doc.IPv6Prefixes))
	for _, p := range doc.Prefixes {
		if v, err := netip.ParsePrefix(p.IPPrefix); err == nil {
			out = append(out, v)
		}
	}
	for _, p := range doc.IPv6Prefixes {
		if v, err := netip.ParsePrefix(p.IPv6Prefix); err == nil {
			out = append(out, v)
		}
	}
	return requireSome(out)
}

// parseGCP reads the cloud.json shape: one array whose entries carry either an
// ipv4Prefix or an ipv6Prefix.
func parseGCP(b []byte) ([]netip.Prefix, error) {
	var doc struct {
		Prefixes []struct {
			V4 string `json:"ipv4Prefix"`
			V6 string `json:"ipv6Prefix"`
		} `json:"prefixes"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		return nil, err
	}
	out := make([]netip.Prefix, 0, len(doc.Prefixes))
	for _, p := range doc.Prefixes {
		for _, raw := range []string{p.V4, p.V6} {
			if raw == "" {
				continue
			}
			if v, err := netip.ParsePrefix(raw); err == nil {
				out = append(out, v)
			}
		}
	}
	return requireSome(out)
}

// parseCSVFirstField reads a headerless CSV whose first column is the prefix.
func parseCSVFirstField(b []byte) ([]netip.Prefix, error) {
	var out []netip.Prefix
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		field := line
		if i := strings.IndexByte(line, ','); i >= 0 {
			field = line[:i]
		}
		if v, err := netip.ParsePrefix(strings.TrimSpace(field)); err == nil {
			out = append(out, v)
		}
	}
	return requireSome(out)
}

// parseDROP reads the Spamhaus DROP text shape: "<cidr> ; SBL<id>", with ";"
// comment lines.
//
// Deliberately strict about the leading field rather than scanning each line for
// anything CIDR-shaped. A tolerant parser would keep working if the endpoint
// started serving something else entirely, which is the opposite of what is
// wanted from the one feed here permitted to deny.
func parseDROP(b []byte) ([]netip.Prefix, error) {
	var out []netip.Prefix
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, ";") {
			continue
		}
		field := line
		if i := strings.IndexByte(line, ';'); i >= 0 {
			field = strings.TrimSpace(line[:i])
		}
		v, err := netip.ParsePrefix(field)
		if err != nil {
			// A line that is neither a comment nor a prefix means this is not the
			// document it is supposed to be. Fail the whole parse rather than
			// salvage what looks familiar.
			return nil, errUnexpectedShape
		}
		out = append(out, v)
	}
	return requireSome(out)
}

// requireSome refuses an empty result.
//
// A 200 with an empty or unrecognised body is the commonest broken-endpoint
// shape there is, and a feed that parsed to nothing would otherwise be applied
// as "this list is now empty" — silently disarming the layer while every
// indicator still reads healthy.
func requireSome(out []netip.Prefix) ([]netip.Prefix, error) {
	if len(out) == 0 {
		return nil, errNoPrefixes
	}
	return out, nil
}

const (
	errNoPrefixes      intelError = "intel: the feed contained no usable prefixes"
	errUnexpectedShape intelError = "intel: the feed is not in the expected format"
)
