// SPDX-License-Identifier: Apache-2.0

package main

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	dbpkg "github.com/johalputt/vayupress/internal/db"
)

// Every post in /llms.txt carries a one-line summary: its excerpt, or, where
// it has none (every post the API writes), the opening of its prose, as its
// meta description reads.
func TestLLMsTxtSummarisesEveryPost(t *testing.T) {
	openMigratedDB(t)
	now := time.Now()
	for _, p := range [][4]string{
		{"1", "with-excerpt", "<p>Body.</p>", "The excerpt."},
		{"2", "no-excerpt", `<svg><text>A LABEL</text></svg><blockquote><p>Opening prose of the post.</p></blockquote><p>More.</p>`, ""},
	} {
		if _, err := dbpkg.DB.Exec(`INSERT INTO articles(id,title,slug,content,excerpt,tags,status,created_at,updated_at) VALUES(?,?,?,?,?,'','published',?,?)`,
			p[0], "Post "+p[0], p[1], p[2], p[3], now, now); err != nil {
			t.Fatal(err)
		}
	}
	rec := httptest.NewRecorder()
	(&App{}).handleLLMsTxt(rec, httptest.NewRequest("GET", "/llms.txt", nil))
	body := rec.Body.String()
	for _, want := range []string{"/with-excerpt): The excerpt. (", "/no-excerpt): Opening prose of the post. More. ("} {
		if !strings.Contains(body, want) {
			t.Errorf("llms.txt lacks %q:\n%s", want, body)
		}
	}
}

// A summary is the post's prose. An imported post whose own <style> runs past
// the opening that is read (johal.in's Cilium post: its CSS was its summary)
// is read whole instead; a post that opens with a table is summarised by the
// prose after it; and a pinned post is listed first, under Start here.
func TestLLMsTxtSummariesAreProseAndPinnedPostsComeFirst(t *testing.T) {
	openMigratedDB(t)
	now := time.Now()
	css := "<style>" + strings.Repeat(".rule{color:red}\n", llmsOpeningChars/10) + "</style>"
	for _, p := range []struct {
		id, slug, content string
		featured          int
	}{
		{"1", "own-style", css + "<p>The post itself, after its styles.</p>", 0},
		{"2", "opens-with-a-table", "<table><tr><th>Dimension</th><th>A</th></tr></table><p>What the comparison found.</p>", 0},
		{"3", "pinned-post", "<p>Read this first.</p>", 1},
	} {
		if _, err := dbpkg.DB.Exec(`INSERT INTO articles(id,title,slug,content,excerpt,tags,status,featured,created_at,updated_at) VALUES(?,?,?,?,'','','published',?,?,?)`,
			p.id, "Post "+p.id, p.slug, p.content, p.featured, now, now); err != nil {
			t.Fatal(err)
		}
	}
	rec := httptest.NewRecorder()
	(&App{}).handleLLMsTxt(rec, httptest.NewRequest("GET", "/llms.txt", nil))
	body := rec.Body.String()
	for _, want := range []string{
		"/own-style): The post itself, after its styles. (",
		"/opens-with-a-table): What the comparison found. (",
		"## Start here\n\n- [Post 3](https://",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("llms.txt lacks %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, ".rule") || strings.Contains(body, "color:red") {
		t.Errorf("a post's CSS was read as its summary:\n%s", body)
	}
}
