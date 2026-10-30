// SPDX-License-Identifier: Apache-2.0

package analytics

import (
	"context"
	"testing"
)

// Each row's visits are measured by the overview's definitions: Google's
// one visit read two pages and did not bounce, Bing's bounced, and the
// direct visit converted; a visit with no referrer is no referrer's row,
// and a narrowed page measures its readers alone.
func TestEachRowsVisitsAreMeasured(t *testing.T) {
	s := focusStore(t)
	ctx := context.Background()
	refs, err := s.VisitMeasuresScoped(ctx, "s1", "visit_referrer", 7, 10)
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]VisitMeasure{}
	for _, m := range refs {
		by[m.Key] = m
	}
	if len(by) != 2 {
		t.Fatalf("referrers = %+v, want Google and Bing alone", refs)
	}
	if g := by["www.google.com"]; g.Visits != 1 || g.PagesPerVisit() != 2 || g.BounceRate() != 0 || g.Long != 1 {
		t.Errorf("Google's visit = %+v, want one of two pages, no bounce", g)
	}
	if b := by["www.bing.com"]; b.BounceRate() != 100 || b.Long != 0 || b.AvgVisit() != 0 {
		t.Errorf("Bing's visit = %+v, want a bounce", b)
	}
	ch, err := s.VisitMeasuresScoped(ctx, "s1", "visit_channel", 7, 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range ch {
		switch m.Key {
		case "organic":
			if m.Visits != 2 || m.Visitors != 2 || m.ConversionRate() != 0 {
				t.Errorf("organic = %+v, want 2 visits, no conversion", m)
			}
		case "direct":
			if m.Visits != 1 || m.ConversionRate() != 100 {
				t.Errorf("direct = %+v, want one visit that converted", m)
			}
		default:
			t.Errorf("a channel no visit arrived by: %+v", m)
		}
	}
	if us, _ := s.VisitMeasuresScoped(narrowTo("country", FilterIs, "US"), "s1", "visit_referrer", 7, 10); len(us) != 1 || us[0].Key != "www.bing.com" {
		t.Errorf("referrers narrowed to US = %+v, want Bing alone", us)
	}
	if _, err := s.VisitMeasuresScoped(ctx, "s1", "path", 7, 10); err == nil {
		t.Error("a page is measured as if it were a visit's own")
	}
}
