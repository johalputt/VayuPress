// SPDX-License-Identifier: Apache-2.0

package analytics

// searches.go — what readers search a site for, and what they do not find.
//
// A search is an event of its own type, recorded by the server as it answers
// the site's search page: the query, normalised, and how many results it
// found. Only RecordSearch makes one. The mark it sets is an unexported field
// of CollectRequest, which no beacon body can fill, so the public collect
// endpoint cannot write a search into anyone's report.
//
// A query is something a reader typed, and readers type their own names,
// addresses and numbers into search boxes. A query that looks like an email
// address or carries a long run of digits is not recorded at all; the rest
// are lowercased, trimmed and capped, so the report holds what was looked
// for, not how it was typed.

import (
	"context"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// eventSearch is a search's event_type: 1 is a page view, 2 a custom event.
const eventSearch = 3

// maxSearchQuery is the longest query kept, in characters.
const maxSearchQuery = 80

// searchMark is what RecordSearch sets on a request: the results it found.
type searchMark struct{ results int }

// NormalizeSearchQuery is q as the report keeps it, and whether it may be
// kept at all: not when empty, nor when it looks like an email address or
// carries five or more digits in a row (a phone number, an order or account
// number, a postcode with its house).
func NormalizeSearchQuery(q string) (string, bool) {
	q = strings.ToLower(strings.Join(strings.Fields(q), " "))
	if q == "" || strings.Contains(q, "@") {
		return "", false
	}
	run := 0
	for _, r := range q {
		if unicode.IsDigit(r) {
			if run++; run >= 5 {
				return "", false
			}
		} else {
			run = 0
		}
	}
	if r := []rune(q); len(r) > maxSearchQuery {
		q = string(r[:maxSearchQuery])
	}
	return q, true
}

// RecordSearch queues one search on domainID's site: req carries the page's
// host and referrer, query is what was searched, results how many it found.
// A query NormalizeSearchQuery refuses is dropped.
func (s *Store) RecordSearch(req CollectRequest, query string, results int, ip, ua, domainID string) {
	q, ok := NormalizeSearchQuery(query)
	if !ok {
		return
	}
	req.EventName = q
	req.EventData = nil
	req.search = &searchMark{results: results}
	s.CollectAsync(req, ip, ua, domainID)
}

// SearchStat is one query over a window: how often it was searched, by how
// many visitors, and how many of those searches found nothing.
type SearchStat struct {
	Query    string `json:"query"`
	Searches int    `json:"searches"`
	Visitors int    `json:"visitors"`
	Empty    int    `json:"empty"`
}

// SearchesScoped is the window's most searched queries on one site (or every
// site, for ScopeAllDomains), most searched first.
func (s *Store) SearchesScoped(ctx context.Context, scope string, days, limit int) ([]SearchStat, error) {
	if days <= 0 {
		days = 30
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	from := s.now().UTC().AddDate(0, 0, -(days - 1)).Format("2006-01-02")
	clause, carg := domainClauseOn("p", scope)
	rows, err := s.readDB().QueryContext(ctx,
		`SELECT p.event_name, COUNT(1), COUNT(DISTINCT p.visitor_id),
		        SUM(CASE WHEN d.property_value='0' THEN 1 ELSE 0 END)
		 FROM analytics_pageviews p
		 LEFT JOIN analytics_event_data d ON d.event_id=p.id AND d.property_key='results'
		 WHERE p.event_type=`+strconv.Itoa(eventSearch)+` AND p.created_at>=?`+clause+`
		 GROUP BY p.event_name ORDER BY 2 DESC, 1 LIMIT ?`,
		append(append([]any{from}, carg...), limit)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SearchStat{}
	for rows.Next() {
		var st SearchStat
		if err := rows.Scan(&st.Query, &st.Searches, &st.Visitors, &st.Empty); err != nil {
			return nil, err
		}
		out = append(out, st)
	}
	return out, rows.Err()
}

// searchResults is the results figure stored for a search event.
func searchResults(m *searchMark, now time.Time, eventID string) []any {
	return []any{eventID, "results", strconv.Itoa(m.results), now}
}
