// SPDX-License-Identifier: Apache-2.0

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

	"github.com/johalputt/vayupress/internal/span"
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

// definitionsAll is the site a definition (goal, funnel) has when it counts
// every site's traffic: the install's own, and every definition made before
// definitions had a site (migration 114).
const definitionsAll = "*"

// definitionSite is the domain_id a definition is stored under for scope.
func definitionSite(scope string) string {
	if scope == ScopeAllDomains {
		return definitionsAll
	}
	return scope
}

// CreateGoal stores a new install-wide goal.
func (s *Store) CreateGoal(ctx context.Context, name, kind, target string) (string, error) {
	return s.CreateGoalScoped(ctx, ScopeAllDomains, name, kind, target)
}

// CreateGoalScoped stores a new goal for one site (or the install, for
// ScopeAllDomains). A site's goal counts only that site's traffic.
func (s *Store) CreateGoalScoped(ctx context.Context, scope, name, kind, target string) (string, error) {
	name = strings.TrimSpace(name)
	target = strings.TrimSpace(target)
	if name == "" || target == "" {
		return "", errors.New("name and target are required")
	}
	if kind != GoalKindEvent && kind != GoalKindPath {
		return "", errors.New("kind must be 'event' or 'path'")
	}
	name, target = trunc(name, 200), trunc(target, 512)
	id := fmt.Sprintf("g%d", time.Now().UnixNano())
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO analytics_goals(id,name,kind,target,created_at,domain_id) VALUES(?,?,?,?,?,?)`,
		id, name, kind, target, time.Now().UTC(), definitionSite(scope))
	return id, err
}

// ListGoals returns the install-wide goals, newest first.
func (s *Store) ListGoals(ctx context.Context) ([]Goal, error) {
	return s.ListGoalsScoped(ctx, ScopeAllDomains)
}

// ListGoalsScoped returns one site's goals (or the install's), newest first.
func (s *Store) ListGoalsScoped(ctx context.Context, scope string) ([]Goal, error) {
	rows, err := s.readDB().QueryContext(ctx,
		`SELECT id,name,kind,target,created_at FROM analytics_goals WHERE domain_id=? ORDER BY created_at DESC`, definitionSite(scope))
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

// DeleteGoal removes an install-wide goal.
func (s *Store) DeleteGoal(ctx context.Context, id string) error {
	return s.DeleteGoalScoped(ctx, ScopeAllDomains, id)
}

// DeleteGoalScoped removes one of scope's goals. Another site's goal, or the
// install's, is "no such goal" from here: a site's page can only remove what
// it shows.
func (s *Store) DeleteGoalScoped(ctx context.Context, scope, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM analytics_goals WHERE id=? AND domain_id=?`, id, definitionSite(scope))
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
	return s.GoalResultsScoped(ctx, ScopeAllDomains, days)
}

// GoalResultsScoped is GoalResults for one site's goals over that site's
// traffic (or the install's goals over every site's).
func (s *Store) GoalResultsScoped(ctx context.Context, scope string, days int) ([]GoalResult, error) {
	return s.GoalResultsOver(ctx, scope, scope, days)
}

// GoalResultsOver is the goals defined for defs counted over scope's traffic.
// The install's own goals (defs ScopeAllDomains) are counted over the primary
// site's visits on its page, and over every site's on the install's.
func (s *Store) GoalResultsOver(ctx context.Context, defs, scope string, days int) ([]GoalResult, error) {
	if days <= 0 {
		days = 30
	}
	goals, err := s.ListGoalsScoped(ctx, defs)
	if err != nil {
		return nil, err
	}
	from, until := span.Days(ctx, time.Now(), days)
	clause, carg := domainClause(scope)
	pclause, _ := domainClauseOn("p", scope)

	// Denominator: distinct visitors active in the window.
	totalVisitors := 0
	_ = s.readDB().QueryRowContext(ctx,
		`SELECT COUNT(DISTINCT +visitor_id) FROM analytics_pageviews WHERE created_at>=? AND created_at<?`+clause, append([]any{from, until}, carg...)...).
		Scan(&totalVisitors)

	results := make([]GoalResult, 0, len(goals))
	for _, g := range goals {
		gr := GoalResult{ID: g.ID, Name: g.Name, Kind: g.Kind, Target: g.Target}

		// The matching predicate on analytics_pageviews (aliased p), whose rows
		// carry their visitor (migration 113): the conversion rate's two sides
		// are counted from the same rows.
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
				arg = NormalizePath(g.Target)
			}
		}

		_ = s.readDB().QueryRowContext(ctx,
			`SELECT COUNT(1), COUNT(DISTINCT p.visitor_id)
			 FROM analytics_pageviews p
			 WHERE p.created_at>=? AND p.created_at<? AND `+where+pclause, append([]any{from, until, arg}, carg...)...).
			Scan(&gr.Completions, &gr.UniqueVisitors)

		if totalVisitors > 0 {
			gr.ConversionRate = float64(gr.UniqueVisitors) / float64(totalVisitors) * 100
		}
		results = append(results, gr)
	}
	return results, nil
}
