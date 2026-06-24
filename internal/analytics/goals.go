package analytics

// goals.go — VayuAnalytics conversion goals.
//
// A goal is a named target the operator wants visitors to reach: either firing
// a custom event ("event") or viewing a particular page ("path"). Goal results
// are computed live from the existing pageview/session tables — no extra
// per-visitor tracking and no PII. A path target ending in "*" matches by
// prefix (e.g. "/checkout*").

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// GoalKindEvent and GoalKindPath are the two supported goal types.
const (
	GoalKindEvent = "event"
	GoalKindPath  = "path"
)

// Goal is a stored goal definition.
type Goal struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Kind      string    `json:"kind"`   // "event" | "path"
	Target    string    `json:"target"` // event name, or path / path-prefix
	CreatedAt time.Time `json:"created_at"`
}

// GoalResult is a goal with its computed conversion data over a window.
type GoalResult struct {
	ID             string  `json:"id"`
	Name           string  `json:"name"`
	Kind           string  `json:"kind"`
	Target         string  `json:"target"`
	Completions    int     `json:"completions"`     // total matching events/views
	UniqueVisitors int     `json:"unique_visitors"` // distinct visitors who converted
	ConversionRate float64 `json:"conversion_rate"` // % of all visitors in window
}

// CreateGoal stores a new goal definition.
func (s *Store) CreateGoal(ctx context.Context, name, kind, target string) (string, error) {
	name = strings.TrimSpace(name)
	target = strings.TrimSpace(target)
	if name == "" || target == "" {
		return "", errors.New("name and target are required")
	}
	if kind != GoalKindEvent && kind != GoalKindPath {
		return "", errors.New("kind must be 'event' or 'path'")
	}
	if len(name) > 200 {
		name = name[:200]
	}
	if len(target) > 512 {
		target = target[:512]
	}
	id := fmt.Sprintf("g%d", time.Now().UnixNano())
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO analytics_goals(id,name,kind,target,created_at) VALUES(?,?,?,?,?)`,
		id, name, kind, target, time.Now().UTC())
	return id, err
}

// ListGoals returns all goal definitions, newest first.
func (s *Store) ListGoals(ctx context.Context) ([]Goal, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id,name,kind,target,created_at FROM analytics_goals ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []Goal{}
	for rows.Next() {
		var g Goal
		if err := rows.Scan(&g.ID, &g.Name, &g.Kind, &g.Target, &g.CreatedAt); err != nil {
			return nil, err
		}
		result = append(result, g)
	}
	return result, rows.Err()
}

// DeleteGoal removes a goal definition.
func (s *Store) DeleteGoal(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM analytics_goals WHERE id=?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errors.New("no such goal")
	}
	return nil
}

// GoalResults computes conversion data for every goal over the trailing N days.
// The conversion rate is the share of all unique visitors in the window who
// completed the goal.
func (s *Store) GoalResults(ctx context.Context, days int) ([]GoalResult, error) {
	if days <= 0 {
		days = 30
	}
	goals, err := s.ListGoals(ctx)
	if err != nil {
		return nil, err
	}
	from := time.Now().UTC().AddDate(0, 0, -(days - 1)).Format("2006-01-02")

	// Denominator: distinct visitors active in the window.
	totalVisitors := 0
	_ = s.db.QueryRowContext(ctx,
		`SELECT COUNT(DISTINCT visitor_id) FROM analytics_sessions WHERE created_at>=?`, from).
		Scan(&totalVisitors)

	results := make([]GoalResult, 0, len(goals))
	for _, g := range goals {
		gr := GoalResult{ID: g.ID, Name: g.Name, Kind: g.Kind, Target: g.Target}

		// Build the matching predicate on analytics_pageviews (aliased p), with a
		// join to sessions (aliased s) for the visitor_id used in unique counts.
		var where string
		var arg interface{}
		switch g.Kind {
		case GoalKindEvent:
			where = `p.event_type=2 AND p.event_name=?`
			arg = g.Target
		default: // path
			if strings.HasSuffix(g.Target, "*") {
				where = `p.event_type=1 AND p.url_path LIKE ?`
				arg = strings.TrimSuffix(g.Target, "*") + "%"
			} else {
				where = `p.event_type=1 AND p.url_path=?`
				arg = normalizePathExtended(g.Target)
			}
		}

		_ = s.db.QueryRowContext(ctx,
			`SELECT COUNT(1), COUNT(DISTINCT s.visitor_id)
			 FROM analytics_pageviews p JOIN analytics_sessions s ON p.session_id=s.id
			 WHERE p.created_at>=? AND `+where, from, arg).
			Scan(&gr.Completions, &gr.UniqueVisitors)

		if totalVisitors > 0 {
			gr.ConversionRate = float64(gr.UniqueVisitors) / float64(totalVisitors) * 100
		}
		results = append(results, gr)
	}
	return results, nil
}
