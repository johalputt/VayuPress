package analytics

import "testing"

func TestReferrerChannel(t *testing.T) {
	cases := map[string]string{
		"":                     "Direct",
		"   ":                  "Direct",
		"google.com":           "Organic search",
		"www.google.co.uk":     "Organic search",
		"bing.de":              "Organic search",
		"duckduckgo.com":       "Organic search",
		"search.brave.com":     "Organic search",
		"t.co":                 "Social",
		"x.com":                "Social",
		"twitter.com":          "Social",
		"m.facebook.com":       "Social",
		"reddit.com":           "Social",
		"news.ycombinator.com": "Social",
		"bsky.app":             "Social",
		"example.com":          "Referral",
		"some.blog.io":         "Referral",
	}
	for host, want := range cases {
		if got := ReferrerChannel(host); got != want {
			t.Errorf("ReferrerChannel(%q) = %q, want %q", host, got, want)
		}
	}
}
