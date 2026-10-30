// SPDX-License-Identifier: Apache-2.0

package analytics

// measures.go — what the visits behind each row of a list did.
//
// A list ranks by how many: a referrer that sent 300 readers who left at
// once is above one that sent 10 who read five pages each, and the list
// alone cannot say which matters. VisitMeasuresScoped counts, for each value of a
// visit's own dimension, its visits and visitors and how those visits went:
// pages a visit, bounce, average visit and conversion, by the same
// definitions as the overview (docs/ANALYTICS.md), in one pass over the
// visit rows of the range.

import (
	"context"
	"fmt"
	"time"

	"github.com/johalputt/vayupress/internal/span"
)

// VisitMeasure is one value of a dimension and what its visits did.
type VisitMeasure struct {
	Key       string
	Visits    int
	Visitors  int
	Views     int     // page views in the visits
	Bounces   int     // visits of one page view and nothing else
	Long      int     // visits of more than one event, which have a length
	LongSecs  float64 // their lengths added up
	Converted int     // visits with a custom event
}

// PagesPerVisit is the page views a visit, over its visits.
func (m VisitMeasure) PagesPerVisit() float64 { return ratio(float64(m.Views), m.Visits) }

// BounceRate is the share of its visits that bounced, in percent.
func (m VisitMeasure) BounceRate() float64 { return 100 * ratio(float64(m.Bounces), m.Visits) }

// AvgVisit is the average length, in seconds, of its visits of more than
// one event, as the overview's average visit.
func (m VisitMeasure) AvgVisit() float64 { return ratio(m.LongSecs, m.Long) }

// ConversionRate is the share of its visits with a custom event, in percent.
func (m VisitMeasure) ConversionRate() float64 { return 100 * ratio(float64(m.Converted), m.Visits) }

func ratio(n float64, d int) float64 {
	if d == 0 {
		return 0
	}
	return n / float64(d)
}

// VisitMeasuresScoped is what the visits of each value of dim did, for scope's
// visits begun in the period, the readers ctx is narrowed to alone, the
// limit values with the most visits first. dim is one of a visit's own
// dimensions (the Explorer's, with a visit column); a value that is empty
// is left out, and so is a referrer that is one of the install's own hosts.
func (s *Store) VisitMeasuresScoped(ctx context.Context, scope, dim string, days, limit int) ([]VisitMeasure, error) {
	d, ok := findDim(dim)
	if !ok || d.visit == "" || d.kind != dimWords {
		return nil, fmt.Errorf("%q is not a visit's own dimension", dim)
	}
	if limit <= 0 || limit > 200 {
		limit = 25
	}
	from, until := span.Days(ctx, time.Now(), days)
	col, cargs := s.dimSQL(d, true)
	clause, sargs := s.visitScope(ctx, scope, "s", from, until)
	notSelf, selfArgs := "", []any(nil)
	if dim == "visit_referrer" {
		notSelf, selfArgs = s.selfHostExclusion("s.referrer")
	}
	args := append(append(append(append(append([]any(nil), cargs...), from, until), sargs...), selfArgs...), limit)
	rows, err := s.readDB().QueryContext(ctx, `SELECT +(`+col+`) AS k, COUNT(1), COUNT(DISTINCT s.visitor_id), COALESCE(SUM(s.views),0),
		COALESCE(SUM(s.events=1 AND s.views=1),0), COALESCE(SUM(s.events>1),0),
		COALESCE(SUM(CASE WHEN s.events>1 THEN (julianday(s.last_at)-julianday(s.created_at))*86400.0 END),0),
		COALESCE(SUM(EXISTS(SELECT 1 FROM analytics_pageviews c WHERE c.session_id=s.id AND c.event_type=2)),0)
		FROM analytics_sessions s WHERE s.created_at>=? AND s.created_at<? AND s.events>0`+clause+notSelf+`
		GROUP BY k HAVING k IS NOT NULL AND k<>'' ORDER BY 2 DESC, 1 LIMIT ?`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []VisitMeasure{}
	for rows.Next() {
		var m VisitMeasure
		if err := rows.Scan(&m.Key, &m.Visits, &m.Visitors, &m.Views, &m.Bounces, &m.Long, &m.LongSecs, &m.Converted); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// CityPoint is a city readers were in over a period, where the city table
// placed it (migration 123), for the map.
type CityPoint struct {
	City, Country string
	Lat, Lon      float64
	Visitors      int
}

// CityPointsScoped is the placed cities of scope's visits begun in the
// period, the readers ctx is narrowed to alone, the limit with the most
// visitors first. A visit the table did not place is on no city.
func (s *Store) CityPointsScoped(ctx context.Context, scope string, days, limit int) ([]CityPoint, error) {
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	from, until := span.Days(ctx, time.Now(), days)
	clause, sargs := s.visitScope(ctx, scope, "s", from, until)
	rows, err := s.readDB().QueryContext(ctx, `SELECT s.city, s.country, AVG(s.lat), AVG(s.lon), COUNT(DISTINCT s.visitor_id)
		FROM analytics_sessions s WHERE s.created_at>=? AND s.created_at<? AND s.events>0 AND s.lat IS NOT NULL AND s.lon IS NOT NULL AND s.city<>''`+clause+`
		GROUP BY s.city, s.country ORDER BY 5 DESC, 1 LIMIT ?`, append(append([]any{from, until}, sargs...), limit)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []CityPoint{}
	for rows.Next() {
		var p CityPoint
		if err := rows.Scan(&p.City, &p.Country, &p.Lat, &p.Lon, &p.Visitors); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
