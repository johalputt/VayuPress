// SPDX-License-Identifier: Apache-2.0

package analytics

import (
	"context"
	"testing"
)

func TestReferrerChannel(t *testing.T) {
	cases := map[string]string{
		"":                     "Direct",
		"   ":                  "Direct",
		"google.com":           "Organic search",
		"www.google.co.uk":     "Organic search",
		"bing.com":             "Organic search",
		"duckduckgo.com":       "Organic search",
		"search.brave.com":     "Organic search",
		"t.co":                 "Social",
		"x.com":                "Social",
		"twitter.com":          "Social",
		"m.facebook.com":       "Social",
		"reddit.com":           "Social",
		"news.ycombinator.com": "Social",
		"bsky.app":             "Social",
		"chatgpt.com":          "AI assistants",
		"mail.google.com":      "Email",
		"app.slack.com":        "Workplace apps",
		"cnet.com":             "Referral",
		"dropbox.com":          "Referral",
		"example.com":          "Referral",
		"some.blog.io":         "Referral",
	}
	for host, want := range cases {
		if got := ReferrerChannel(host); got != want {
			t.Errorf("ReferrerChannel(%q) = %q, want %q", host, got, want)
		}
	}
}

// A view one of the install's own pages led to is internal navigation:
// Direct, as the engagement classifier has it, never a referral from the
// site to itself.
func TestOwnPagesAreNotAReferral(t *testing.T) {
	s, _ := contractStore(t)
	got, err := s.ChannelsScoped(context.Background(), "s1", 2)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]int{"Direct": 6, "Organic search": 1, "Social": 1}
	if len(got) != len(want) {
		t.Fatalf("channels = %+v, want %v", got, want)
	}
	for _, c := range got {
		if want[c.Label] != c.Count {
			t.Errorf("channel %s = %d, want %d", c.Label, c.Count, want[c.Label])
		}
	}
}
