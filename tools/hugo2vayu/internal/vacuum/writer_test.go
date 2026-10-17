// SPDX-License-Identifier: Apache-2.0

package vacuum

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/johalputt/hugo2vayu/internal/hugoparse"
)

// articles.status defaults to 'published', so an insert that leaves it out
// publishes a draft kept with --skip-drafts=false.
func TestADraftIsStoredAsADraft(t *testing.T) {
	w, err := Open(filepath.Join(t.TempDir(), "vayupress.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	for _, d := range []hugoparse.Document{
		{Title: "Private", Slug: "private", Draft: true, HTML: "<p>x</p>", Date: time.Now()},
		{Title: "Public", Slug: "public", HTML: "<p>x</p>", Date: time.Now()},
	} {
		if _, err := w.InsertArticle(&d); err != nil {
			t.Fatal(err)
		}
	}
	for slug, want := range map[string]string{"private": "draft", "public": "published"} {
		var got string
		if err := w.db.QueryRow(`SELECT status FROM articles WHERE slug=?`, slug).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Errorf("%s stored as %q, want %q", slug, got, want)
		}
	}
}
