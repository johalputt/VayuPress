// SPDX-License-Identifier: Apache-2.0

package analytics

import (
	"context"
	"errors"
	"strings"
	"testing"

	dbpkg "github.com/johalputt/vayupress/internal/db"
)

// A segment belongs to the site it was saved for; another site's page can
// neither list nor delete it; it keeps its filters as given.
func TestASegmentIsItsSitesOwn(t *testing.T) {
	openTrendingDB(t)
	s := New(dbpkg.DB)
	ctx := context.Background()
	f := []ExploreFilter{{Dim: "returning", Op: FilterIs, Value: "yes"}, {Dim: "landing", Op: FilterPrefix, Value: "/blog/"}}
	id, err := s.CreateSegment(ctx, "s1", " Returning readers ", f)
	if err != nil {
		t.Fatal(err)
	}
	list, _ := s.ListSegments(ctx, "s1")
	if len(list) != 1 || list[0].Name != "Returning readers" || len(list[0].Filters) != 2 || list[0].Filters[1] != f[1] {
		t.Fatalf("site one's segments = %+v", list)
	}
	for _, other := range []string{"s2", ScopeAllDomains} {
		if l, _ := s.ListSegments(ctx, other); len(l) != 0 {
			t.Errorf("%q lists site one's segment", other)
		}
		if _, err := s.SegmentByID(ctx, other, id); !errors.Is(err, ErrNoSegment) {
			t.Errorf("%q reads site one's segment: %v", other, err)
		}
		if err := s.DeleteSegment(ctx, other, id); !errors.Is(err, ErrNoSegment) {
			t.Errorf("%q deletes site one's segment: %v", other, err)
		}
	}
	if g, err := s.SegmentByID(ctx, "s1", id); err != nil || g.Name != "Returning readers" {
		t.Errorf("site one cannot read its segment: %v", err)
	}
	if err := s.DeleteSegment(ctx, "s1", id); err != nil {
		t.Errorf("site one cannot delete its segment: %v", err)
	}
}

// A segment that could never be applied is refused when saved, with the
// reason, and a site keeps at most MaxSegments.
func TestASegmentIsCheckedWhenSaved(t *testing.T) {
	openTrendingDB(t)
	s := New(dbpkg.DB)
	ctx := context.Background()
	ok := []ExploreFilter{{Dim: "device", Op: FilterIs, Value: "Mobile"}}
	for _, c := range []struct {
		name    string
		filters []ExploreFilter
		want    string
	}{
		{"", ok, "name"},
		{"a\nb", ok, "one line"},
		{strings.Repeat("x", 81), ok, "80 characters"},
		{"none", nil, "1 to 5 filters"},
		{"many", make([]ExploreFilter, ExploreMaxFilters+1), "1 to 5 filters"},
		{"bad", []ExploreFilter{{Dim: "visit_views", Op: FilterAtLeast, Value: "lots"}}, "whole number"},
		{"day", []ExploreFilter{{Dim: "day", Op: FilterIs, Value: "2026-01-01"}}, "the range sets the days"},
	} {
		if _, err := s.CreateSegment(ctx, "s1", c.name, c.filters); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%q %+v: %v, want the reason to say %q", c.name, c.filters, err, c.want)
		}
	}
	for i := 0; i < MaxSegments; i++ {
		if _, err := s.CreateSegment(ctx, "s1", "seg", ok); err != nil {
			t.Fatalf("segment %d: %v", i+1, err)
		}
	}
	if _, err := s.CreateSegment(ctx, "s1", "seg", ok); err == nil || !strings.Contains(err.Error(), "at most") {
		t.Errorf("one past the limit: %v", err)
	}
	if _, err := s.CreateSegment(ctx, "s2", "seg", ok); err != nil {
		t.Errorf("another site's first segment is refused: %v", err)
	}
}
