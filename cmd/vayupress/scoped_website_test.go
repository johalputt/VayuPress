// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/bizsite"
	"github.com/johalputt/vayupress/internal/domain"
)

// ADR-0154 D9/D10 — a hosted domain can be a blog or a website, and either can
// be built by hand or by asking an assistant.
//
// The serving side has been per-domain since ADR-0132 Stage 2b. The admin side
// resolved by REQUEST HOST, and an operator's admin request carries no secondary
// host, so /os/website always edited the primary. A hosted domain's mode was
// reachable only from the CLI.

func websiteDomain(t *testing.T, mode, template, content string) domain.Domain {
	t.Helper()
	cfg, err := domain.EncodeSiteConfigInto("", domain.SiteConfig{Mode: mode, Template: template, Content: content})
	if err != nil {
		t.Fatalf("EncodeSiteConfigInto: %v", err)
	}
	return domain.Domain{ID: "s1", Host: "client.example", Status: domain.StatusActive,
		SyncState: domain.SyncApproved, TLSState: domain.TLSActive, ConfigJSON: cfg}
}

// A blank mode is not "unset" to a visitor — it serves the blog. Rendering the
// radio group with nothing selected would tell an operator their site serves
// nothing.
func TestABlankModeIsShownAsWhatItActuallyServes(t *testing.T) {
	if got := scopedSiteMode(domain.Domain{ID: "s1", Host: "client.example"}); got != "blog" {
		t.Errorf("a domain with no website override reports %q; it serves the blog", got)
	}
	page := scopedWebsitePage(domain.Domain{ID: "s1", Host: "client.example"}, "", bizsite.Content{})
	if !strings.Contains(page, `value="blog" checked`) {
		t.Error("the mode picker has nothing selected for a site with no override, so it reads " +
			"as serving nothing while it actually serves the blog")
	}
}

// The catalogue of modes must match what the renderer understands. A mode the
// page can offer and bizRootActive does not know is a domain that serves a blank
// page, discovered by a visitor.
func TestEveryOfferedModeIsOneTheRendererUnderstands(t *testing.T) {
	for _, m := range scopedSiteModes {
		switch m.Value {
		case "blog", "business", "business_subpath":
		default:
			t.Errorf("the website page offers mode %q, which the renderer does not handle", m.Value)
		}
	}
	if _, err := scopedWebsiteConfig(domain.Domain{}, "custom", "", bizsite.Content{}); err == nil {
		t.Error("an arbitrary mode was accepted — a stored mode nothing renders serves a blank page")
	}
}

// Saving from a form that does not render every field must not wipe the ones it
// omits. Losing a services grid nobody touched is worse than any layout bug.
func TestSavingKeepsTheFieldsThisPageDoesNotEdit(t *testing.T) {
	prev, err := json.Marshal(bizsite.Content{
		Name: "Old", Services: []bizsite.Service{{Title: "Haircut"}},
		Gallery: []string{"/a.jpg"}, SectionA: "What we do",
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	d := websiteDomain(t, "business", "studio", string(prev))

	cfg, err := scopedWebsiteConfig(d, "business", "studio", bizsite.Content{Name: "New", Tagline: "Fresh"})
	if err != nil {
		t.Fatalf("scopedWebsiteConfig: %v", err)
	}
	got := bizsite.ParseContent(cfg.Content)
	if got.Name != "New" {
		t.Errorf("the edited field did not save: %q", got.Name)
	}
	if len(got.Services) != 1 || got.Services[0].Title != "Haircut" {
		t.Error("saving the website form destroyed the services grid, which this page does not edit")
	}
	if len(got.Gallery) != 1 {
		t.Error("saving the website form destroyed the gallery")
	}
	if got.SectionA != "What we do" {
		t.Error("saving the website form destroyed the services heading")
	}
}

// An unknown template key must resolve rather than be stored: a stored key
// nothing matches renders an unstyled page later, far from the save that caused
// it.
func TestAnUnknownTemplateResolvesRatherThanBeingStored(t *testing.T) {
	cfg, err := scopedWebsiteConfig(domain.Domain{}, "business", "no-such-template", bizsite.Content{})
	if err != nil {
		t.Fatalf("scopedWebsiteConfig: %v", err)
	}
	if cfg.Template == "no-such-template" {
		t.Fatal("an unknown template key was stored verbatim; the site renders unstyled later, " +
			"with nothing connecting it to this save")
	}
	if bizsite.ByKey(cfg.Template).Key != cfg.Template {
		t.Errorf("the stored template %q is not one the renderer knows", cfg.Template)
	}
}

// The MCP write path must share the console's validator. Two validators for one
// shape is how one of them comes to accept a mode the other refuses.
func TestTheAssistantAndTheConsoleShareOneValidator(t *testing.T) {
	src := readSourceFile(t, "mcp_sites.go")
	body := goFuncBody(src, "registerSiteTools")
	if !strings.Contains(body, "scopedWebsiteConfig(") {
		t.Error("the MCP site tools validate independently of the console, so the two surfaces " +
			"can disagree about what a valid mode or template is")
	}
	// And it must refuse the primary by name rather than letting SetSite fail
	// with something an assistant will just retry.
	look := goFuncBody(src, "mcpSiteByHost")
	if !strings.Contains(look, "IsPrimary") {
		t.Error("the host lookup does not refuse the primary, so an assistant is handed an " +
			"opaque failure from the registry instead of the reason")
	}
}

// An omitted field must be distinguishable from one set to empty, or an
// assistant editing one line blanks every other field on a live website.
func TestOmittedFieldsAreNotTreatedAsBlanking(t *testing.T) {
	body := goFuncBody(readSourceFile(t, "mcp_sites.go"), "registerSiteTools")
	i := strings.Index(body, `Name: "update_site"`)
	if i < 0 {
		t.Fatal("update_site is gone")
	}
	seg := body[i:]
	if !strings.Contains(seg, "Tagline  *string") {
		t.Fatal("update_site decodes its content fields as plain strings, so an assistant that " +
			"sends only a new tagline blanks the name, about, phone and every other field on " +
			"somebody's live website")
	}
}
