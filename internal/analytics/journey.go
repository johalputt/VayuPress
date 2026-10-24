// SPDX-License-Identifier: Apache-2.0

package analytics

// journey.go — VayuAnalytics visitor journey / path-flow analysis.
//
// PathFlows reconstructs how visitors move between pages by walking each
// session's pageviews in time order and counting consecutive (from -> to)
// transitions. Two synthetic markers bound each session: "(entry)" precedes the
// first page and "(exit)" follows the last, so entry and drop-off pages are
// visible.
//
// Privacy: this reads only the existing pageview rows (path + session_id +
// timestamp). No PII is involved, and the scan is bounded so it stays cheap even
// with hundreds of thousands of pageviews.

import (
	"context"
	"sort"
	"time"
)

// PathFlow is a single (from -> to) transition with its occurrence count.
type PathFlow struct {
	From  string `json:"from"`
	To    string `json:"to"`
	Count int    `json:"count"`
}

// entryMarker and exitMarker bound a session's path in the flow graph.
const (
	entryMarker = "(entry)"
	exitMarker  = "(exit)"
)

// maxJourneyRows bounds how many pageview rows a single PathFlows call scans, so
// the in-memory walk stays cheap on very large datasets.
const maxJourneyRows = 100000

// PathFlows returns the most common page-to-page transitions over the trailing
// N days, capped to `limit` rows (highest count first), every site's together.
func (s *Store) PathFlows(ctx context.Context, days, limit int) ([]PathFlow, error) {
	return s.PathFlowsScoped(ctx, ScopeAllDomains, days, limit)
}

// PathFlowsScoped is PathFlows for one site's visits, or every site's for
// ScopeAllDomains. A visit is one site's (migration 113), so a site's paths
// never run into another's.
func (s *Store) PathFlowsScoped(ctx context.Context, scope string, days, limit int) ([]PathFlow, error) {
	if days <= 0 {
		days = 14
	}
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	from := time.Now().UTC().AddDate(0, 0, -(days - 1)).Format("2006-01-02")

	// Ordered by session then time so consecutive rows form each visitor's path.
	clause, carg := domainClause(scope)
	rows, err := s.readDB().QueryContext(ctx,
		`SELECT session_id, url_path FROM analytics_pageviews
		 WHERE created_at>=? AND event_type=1`+clause+`
		 ORDER BY session_id, created_at, id
		 LIMIT ?`, append(append([]any{from}, carg...), maxJourneyRows)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	counts := map[string]map[string]int{}
	bump := func(f, t string) {
		m := counts[f]
		if m == nil {
			m = map[string]int{}
			counts[f] = m
		}
		m[t]++
	}

	curSession := ""
	prev := ""
	for rows.Next() {
		var sid, path string
		if err := rows.Scan(&sid, &path); err != nil {
			return nil, err
		}
		if sid != curSession {
			// Close out the previous session's path with an exit marker.
			if prev != "" {
				bump(prev, exitMarker)
			}
			curSession = sid
			bump(entryMarker, path)
		} else {
			bump(prev, path)
		}
		prev = path
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if prev != "" {
		bump(prev, exitMarker)
	}

	flows := make([]PathFlow, 0, len(counts))
	for f, tos := range counts {
		for t, c := range tos {
			flows = append(flows, PathFlow{From: f, To: t, Count: c})
		}
	}
	sort.Slice(flows, func(i, j int) bool {
		if flows[i].Count != flows[j].Count {
			return flows[i].Count > flows[j].Count
		}
		if flows[i].From != flows[j].From {
			return flows[i].From < flows[j].From
		}
		return flows[i].To < flows[j].To
	})
	if len(flows) > limit {
		flows = flows[:limit]
	}
	return flows, nil
}

// BetweenPages is the flows from one page to another, without the visits'
// entries and exits (the landing and exit pages, which are reported as such).
func BetweenPages(flows []PathFlow) []PathFlow {
	out := make([]PathFlow, 0, len(flows))
	for _, f := range flows {
		if f.From != entryMarker && f.To != exitMarker {
			out = append(out, f)
		}
	}
	return out
}
