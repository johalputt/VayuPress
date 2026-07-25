package render

import (
	"strings"
	"testing"
	"time"

	"github.com/microcosm-cc/bluemonday"

	"github.com/johalputt/vayupress/internal/config"
	"github.com/johalputt/vayupress/internal/db"
)

// setupRenderForTZ initialises the sanitiser + site settings the article renderer
// needs (mirrors the other render tests).
func setupRenderForTZ(t *testing.T) {
	t.Helper()
	policy = bluemonday.UGCPolicy()
	config.Cfg.Domain = "example.com"
	SetActiveSettings(SiteSettings{Name: "Acme"})
	t.Cleanup(func() { SetActiveSettings(SiteSettings{}) })
}

// TestPublicPostDateUsesSiteTimezone: the date a reader (and a crawler) sees must
// be the site's LOCAL date. A post created at 20:00 UTC is 26 July in IST, not
// 25 July — rendering UTC showed readers the wrong day, and the visible date
// disagreed with the machine <time datetime> and schema.org datePublished.
func TestPublicPostDateUsesSiteTimezone(t *testing.T) {
	setupRenderForTZ(t)
	t.Cleanup(func() { _ = config.SetSiteTimeZone("") })
	if err := config.SetSiteTimeZone("Asia/Kolkata"); err != nil {
		t.Skipf("zone database unavailable: %v", err)
	}
	created := time.Date(2026, 7, 25, 20, 0, 0, 0, time.UTC)
	art := db.Article{
		Slug:      "tz-post",
		Title:     "Timezone post",
		Content:   "<p>body</p>",
		Status:    "published",
		CreatedAt: created,
		UpdatedAt: created,
	}
	out, err := RenderArticle(art)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	// The human-readable date must be the local day.
	if !strings.Contains(out, "26 July 2026") {
		t.Error("rendered post should show the site-local date 26 July 2026")
	}
	if strings.Contains(out, "25 July 2026") {
		t.Error("rendered post must not show the UTC day 25 July 2026")
	}
	// The machine datetime must agree with it.
	if !strings.Contains(out, `datetime="2026-07-26"`) {
		t.Error(`<time datetime> should be the site-local date 2026-07-26`)
	}
	// And the structured/OG timestamps stay unambiguous by carrying the offset.
	if !strings.Contains(out, "2026-07-26T01:30") {
		t.Error("isoDate timestamps should render the local instant")
	}
	// The offset is present but html/template escapes the "+" ("&#43;" in an
	// attribute, "\u002b" inside the JSON-LD) — both decode to "+05:30", so accept
	// either encoding rather than asserting the raw byte.
	if !strings.Contains(out, "&#43;05:30") && !strings.Contains(out, `\u002b05:30`) {
		t.Error("isoDate must keep a zone offset so it stays machine-unambiguous")
	}
}

// TestPublicPostDateUTCByDefault: an install that never sets a timezone keeps the
// previous UTC behaviour exactly.
func TestPublicPostDateUTCByDefault(t *testing.T) {
	setupRenderForTZ(t)
	t.Cleanup(func() { _ = config.SetSiteTimeZone("") })
	_ = config.SetSiteTimeZone("")
	created := time.Date(2026, 7, 25, 20, 0, 0, 0, time.UTC)
	out, err := RenderArticle(db.Article{
		Slug: "utc-post", Title: "UTC post", Content: "<p>b</p>",
		Status: "published", CreatedAt: created, UpdatedAt: created,
	})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(out, "25 July 2026") {
		t.Error("with no timezone configured the UTC date must be shown")
	}
}
