// SPDX-License-Identifier: Apache-2.0

package analytics

// search_followups.go — what a reader did next after searching.
//
// A search is followed by the visit's next event that says what came of it:
// a page view of another page (the reader opened something, most often a
// result), or another search (the reader refined the query). The results
// page's own view is not one: the search is recorded as the server answers
// that page, and the page's script then counts it as a view of the same path.
// A search with neither after it in its visit is one the reader left.
//
// A page opened next is not always a result, since the reader may have used
// the menu instead, so the figure is named for what it measures: searches
// followed by a page.

import (
	"context"
	"sort"
	"strconv"

	"github.com/johalputt/vayupress/internal/span"
)

// SearchRefinement is one query searched again as another, and how often.
type SearchRefinement struct {
	From  string `json:"from"`
	To    string `json:"to"`
	Count int    `json:"count"`
}

// SearchFollowUps is what came of the window's searches.
type SearchFollowUps struct {
	Searches int `json:"searches"`
	// Opened counts the searches followed by a view of another page;
	// Refined, those followed by another search first.
	Opened  int `json:"opened"`
	Refined int `json:"refined"`
	// OpenedByQuery is Opened for each query.
	OpenedByQuery map[string]int `json:"opened_by_query"`
	// Pages are the pages opened after a search, most opened first.
	Pages []AudienceStat `json:"pages"`
	// Refinements are the queries searched again as another, most first.
	Refinements []SearchRefinement `json:"refinements"`
}

// searchFollowUpSQL reads each search with what followed it in its visit:
// the next view of another page or the next search, whichever came first.
// The search is found by the time index; what followed, by the visit's own
// events (idx_apv_session), so the cost is the window's searches.
var searchFollowUpSQL = `SELECT s.event_name,
	COALESCE((SELECT CASE WHEN n.event_type=` + strconv.Itoa(eventSearch) + ` THEN 's' || n.event_name ELSE 'p' || n.url_path END
		FROM analytics_pageviews n
		WHERE n.session_id=s.session_id AND (n.created_at>s.created_at OR (n.created_at=s.created_at AND n.id>s.id))
		  AND (n.event_type=` + strconv.Itoa(eventSearch) + ` OR (n.event_type=1 AND n.url_path<>s.url_path))
		ORDER BY n.created_at, n.id LIMIT 1),'')
	FROM analytics_pageviews s WHERE s.event_type=` + strconv.Itoa(eventSearch) + ` AND s.created_at>=? AND s.created_at<?`

// SearchFollowUpsScoped is what came of one site's searches over the window
// (every site's, for ScopeAllDomains); the lists hold the limit largest.
func (s *Store) SearchFollowUpsScoped(ctx context.Context, scope string, days, limit int) (*SearchFollowUps, error) {
	if days <= 0 {
		days = 30
	}
	if limit <= 0 || limit > 100 {
		limit = 10
	}
	from, until := span.Days(ctx, s.now(), days)
	clause, carg := s.eventScope(ctx, scope, "s", from, until)
	rows, err := s.readDB().QueryContext(ctx, searchFollowUpSQL+clause, append([]any{from, until}, carg...)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := &SearchFollowUps{OpenedByQuery: map[string]int{}}
	pages := map[string]int{}
	refined := map[[2]string]int{}
	for rows.Next() {
		var q, next string
		if err := rows.Scan(&q, &next); err != nil {
			return nil, err
		}
		out.Searches++
		switch {
		case next == "":
		case next[0] == 'p':
			out.Opened++
			out.OpenedByQuery[q]++
			pages[next[1:]]++
		case next[0] == 's':
			out.Refined++
			refined[[2]string{q, next[1:]}]++
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for p, n := range pages {
		out.Pages = append(out.Pages, AudienceStat{Label: p, Count: n})
	}
	sort.Slice(out.Pages, func(i, j int) bool {
		if out.Pages[i].Count != out.Pages[j].Count {
			return out.Pages[i].Count > out.Pages[j].Count
		}
		return out.Pages[i].Label < out.Pages[j].Label
	})
	if len(out.Pages) > limit {
		out.Pages = out.Pages[:limit]
	}
	for k, n := range refined {
		out.Refinements = append(out.Refinements, SearchRefinement{From: k[0], To: k[1], Count: n})
	}
	sort.Slice(out.Refinements, func(i, j int) bool {
		a, b := out.Refinements[i], out.Refinements[j]
		if a.Count != b.Count {
			return a.Count > b.Count
		}
		return a.From+"\x00"+a.To < b.From+"\x00"+b.To
	})
	if len(out.Refinements) > limit {
		out.Refinements = out.Refinements[:limit]
	}
	return out, nil
}
