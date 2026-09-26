// SPDX-License-Identifier: Apache-2.0

package render

import (
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/johalputt/vayupress/internal/config"
)

// One seed per rule the grammar applies, each asserting the reason it gives:
// a seed that broke two rules could not say which one refused it.
func TestATypedOriginIsRefusedForItsOwnReason(t *testing.T) {
	for _, c := range []struct{ dir, src, reason string }{
		{"script-src", "https://cdn.example.com", "only from the listed services"},
		{"connect-src", "https://api.example.com", "only from the listed services"},
		{"frame-ancestors", "https://example.com", "not a directive a site can add to"},
		{"img-src", "https://example.com 'unsafe-inline'", "no quotes, spaces"},
		{"img-src", "https://a.example;script-src", "no quotes, spaces"},
		{"img-src", "http://example.com", "starts with https://"},
		{"img-src", "https:", "starts with https://"},
		{"img-src", "*", "starts with https://"},
		{"img-src", "https://example.com/images", "no path"},
		{"img-src", "https://user@example.com", "no path"},
		{"img-src", "https://example.com:http", "port must be a number"},
		{"img-src", "https://93.184.216.34", "not an IP address"},
		{"img-src", "https://[::1]:443", "not an IP address"},
		{"img-src", "https://cdn.*.example.com", "not a host name"},
		{"img-src", "https://localhost", "not a host name"},
		{"img-src", "https://ex_ample.com", "not a host name"},
	} {
		err := ValidateSource(c.dir, c.src)
		if err == nil || !strings.Contains(err.Error(), c.reason) {
			t.Errorf("%s %q: %v, want refused because %q", c.dir, c.src, err, c.reason)
		}
	}
	for _, c := range []struct{ dir, src string }{
		{"img-src", "https://images.example.com"},
		{"font-src", "https://*.example.com"},
		{"frame-src", "https://embed.example.co.uk:8443"},
		{"form-action", "https://example.list-manage.com"},
		{"style-src", "https://xn--bcher-kva.example"},
	} {
		if err := ValidateSource(c.dir, c.src); err != nil {
			t.Errorf("%s %q was refused: %v", c.dir, c.src, err)
		}
	}
}

// The catalogue is the only way to allow scripts, so every origin in it is
// held to a stricter, independent rule than the typed grammar: https or wss,
// a host, nothing else.
func TestEveryServiceOriginIsAPlainOrigin(t *testing.T) {
	origin := regexp.MustCompile(`^(https|wss)://(\*\.)?[a-z0-9-]+(\.[a-z0-9-]+)+$`)
	ids := map[string]bool{}
	for _, s := range CSPServices() {
		if ids[s.ID] {
			t.Errorf("service id %q is listed twice", s.ID)
		}
		ids[s.ID] = true
		for dir, list := range s.Sources {
			for _, src := range list {
				if !origin.MatchString(src) {
					t.Errorf("%s %s: %q is not a plain origin", s.ID, dir, src)
				}
				if strings.HasPrefix(src, "wss://") && dir != "connect-src" {
					t.Errorf("%s: a websocket origin under %s", s.ID, dir)
				}
			}
		}
	}
	for _, cdn := range []string{"cdn.jsdelivr.net", "cdnjs.cloudflare.com", "unpkg.com"} {
		for _, s := range CSPServices() {
			for _, src := range s.Sources["script-src"] {
				if strings.Contains(src, cdn) {
					t.Errorf("%s allows scripts from the public CDN %s, which serves anyone's code", s.ID, cdn)
				}
			}
		}
	}
}

func TestAStrictSiteKeepsTheBaselineByteForByte(t *testing.T) {
	base := BuildCSP("n0nce", nil)
	p := SitePolicy{Mode: PolicyStrict, Services: []string{"stripe"}, Sources: map[string][]string{"img-src": {"https://a.example.com"}}}
	if got := p.Apply(base); got != base {
		t.Errorf("a strict site's policy changed:\n%s\n%s", base, got)
	}
}

