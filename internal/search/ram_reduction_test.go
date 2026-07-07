package search

import (
	"context"
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
