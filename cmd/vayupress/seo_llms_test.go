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
