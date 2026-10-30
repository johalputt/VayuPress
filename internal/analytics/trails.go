// SPDX-License-Identifier: Apache-2.0

package analytics

// trails.go — the latest visits, each as the trail it left: where it came
// from, the pages it read in order, what it did, how long it stayed.
//
// A trail is one visit, never a person: the visitor hash changes every day
// (docs/ANALYTICS.md), so no two days' visits are joined, and nothing that
// names a reader is kept to show. A place is shown as finely as it is
// shared: a city only when enough of the period's visits were from it that
// a trail does not single one reader out, else the country alone.

import (
	"context"
	"database/sql"
	"strconv"
	"strings"
	"time"

	"github.com/johalputt/vayupress/internal/span"
)

// TrailCityMin is how many of the period's visits must share a city before
// a trail names it.
const TrailCityMin = 5

// TrailStep is one thing a visit did: a page read, a link out, a download,
// a custom event or a search.
type TrailStep struct {
	Kind  string // "page", "out", "download", "event", "search"
	Value string
}

// VisitTrail is one visit as it went.
type VisitTrail struct {
	Started         time.Time
	Country, City   string // City "" when too few visits shared it
	Device, Browser string
	Referrer        string
	Steps           []TrailStep
	Seconds         float64
	Converted       bool
	MoreSteps       int // steps past those kept
}

// trailSteps is how many steps of a visit a trail keeps.
const trailSteps = 12

// VisitTrailsScoped is scope's latest limit visits begun in the period, the
// readers ctx is narrowed to alone, newest first.
func (s *Store) VisitTrailsScoped(ctx context.Context, scope string, days, limit int) ([]VisitTrail, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	from, until := span.Days(ctx, s.now(), days)
	clause, sargs := s.visitScope(ctx, scope, "s", from, until)
	rows, err := s.readDB().QueryContext(ctx, `SELECT s.id, s.created_at, s.country, s.city, s.device, s.browser, s.referrer,
		COALESCE((julianday(s.last_at)-julianday(s.created_at))*86400.0,0)
		FROM analytics_sessions s WHERE s.created_at>=? AND s.created_at<? AND s.events>0`+clause+`
		ORDER BY s.created_at DESC, s.id LIMIT ?`, append(append([]any{from, until}, sargs...), limit)...)
	if err != nil {
		return nil, err
	}
	var ids []any
	trails := []VisitTrail{}
	byID := map[string]int{}
	for rows.Next() {
		var t VisitTrail
		var id string
		var started sql.NullTime
		if err := rows.Scan(&id, &started, &t.Country, &t.City, &t.Device, &t.Browser, &t.Referrer, &t.Seconds); err != nil {
			_ = rows.Close()
			return nil, err
		}
		t.Started = started.Time
		byID[id] = len(trails)
		ids = append(ids, id)
		trails = append(trails, t)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	_ = rows.Close()
	if len(ids) == 0 {
		return trails, nil
	}
	if err := s.trailSteps(ctx, ids, byID, trails); err != nil {
		return nil, err
	}
	if err := s.trailPlaces(ctx, scope, from, until, trails); err != nil {
		return nil, err
	}
	return trails, nil
}

// trailSteps fills each trail's steps from its events, in the order they
// happened.
func (s *Store) trailSteps(ctx context.Context, ids []any, byID map[string]int, trails []VisitTrail) error {
	rows, err := s.readDB().QueryContext(ctx, `SELECT session_id, event_type, url_path, event_name FROM analytics_pageviews
		WHERE session_id IN (?`+strings.Repeat(",?", len(ids)-1)+`) ORDER BY created_at, id`, ids...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id, path, name string
		var et int
		if err := rows.Scan(&id, &et, &path, &name); err != nil {
			return err
		}
		t := &trails[byID[id]]
		step := TrailStep{Kind: "page", Value: path}
		switch et {
		case 1:
		case 2:
			step = TrailStep{Kind: "event", Value: name}
			t.Converted = true
		case eventOutbound:
			step = TrailStep{Kind: "out", Value: name}
		case eventDownload:
			step = TrailStep{Kind: "download", Value: name}
		case eventSearch:
			// What was searched is the site's search report's, counted
			// over every reader; set on one visit beside its place and
			// pages, a reader looking up their own name would be named.
			step = TrailStep{Kind: "search"}
		default:
			continue
		}
		if len(t.Steps) == trailSteps {
			t.MoreSteps++
			continue
		}
		t.Steps = append(t.Steps, step)
	}
	return rows.Err()
}

// trailPlaces clears each trail's city that fewer than TrailCityMin of the
// period's visits shared. Only the trails' own cities are counted, so a busy
// site's month is never grouped city by city to name fifty.
func (s *Store) trailPlaces(ctx context.Context, scope, from, until string, trails []VisitTrail) error {
	var cities []any
	seen := map[string]bool{}
	for _, t := range trails {
		if t.City != "" && !seen[t.City] {
			seen[t.City] = true
			cities = append(cities, t.City)
		}
	}
	if len(cities) == 0 {
		return nil
	}
	clause, cargs := domainClause(scope)
	rows, err := s.readDB().QueryContext(ctx, `SELECT country, city FROM analytics_sessions WHERE created_at>=? AND created_at<? AND events>0
		AND city IN (?`+strings.Repeat(",?", len(cities)-1)+`)`+clause+`
		GROUP BY country, city HAVING COUNT(1)>=`+strconv.Itoa(TrailCityMin), append(append([]any{from, until}, cities...), cargs...)...)
	if err != nil {
		return err
	}
	defer rows.Close()
	shared := map[string]bool{}
	for rows.Next() {
		var country, city string
		if err := rows.Scan(&country, &city); err != nil {
			return err
		}
		shared[country+"|"+city] = true
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for i := range trails {
		if !shared[trails[i].Country+"|"+trails[i].City] {
			trails[i].City = ""
		}
	}
	return nil
}
