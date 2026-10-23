// SPDX-License-Identifier: Apache-2.0

package analytics

// segments.go — a site's saved filters. A segment is a name for up to
// ExploreMaxFilters Explorer filters, kept for one site or for the install
// (as goals are), and applied by the Explorer beside the filters of the
// moment. The filters are checked when saved and again when used: a segment
// of page filters is refused for the visit figures, with the reason.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
)

// MaxSegments bounds the segments one site, or the install, keeps.
const MaxSegments = 50

// Segment is a saved set of filters.
type Segment struct {
	ID, Name  string
	Filters   []ExploreFilter
	CreatedAt time.Time
}

// ErrNoSegment is a segment that is not the scope's: none by that id, or
// another site's.
var ErrNoSegment = errors.New("no such segment here")

// CreateSegment saves name for filters on scope's site (or the install's,
// for ScopeAllDomains), and returns its id.
func (s *Store) CreateSegment(ctx context.Context, scope, name string, filters []ExploreFilter) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || len([]rune(name)) > 80 || strings.IndexFunc(name, unicode.IsControl) >= 0 {
		return "", errors.New("give the segment a name of up to 80 characters on one line")
	}
	if len(filters) == 0 || len(filters) > ExploreMaxFilters {
		return "", fmt.Errorf("a segment is 1 to %d filters", ExploreMaxFilters)
	}
	for _, f := range filters {
		if err := ValidFilter(f); err != nil {
			return "", err
		}
	}
	site := definitionSite(scope)
	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM analytics_segments WHERE domain_id=?`, site).Scan(&n); err != nil {
		return "", err
	}
	if n >= MaxSegments {
		return "", fmt.Errorf("a site keeps at most %d segments; delete one first", MaxSegments)
	}
	body, err := json.Marshal(filters)
	if err != nil {
		return "", err
	}
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	id := "sg" + hex.EncodeToString(b)
	_, err = s.db.ExecContext(ctx, `INSERT INTO analytics_segments(id,domain_id,name,filters,created_at) VALUES(?,?,?,?,?)`,
		id, site, name, string(body), s.now().UTC().Format("2006-01-02 15:04:05"))
	return id, err
}

// ListSegments is scope's segments, oldest first.
func (s *Store) ListSegments(ctx context.Context, scope string) ([]Segment, error) {
	rows, err := s.readDB().QueryContext(ctx, `SELECT id,name,filters,created_at FROM analytics_segments WHERE domain_id=? ORDER BY created_at, id`, definitionSite(scope))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Segment
	for rows.Next() {
		var g Segment
		var body string
		if err := rows.Scan(&g.ID, &g.Name, &body, &g.CreatedAt); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(body), &g.Filters); err != nil {
			return nil, fmt.Errorf("segment %s: %w", g.ID, err)
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// SegmentByID is scope's segment id, or ErrNoSegment.
func (s *Store) SegmentByID(ctx context.Context, scope, id string) (Segment, error) {
	list, err := s.ListSegments(ctx, scope)
	if err != nil {
		return Segment{}, err
	}
	for _, g := range list {
		if g.ID == id {
			return g, nil
		}
	}
	return Segment{}, ErrNoSegment
}

// DeleteSegment removes scope's segment id; another site's is ErrNoSegment.
func (s *Store) DeleteSegment(ctx context.Context, scope, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM analytics_segments WHERE id=? AND domain_id=?`, id, definitionSite(scope))
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNoSegment
	}
	return nil
}

// SegmentPreset is a segment the Explorer offers to start from: the brief's
// usual ones, each a set of filters an operator can adjust before saving.
type SegmentPreset struct {
	Name    string
	Filters []ExploreFilter
}

// SegmentPresets are the segments offered to start from. A value the
// operator must choose (a country) is left for them to fill in.
func SegmentPresets() []SegmentPreset {
	return []SegmentPreset{
		{"Organic search", []ExploreFilter{{Dim: "visit_channel", Op: FilterIs, Value: "organic"}}},
		{"Returning the same day", []ExploreFilter{{Dim: "returning", Op: FilterIs, Value: "yes"}}},
		{"Mobile visitors", []ExploreFilter{{Dim: "device", Op: FilterIs, Value: "Mobile"}}},
		{"From one country", []ExploreFilter{{Dim: "country", Op: FilterIs, Value: ""}}},
		{"Landing on a section", []ExploreFilter{{Dim: "landing", Op: FilterPrefix, Value: "/blog/"}}},
		{"Engaged visits", []ExploreFilter{{Dim: "visit_views", Op: FilterAtLeast, Value: "3"}}},
		{"Converted", []ExploreFilter{{Dim: "converted", Op: FilterIs, Value: "yes"}}},
	}
}
