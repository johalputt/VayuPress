// SPDX-License-Identifier: Apache-2.0

package classifier

import "testing"

func c(ref string) Result { return Classify(ref, "example.test", UTM{}, false) }

func TestOrganic(t *testing.T) {
	if r := c("https://www.google.com/search?q=x"); r.Category != Organic || r.Detail != "Google" {
		t.Fatalf("google -> %+v", r)
	}
	if r := c("https://duckduckgo.com/"); r.Category != Organic || r.Detail != "DuckDuckGo" {
		t.Fatalf("ddg -> %+v", r)
	}
}

func TestAIAssisted(t *testing.T) {
	for ref, want := range map[string]string{
		"https://chatgpt.com/":             "ChatGPT",
		"https://www.perplexity.ai/search": "Perplexity",
		"https://claude.ai/chat/123":       "Claude",
		"https://copilot.microsoft.com/":   "Copilot",
	} {
		r := c(ref)
		if r.Category != AIAssisted || r.Detail != want {
			t.Fatalf("%s -> %+v (want %s)", ref, r, want)
		}
	}
}

func TestDirect(t *testing.T) {
	if r := c(""); r.Category != Direct || r.Detail != "typed" {
		t.Fatalf("empty referrer -> %+v", r)
	}
	if r := c("https://example.test/other-post"); r.Category != Direct || r.Detail != "internal" {
		t.Fatalf("same-site -> %+v", r)
	}
	if r := c("https://sub.example.test/x"); r.Category != Direct {
		t.Fatalf("subdomain same-site -> %+v", r)
	}
}

func TestSocialAndCorporate(t *testing.T) {
	if r := c("https://t.co/abc"); r.Category != Social || r.Detail != "X/Twitter" {
		t.Fatalf("t.co -> %+v", r)
	}
	if r := c("https://statics.teams.cdn.office.net/x"); r.Category != Corporate {
		t.Fatalf("teams -> %+v", r)
	}
	if r := c("https://app.slack.com/x"); r.Category != Corporate || r.Detail != "Slack" {
		t.Fatalf("slack -> %+v", r)
	}
}

func TestEmailAndReferral(t *testing.T) {
	if r := c("https://mail.google.com/mail/u/0"); r.Category != Email {
		t.Fatalf("gmail -> %+v", r)
	}
	if r := c("https://someblog.example/post"); r.Category != Referral || r.Detail != "someblog.example" {
		t.Fatalf("referral -> %+v", r)
	}
}

func TestUTMOverrides(t *testing.T) {
	r := Classify("https://mail.google.com/", "example.test", UTM{Medium: "newsletter", Campaign: "july"}, false)
	if r.Category != Newsletter || r.Detail != "july" {
		t.Fatalf("newsletter utm -> %+v", r)
	}
	r = Classify("", "example.test", UTM{Source: "email"}, false)
	if r.Category != Email {
		t.Fatalf("email utm -> %+v", r)
	}
}

func TestBotShortCircuits(t *testing.T) {
	r := Classify("https://www.google.com/", "example.test", UTM{}, true)
	if r.Category != Bot {
		t.Fatalf("bot should short-circuit even with search referrer, got %+v", r)
	}
	// Referrer domain is still recorded for the bot view.
	if r.ReferrerDomain != "www.google.com" {
		t.Fatalf("referrer domain not recorded for bot: %+v", r)
	}
}
