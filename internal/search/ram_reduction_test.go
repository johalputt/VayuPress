package search

import (
	"context"
	"encoding/json"
	"strconv"
	"testing"
)

// TestExcerptOnlyFallbackStillMatches locks in the L2a change: after replacing
// the per-doc title+tags+excerpt haystack with an excerpt-only lowercase field,
// a term that appears ONLY in the body/excerpt (not the title or tags) must
// still match, and a term that appears nowhere must still miss.
func TestExcerptOnlyFallbackStillMatches(t *testing.T) {
	SetEnabled(true)
	s := NewService(nil)
	// "kubernetes" appears only in the body, never in title or tags.
	mustIndex(t, s, "1", "Deploying at scale", "deploying-at-scale",
		"We run everything on kubernetes across three regions.", []string{"ops", "infra"})

	res, err := s.Search(context.Background(), "kubernetes", 10)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(res.Hits) != 1 || res.Hits[0].Slug != "deploying-at-scale" {
		t.Fatalf("excerpt-only term should match via the excerpt fallback, got %+v", res.Hits)
	}

	// A term present in none of title/tags/excerpt must not match.
	res, _ = s.Search(context.Background(), "nonexistentterm", 10)
	if len(res.Hits) != 0 {
		t.Errorf("absent term must not match, got %d hits", len(res.Hits))
	}
}

// TestSnapshotVersionSensitiveToContentEdits locks in the L3 change: the
// single-pass streaming hash must remain sensitive to edits in EVERY field the
// client payload carries (title, excerpt/body, tags), not just slug+date — an
// in-place edit that keeps the same id/slug/date must still bump the version so
// browsers and CDNs re-fetch the updated index.
func TestSnapshotVersionSensitiveToContentEdits(t *testing.T) {
	SetEnabled(true)

	versionAfter := func(title, content string, tags []string) string {
		s := NewService(nil)
		// Same id/slug throughout; createdAt is derived from id by the helper, so
		// only the field under test varies between calls.
		mustIndex(t, s, "1", title, "fixed-slug", content, tags)
		_, v := s.Snapshot()
		return v
	}

	base := versionAfter("Original title", "original body text", []string{"alpha"})
	if base == "" || base == "off" {
		t.Fatalf("expected a real base version, got %q", base)
	}

	if v := versionAfter("Different title", "original body text", []string{"alpha"}); v == base {
		t.Error("version must change when only the TITLE changes")
	}
	if v := versionAfter("Original title", "different body text", []string{"alpha"}); v == base {
		t.Error("version must change when only the EXCERPT/body changes")
	}
	if v := versionAfter("Original title", "original body text", []string{"beta"}); v == base {
		t.Error("version must change when only the TAGS change")
	}

	// Identical content must produce an identical version (deterministic ETag).
	if v := versionAfter("Original title", "original body text", []string{"alpha"}); v != base {
		t.Errorf("identical content must yield a stable version: %q != %q", v, base)
	}
}

// TestSnapshotCapsToRecentWindow locks in the L2b change: the client index ships
// at most clientSnapshotMax of the newest posts and reports the true total, while
// the server-side index still holds every post so /search covers the full
// archive. This is the invariant behind the modal's "search the full archive"
// escalation.
func TestSnapshotCapsToRecentWindow(t *testing.T) {
	SetEnabled(true)
	s := NewService(nil)

	n := clientSnapshotMax + 25
	for i := 0; i < n; i++ {
		id := strconv.Itoa(i)
		// createdAt = i, so a higher index is a newer post. "u<id>" is a unique
		// body token so a later search can target one specific (old) post.
		if err := s.Index(context.Background(), id, "Post "+id, "post-"+id, "body u"+id, nil, int64(i)); err != nil {
			t.Fatalf("Index(%s): %v", id, err)
		}
	}

	payload, _ := s.Snapshot()
	var idx clientIndex
	if err := json.Unmarshal(payload, &idx); err != nil {
		t.Fatalf("snapshot is not valid JSON: %v", err)
	}
	if idx.N != n {
		t.Errorf("reported total N = %d, want %d", idx.N, n)
	}
	if len(idx.Posts) != clientSnapshotMax {
		t.Fatalf("client index size = %d, want cap %d", len(idx.Posts), clientSnapshotMax)
	}
	// The window must be the NEWEST posts, sorted newest-first.
	if idx.Posts[0].U != "post-"+strconv.Itoa(n-1) {
		t.Errorf("newest post should be first in the client index, got %q", idx.Posts[0].U)
	}
	// The oldest posts must be excluded from the (capped) client index...
	for _, p := range idx.Posts {
		if p.U == "post-0" {
			t.Error("oldest post must not appear in the capped client index")
		}
	}
	// ...but the SERVER index must still hold every post so /search covers the
	// full archive.
	if c, _ := s.DocCount(context.Background()); c != n {
		t.Errorf("server index DocCount = %d, want all %d (full-archive coverage)", c, n)
	}
	res, _ := s.Search(context.Background(), "u0", 5)
	found := false
	for _, h := range res.Hits {
		if h.Slug == "post-0" {
			found = true
		}
	}
	if !found {
		t.Error("server search must still find an old post that is excluded from the capped client index")
	}
}
