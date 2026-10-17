// SPDX-License-Identifier: Apache-2.0

package vacuum

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

// articles.status defaults to 'published', so an insert that leaves it out
// publishes every draft a --status all or draft run imports.
func TestADraftIsStoredAsADraft(t *testing.T) {
	w, err := Open(filepath.Join(t.TempDir(), "vayupress.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	now := time.Now()
	for _, a := range []Article{
		{Title: "Private", Slug: "private", Content: "<p>x</p>", Draft: true, CreatedAt: now, UpdatedAt: now},
		{Title: "Public", Slug: "public", Content: "<p>x</p>", CreatedAt: now, UpdatedAt: now},
	} {
		if _, err := w.Insert(context.Background(), a); err != nil {
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
