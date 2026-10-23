// SPDX-License-Identifier: Apache-2.0

package analytics

import (
	"context"
	"testing"
	"time"

	vasession "github.com/johalputt/vayupress/internal/vayuanalytics/session"
)

// Right now is a count of readers, by the visitor definition every other
// figure uses, and a site's live panel counts that site alone. Counting
// visits instead, one person reading two of the install's sites was two
// readers on the install's panel; and the panel had no site at all.
func TestRightNowCountsReadersAndOneSiteAlone(t *testing.T) {
	s := newExtStore(t)
	s.UseVisits(vasession.NewMasterHasher([]byte("live")))
	now := time.Now().UTC()
	s.now = func() time.Time { return now }
	collect := func(who contractReader, site, path string) {
		t.Helper()
		req := CollectRequest{URL: path, Hostname: site + ".test", EventType: 1, Lang: "en"}
		if err := s.Collect(context.Background(), req, who.ip, who.ua, site); err != nil {
			t.Fatal(err)
		}
	}
	collect(r1, "s1", "/a")
	collect(r1, "s1", "/b") // the same reader, another page: still one reader
	collect(r1, "s2", "/c") // the same reader on another site: a second visit, one reader
	collect(r2, "s2", "/c")

	ctx := context.Background()
	all, err := s.RealtimeScoped(ctx, ScopeAllDomains)
	if err != nil {
		t.Fatal(err)
	}
	if all.ActiveVisitors != 2 {
		t.Errorf("the install reads %d readers right now, want 2 (three visits by two people)", all.ActiveVisitors)
	}
	one, err := s.RealtimeScoped(ctx, "s1")
	if err != nil {
		t.Fatal(err)
	}
	if one.ActiveVisitors != 1 {
		t.Errorf("site one reads %d readers right now, want 1", one.ActiveVisitors)
	}
	for _, p := range one.ActivePages {
		if p.Path == "/c" {
			t.Errorf("site one's live pages list site two's page %q", p.Path)
		}
	}
	two, _ := s.RealtimeScoped(ctx, "s2")
	if two.ActiveVisitors != 2 || len(two.ActivePages) != 1 || two.ActivePages[0].Count != 2 {
		t.Errorf("site two: %d readers, pages %+v; want 2 readers on /c", two.ActiveVisitors, two.ActivePages)
	}
}
