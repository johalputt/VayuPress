// SPDX-License-Identifier: Apache-2.0

package render

import (
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/theme"
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

// A member page on a hosted site is drawn in that site's chrome.
func TestAMemberPageIsInItsSitesChrome(t *testing.T) {
	prev := GetActiveSettings()
	SetActiveSettings(SiteSettings{Name: "The Install"})
	t.Cleanup(func() { SetActiveSettings(prev) })
	withHalcyon(t, nil)
	page := `<!DOCTYPE html><html lang="en"><head></head><body></body></html>`
	for _, kind := range []MemberPage{MemberTask, MemberSite} {
		out := HalcyonMemberPageFor(&SiteSettings{Name: "Notes Site"}, page, kind)
		if !strings.Contains(out, "Notes Site") || strings.Contains(out, "The Install") {
			t.Errorf("member page %v is not in the site's chrome", kind)
		}
	}
}

// A site with Halcyon options of its own is drawn with them on every Halcyon
// page; one without is drawn with the install's.
func TestASitesOwnHalcyonOptionsDrawItsPages(t *testing.T) {
	withHalcyon(t, map[string]string{"face": "serif"})
	own := theme.ResolveHalcyon(map[string]string{"face": "sans"})
	for name, render := range map[string]func(s *SiteSettings) (string, error){
		"home": func(s *SiteSettings) (string, error) {
			return RenderHomePage(HomeInput{Settings: *s, Domain: "example.com"})
		},
		"topic": func(s *SiteSettings) (string, error) {
			return RenderTopicPage(TopicInput{Settings: s, Domain: "example.com", Tag: "go", Total: 40})
		},
		"search": func(s *SiteSettings) (string, error) { return RenderSearchPage(SearchInput{Settings: s, Query: "x"}) },
		"member": func(s *SiteSettings) (string, error) {
			return HalcyonMemberPageFor(s, `<!DOCTYPE html><html lang="en"><head></head><body></body></html>`, MemberSite), nil
		},
	} {
		mine, err := render(&SiteSettings{Name: "N", Halcyon: &own})
		if err != nil {
			t.Fatal(err)
		}
		theirs, _ := render(&SiteSettings{Name: "N"})
		if !strings.Contains(mine, `data-face="sans"`) || !strings.Contains(theirs, `data-face="serif"`) {
			t.Errorf("%s: the site's own options are not what it is drawn with", name)
		}
	}
}
