// SPDX-License-Identifier: Apache-2.0

package render

import (
	"strings"
	"testing"
	"time"

	"github.com/johalputt/vayupress/internal/config"
	"github.com/johalputt/vayupress/internal/db"
	"github.com/microcosm-cc/bluemonday"
)

// The author card was designed and never built. The admin exposed a bio field
// ("One line about the author"), the design studio offered Show/Hide for it,
// twelve theme stylesheets styled .vayu-author-box / -avatar / -name / -bio,
// ADR-0086 named it, and the whole-site coverage gate required every theme to
// style it — while no page ever contained the class. The bio an operator typed
// went into settings, through SiteSettings, into the article template's data,
// and stopped there.
//
// These tests are the difference between that state and this one.

func authorArticle(t *testing.T, s SiteSettings, art db.Article, ov ArticleMetaOverrides) string {
	t.Helper()
	policy = bluemonday.UGCPolicy()
	config.Cfg.Domain = "example.com"
	prev := getActiveSettings()
	SetActiveSettings(s)
	t.Cleanup(func() { SetActiveSettings(prev) })

	out, err := RenderArticleWithMetaSettings(s, art, ArticleLayoutDefault, nil, ov)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	return out
}

func samplePost() db.Article {
	return db.Article{
		ID: "1", Title: "Hello", Slug: "hello", Content: "<p>Body.</p>",
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
}

func TestAuthorBoxRendersWithTheBio(t *testing.T) {
	out := authorArticle(t, SiteSettings{
		Name: "Acme", Author: "A Person", AuthorBio: "Writes about instruments.",
	}, samplePost(), ArticleMetaOverrides{})

	// The class names are the ones the twelve theme stylesheets were written
	// against. Emitting anything else would leave their rules dead.
	for _, want := range []string{
		`class="vayu-author-box"`,
		`vayu-author-avatar`,
		`class="vayu-author-name"`,
		`class="vayu-author-bio"`,
		"Writes about instruments.",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the author card is missing %q", want)
		}
	}
}

// Gating on the bio rather than on a flag is what keeps this from appearing on
// every existing site the moment it ships. An operator who never filled the
// field in sees no change at all.
func TestAuthorBoxIsSilentWithoutABio(t *testing.T) {
	out := authorArticle(t, SiteSettings{Name: "Acme", Author: "A Person"},
		samplePost(), ArticleMetaOverrides{})
	if strings.Contains(out, "vayu-author-box") {
		t.Error("an author with no bio still produced an author card — every existing site would grow one")
	}
	// …and the byline, which does not depend on the bio, must still be there.
	if !strings.Contains(out, "vayu-byline") {
		t.Error("the byline vanished along with the author card")
	}
}

// A standalone page is not written by a byline-carrying author, and the rest of
// the post chrome is already suppressed for pages.
func TestAuthorBoxIsSuppressedOnPages(t *testing.T) {
	out := authorArticle(t, SiteSettings{
		Name: "Acme", Author: "A Person", AuthorBio: "Writes about instruments.",
	}, samplePost(), ArticleMetaOverrides{IsPage: true})
	if strings.Contains(out, "vayu-author-box") {
		t.Error("a standalone page carries an author card")
	}
}

// The bio is operator-supplied text landing in HTML. html/template escapes it,
// but the whole point of the L12 lesson is that the mechanism gets named and
// tested rather than assumed.
func TestAuthorBoxEscapesTheBio(t *testing.T) {
	out := authorArticle(t, SiteSettings{
		Name: "Acme", Author: `Evil</strong><script>alert(1)</script>`,
		AuthorBio: `Bio with <script>alert(2)</script> and "quotes".`,
	}, samplePost(), ArticleMetaOverrides{})

	if strings.Contains(out, "<script>alert(1)</script>") || strings.Contains(out, "<script>alert(2)</script>") {
		t.Fatal("the author name or bio escaped into live markup")
	}
	if !strings.Contains(out, "alert(2)") {
		t.Error("the bio was dropped entirely rather than escaped")
	}
}

// The avatar is optional; without one the initials placeholder must be a real
// element the themes can size, not a bare letter.
func TestAuthorBoxFallsBackToInitials(t *testing.T) {
	out := authorArticle(t, SiteSettings{
		Name: "Acme", Author: "A Person", AuthorBio: "Bio.",
	}, samplePost(), ArticleMetaOverrides{})
	if !strings.Contains(out, "vayu-author-avatar--ph") {
		t.Error("no initials placeholder was rendered for an author without an avatar")
	}
	if strings.Contains(out, `<img class="vayu-author-avatar"`) {
		t.Error("an avatar image was rendered for an author who has none")
	}
}
