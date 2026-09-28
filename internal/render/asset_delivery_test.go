// SPDX-License-Identifier: Apache-2.0

package render

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
	"time"

	"github.com/johalputt/vayupress/internal/db"
	"github.com/microcosm-cc/bluemonday"
)

// TestChromaStylesheetOnlyWhereCodeIs — every article linked chroma.css, and
// the stylesheet blocked the first paint of pages with no code on them.
func TestChromaStylesheetOnlyWhereCodeIs(t *testing.T) {
	if got := ChromaCSSLink("<p>No code here.</p>"); got != "" {
		t.Errorf("an article without code links %s", got)
	}
	got := string(ChromaCSSLink(`<pre class="chroma"><code>x</code></pre>`))
	sum := sha256.Sum256([]byte(ChromaCSSBody()))
	want := `href="/static/chroma.css?v=` + hex.EncodeToString(sum[:4]) + `"`
	if !strings.Contains(got, want) {
		t.Errorf("an article with code links %s, want the stylesheet versioned by its content (%s)", got, want)
	}

	// Through the article renderer, which decides from the highlighted HTML.
	policy = bluemonday.UGCPolicy()
	for _, c := range []struct {
		content string
		link    bool
	}{
		{"<p>Prose only.</p>", false},
		{"<pre><code class=\"language-go\">package main</code></pre>", true},
	} {
		out, err := RenderArticleWithLayout(db.Article{Title: "T", Slug: "t", Content: c.content, CreatedAt: time.Now(), UpdatedAt: time.Now()}, ArticleLayoutDefault, nil)
		if err != nil {
			t.Fatal(err)
		}
		if has := strings.Contains(out, "/static/chroma.css"); has != c.link {
			t.Errorf("article %q links chroma.css = %v, want %v", c.content, has, c.link)
		}
	}
}

// TestHighContrastSheetLivesInsideItsLinkMedia — the link carries
// media="(prefers-contrast: more), (forced-colors: active)" so the sheet stops
// blocking everyone else's first paint. That is only harmless while every rule
// in it is inside one of those two queries: a rule outside them would stop
// applying to readers whose browser matches neither.
func TestHighContrastSheetLivesInsideItsLinkMedia(t *testing.T) {
	link := string(HighContrastCSSLink())
	if !strings.Contains(link, `media="(prefers-contrast: more), (forced-colors: active)"`) {
		t.Fatalf("link %s lost its media queries", link)
	}
	css := string(MinifyCSS([]byte(hcCSSMin)))
	for len(css) > 0 {
		var ok bool
		for _, q := range []string{"@media(prefers-contrast:more){", "@media(forced-colors:active){"} {
			if strings.HasPrefix(css, q) {
				css, ok = css[len(q):], true
				break
			}
		}
		if !ok {
			t.Fatalf("high-contrast.css has a rule outside its two media queries: %.80s", css)
		}
		depth := 1
		i := 0
		for ; i < len(css) && depth > 0; i++ {
			switch css[i] {
			case '{':
				depth++
			case '}':
				depth--
			}
		}
		css = strings.TrimSpace(css[i:])
	}
}
