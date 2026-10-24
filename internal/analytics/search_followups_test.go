// SPDX-License-Identifier: Apache-2.0

package analytics

import (
	"context"
	"strings"
	"testing"
	"time"
)

// One reader's visit, by hand: "go" then a post opened; "rust" searched
// again as "rust lang", which opened another; "zig", then nothing. The
// results page's own view, after each search, is not what followed it.
func TestWhatFollowedEachSearch(t *testing.T) {
	s := newExtStore(t)
	ctx := context.Background()
	at := time.Now().UTC().Truncate(time.Hour)
	ev := func(sec int, site, path, query string) {
		t.Helper()
		s.now = func() time.Time { return at.Add(time.Duration(sec) * time.Second) }
		req := CollectRequest{URL: path, Hostname: "site.test", EventType: 1}
		if query != "" {
			req.EventName, req.search = query, &searchMark{results: 3}
		}
		if err := s.Collect(ctx, req, "198.51.100.4", "Mozilla/5.0 Firefox/131.0", site); err != nil {
			t.Fatal(err)
		}
	}
	ev(0, "s1", "/search", "go")
	ev(1, "s1", "/search", "")
	ev(10, "s1", "/post-a", "")
	ev(60, "s1", "/search", "rust")
	ev(61, "s1", "/search", "")
	ev(65, "s1", "/search", "rust lang")
	ev(66, "s1", "/search", "")
	ev(80, "s1", "/post-b", "")
	ev(120, "s1", "/search", "zig")
	ev(121, "s1", "/search", "")
	ev(130, "s2", "/search", "elsewhere")
	ev(135, "s2", "/post-z", "")
	s.now = time.Now
	got, err := s.SearchFollowUpsScoped(ctx, "s1", 1, 10)
	if err != nil {
		t.Fatal(err)
	}
	if got.Searches != 4 || got.Opened != 2 || got.Refined != 1 {
		t.Fatalf("searches %d, opened %d, refined %d; want 4, 2, 1", got.Searches, got.Opened, got.Refined)
	}
	if len(got.OpenedByQuery) != 2 || got.OpenedByQuery["go"] != 1 || got.OpenedByQuery["rust lang"] != 1 {
		t.Errorf("opened by query = %v, want go 1 and rust lang 1", got.OpenedByQuery)
	}
	if len(got.Pages) != 2 || got.Pages[0] != (AudienceStat{Label: "/post-a", Count: 1}) || got.Pages[1] != (AudienceStat{Label: "/post-b", Count: 1}) {
		t.Errorf("pages opened after a search = %+v", got.Pages)
	}
	if len(got.Refinements) != 1 || got.Refinements[0] != (SearchRefinement{From: "rust", To: "rust lang", Count: 1}) {
		t.Errorf("refinements = %+v, want rust to rust lang", got.Refinements)
	}
	all, _ := s.SearchFollowUpsScoped(ctx, ScopeAllDomains, 1, 10)
	if all.Searches != 5 || all.Opened != 3 {
		t.Errorf("every site: %d searches, %d opened; want 5 and 3", all.Searches, all.Opened)
	}
}

// What follows a search is after it: an event at the same second but
// recorded before it is not; one recorded after it is. The lists keep the
// limit largest.
func TestWhatFollowsIsAfterAndListsAreBounded(t *testing.T) {
	s := newExtStore(t)
	ctx := context.Background()
	at := time.Now().UTC().Truncate(time.Hour).Format("2006-01-02 15:04:05")
	for _, q := range []string{
		// Visit v1: a view of /before at the search's second, recorded
		// first, then the search, then /after at the same second.
		`INSERT INTO analytics_pageviews(id,session_id,url_path,event_type,event_name,domain_id,created_at,visitor_id) VALUES
			('e1','v1','/before',1,'','s1',?,'r'),('e2','v1','/search',3,'one','s1',?,'r'),('e3','v1','/after',1,'','s1',?,'r')`,
		// Visits v2 and v3: two different refinements.
		`INSERT INTO analytics_pageviews(id,session_id,url_path,event_type,event_name,domain_id,created_at,visitor_id) VALUES
			('f1','v2','/search',3,'a','s1',?,'r2'),('f2','v2','/search',3,'b','s1',?,'r2'),
			('g1','v3','/search',3,'c','s1',?,'r3'),('g2','v3','/search',3,'d','s1',?,'r3')`,
	} {
		args := make([]any, strings.Count(q, "?"))
		for i := range args {
			args[i] = at
		}
		if _, err := s.db.Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.SearchFollowUpsScoped(ctx, "s1", 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if got.Opened != 1 || len(got.Pages) != 1 || got.Pages[0].Label != "/after" {
		t.Errorf("what followed the search = %+v, want /after alone", got.Pages)
	}
	if got.Refined != 2 || len(got.Refinements) != 1 {
		t.Errorf("refinements = %d, listed %+v; want 2 counted and 1 listed", got.Refined, got.Refinements)
	}
}
