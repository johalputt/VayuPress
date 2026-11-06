// SPDX-License-Identifier: Apache-2.0

package render

import (
	"strings"
	"testing"
)

// A hosted site's topic, topics, tag and search pages are drawn in its own
// name, in Halcyon and in the classic theme: they read the install's
// settings, so a hosted blog's topic page wore the install's name and menu.
func TestASitesListingPagesAreInItsOwnName(t *testing.T) {
	prev := GetActiveSettings()
	SetActiveSettings(SiteSettings{Name: "The Install"})
	t.Cleanup(func() { SetActiveSettings(prev) })
	site := &SiteSettings{Name: "Notes Site"}
	pages := func() map[string]func() (string, error) {
		return map[string]func() (string, error){
			"topic": func() (string, error) {
				return RenderTopicPage(TopicInput{Settings: site, Domain: "example.com", Tag: "go", Total: 40})
			},
			"search": func() (string, error) { return RenderSearchPage(SearchInput{Settings: site, Query: "x"}) },
			"topics": func() (string, error) {
				return RenderTagIndexFor(site, "example.com", "1", []TagInfo{{Name: "go", Count: 3}}, 3)
			},
			"tag": func() (string, error) { return RenderTagPageFor(site, "example.com", "1", "go", nil, 40, false) },
		}
	}
	check := func(themeName string) {
		for name, render := range pages() {
			page, err := render()
			if err != nil {
				t.Fatalf("%s %s: %v", themeName, name, err)
			}
			if !strings.Contains(page, "Notes Site") || strings.Contains(page, "The Install") {
				t.Errorf("%s %s page is not in the site's own name", themeName, name)
			}
		}
	}
	check("classic")
	withHalcyon(t, nil)
	check("Halcyon")
}