func TestAllowancesMergeIntoWhateverPolicyThePageBuilt(t *testing.T) {
	p := SitePolicy{Mode: PolicyCustom, Services: []string{"stripe", "google-fonts"},
		Sources:      map[string][]string{"media-src": {"https://media.example.com"}, "img-src": {"https://images.example.com"}},
		InlineStyles: true}
	got := p.Apply(BuildCSP("n0nce", []string{"https://www.youtube-nocookie.com"}))
	dir := func(name string) string {
		for _, part := range strings.Split(got, ";") {
			if f := strings.Fields(part); len(f) > 0 && f[0] == name {
				return strings.Join(f, " ")
			}
		}
		return ""
	}
	for name, want := range map[string]string{
		// The nonce and the theme hash survive; the service's origin is added.
		"script-src": "script-src 'self' 'nonce-n0nce' " + ThemeToggleCSPHash + " https://js.stripe.com",
		// A directive the page already widened keeps its widening.
		"frame-src": "frame-src 'self' https://www.youtube-nocookie.com https://hooks.stripe.com https://js.stripe.com",
		// Absent before, so default-src 'self' applied: created with 'self' first.
		"media-src": "media-src 'self' https://media.example.com",
		"style-src": "style-src 'self' 'unsafe-inline' https://fonts.googleapis.com",
		"font-src":  "font-src 'self' https://fonts.gstatic.com",
		// img-src admits every https: origin already; nothing is repeated.
		"img-src": "img-src 'self' data: https:",
	} {
		if d := dir(name); d != want {
			t.Errorf("%s\n got %s\nwant %s", name, d, want)
		}
	}
	if !strings.HasPrefix(got, "default-src 'self'; ") || strings.Contains(got, ";;") {
		t.Errorf("the header lost its shape: %s", got)
	}
}

// A stored policy is trusted no further than one being saved: a script origin
// written straight into the setting never reaches the header.
func TestAStoredPolicyCannotSmuggleAScriptOrigin(t *testing.T) {
	p := SitePolicy{Mode: PolicyCustom, Sources: map[string][]string{
		"script-src": {"https://evil.example"},
		"img-src":    {"https://ok.example.com", "http://plain.example.com"},
	}}
	got := p.Apply(BuildCSP("n", nil))
	if strings.Contains(got, "evil.example") || strings.Contains(got, "plain.example.com") {
		t.Errorf("an origin the grammar refuses reached the header: %s", got)
	}
}

func TestTheTorWorldStillStripsEveryOutsideOrigin(t *testing.T) {
	defer func(v bool) { config.Cfg.OnionMode = v }(config.Cfg.OnionMode)
	config.Cfg.OnionMode = true
	p := SitePolicy{Mode: PolicyCustom, Services: []string{"stripe"}, Sources: map[string][]string{"font-src": {"https://fonts.example.com"}}}
	if got := p.Apply(BuildCSP("n", nil)); strings.Contains(got, "https://") {
		t.Errorf("an outside origin survived in the Tor world: %s", got)
	}
}

func TestSavingNormalizesAndRefusesTheUnknown(t *testing.T) {
	got, err := SitePolicy{Mode: "custom", Services: []string{"vimeo", "stripe", "vimeo"},
		Sources: map[string][]string{"img-src": {" HTTPS://B.example.com", "https://a.example.com", "https://a.example.com"}}}.Normalize()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got.Services, ",") != "stripe,vimeo" || strings.Join(got.Sources["img-src"], ",") != "https://a.example.com,https://b.example.com" {
		t.Errorf("not normalized: %+v", got)
	}
	for _, c := range []struct {
		p      SitePolicy
		reason string
	}{
		{SitePolicy{Mode: "open"}, `unknown mode "open"`},
		{SitePolicy{Mode: "custom", Services: []string{"jsdelivr"}}, `unknown service "jsdelivr"`},
		{SitePolicy{Mode: "custom", Sources: map[string][]string{"script-src": {"https://x.example.com"}}}, "only from the listed services"},
	} {
		if _, err := c.p.Normalize(); err == nil || !strings.Contains(err.Error(), c.reason) {
			t.Errorf("%+v: %v, want %q", c.p, err, c.reason)
		}
	}
}

func TestReportOnlyEndsOnItsOwn(t *testing.T) {
	now := time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)
	p := SitePolicy{Mode: PolicyReport, ReportUntil: now.Add(time.Hour)}
	if p.Enforced(now) != PolicyReport {
		t.Error("report-only ended early")
	}
	if p.Enforced(now.Add(time.Hour)) != PolicyCustom {
		t.Error("report-only outlived its end")
	}
}
