// SPDX-License-Identifier: Apache-2.0

package classifier

import "testing"

// Referrer attribution must survive the 2025 audit's spoof class: a host that
// merely CONTAINS a table key ("box.com" contains x.com) or shares a substring
// label ("notmastodon.com" contains mastodon) must never claim that brand.
func TestMatchHostSpoofResistance(t *testing.T) {
	cases := []struct {
		referrer string
		want     Category
		wantDet  string
	}{
		{"https://box.com/article", Referral, "box.com"},          // contains "x.com"
		{"https://wix.com/site", Referral, "wix.com"},             // contains "x.com"
		{"https://notmastodon.com/", Referral, "notmastodon.com"}, // contains label mastodon
		{"https://bing.com.evil.tld/", Referral, "bing.com.evil.tld"},
	}
	for _, c := range cases {
		got := Classify(c.referrer, "example.com", UTM{}, false)
		if got.Category != c.want || got.Detail != c.wantDet {
			t.Errorf("%s -> %s/%s, want %s/%s", c.referrer, got.Category, got.Detail, c.want, c.wantDet)
		}
	}

	truth := []struct {
		referrer string
		wantDet  string
	}{
		{"https://www.google.com/search?q=x", "Google"},
		{"https://google.de/search?q=x", "Google"},
		{"https://news.google.com/", "Google"},
		{"https://x.com/user/status/1", "X/Twitter"},
		{"https://t.co/abc", "X/Twitter"},
		{"https://mastodon.social/@user", "Mastodon"},
		{"https://www.bing.com/search?q=x", "Bing"},
	}
	for _, c := range truth {
		got := Classify(c.referrer, "example.com", UTM{}, false)
		if got.Detail != c.wantDet {
			t.Errorf("%s -> %s/%s, want detail %s", c.referrer, got.Category, got.Detail, c.wantDet)
		}
	}
}
