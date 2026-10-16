// SPDX-License-Identifier: Apache-2.0

package render

import (
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/config"
)

// The site's links switch (settings.KeyNoFollowLinks): off, no link in a
// post carries nofollow, where bluemonday's preset marked every one; on, a
// link to another site does, and a link within the site still does not, by
// either way it is written. One seed per kind of link.
func TestAPostsLinksFollowTheSitesSwitch(t *testing.T) {
	Init(t.TempDir())
	prevDomain := config.Cfg.Domain
	config.Cfg.Domain = "johal.in"
	t.Cleanup(func() { config.Cfg.Domain = prevDomain; SetActiveSettings(SiteSettings{}) })

	elsewhere := `<p><a href="https://www.bairesdev.com/guide">a source</a></p>`
	within := map[string]string{
		"relative":        `<p><a href="/zero-downtime-migrations">another post</a></p>`,
		"the site's host": `<p><a href="https://johal.in/zero-downtime-migrations">another post</a></p>`,
		"the site's www":  `<p><a href="https://www.johal.in/x">another post</a></p>`,
		"a fragment":      `<p><a href="#faq">below</a></p>`,
	}

	SetActiveSettings(SiteSettings{NoFollowLinks: false})
	if out := renderContentHTML(elsewhere); strings.Contains(out, "nofollow") {
		t.Errorf("switch off: a link to another site carries nofollow: %s", out)
	}
	SetActiveSettings(SiteSettings{NoFollowLinks: true})
	if out := renderContentHTML(elsewhere); !strings.Contains(out, `<a href="https://www.bairesdev.com/guide" rel="nofollow">`) {
		t.Errorf("switch on: a link to another site is not nofollow: %s", out)
	}
	for kind, in := range within {
		if out := renderContentHTML(in); strings.Contains(out, "nofollow") {
			t.Errorf("switch on: a link within the site (%s) carries nofollow: %s", kind, out)
		}
	}
}
