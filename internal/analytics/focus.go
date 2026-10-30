// SPDX-License-Identifier: Apache-2.0

package analytics

// focus.go — an Analytics page narrowed to some readers (package focus).
//
// A filter on the page picks visits, not events: "came from Google" keeps
// every event of every visit that arrived from Google, so the pages those
// readers went on to read are counted, not only the one they landed on. A
// filter on a visit's own fact (its referrer, place, device, landing page)
// reads its row; a filter on something a visit does (a page read, an event,
// a campaign carried) keeps the visits with such an event, and "is not"
// keeps the visits with none. The dimensions, operators and their SQL are
// the Explorer's.

import (
	"context"

	"github.com/johalputt/vayupress/internal/focus"
)

// focusVisit is the condition a visit row aliased s meets to be one of the
// visits ctx is narrowed to, with its arguments; "" when ctx is not
// narrowed.
func (s *Store) focusVisit(ctx context.Context) (string, []any) {
	fs := focus.Of(ctx)
	if len(fs) == 0 {
		return "", nil
	}
	cond := "1"
	var args []any
	for _, f := range fs {
		d, ok := findDim(f.Dim)
		if !ok || ValidFilter(f) != nil {
			// The console refuses a filter it cannot apply before it reaches
			// here; one that did keeps nothing rather than everything.
			return "0", nil
		}
		if d.visit != "" {
			col, cargs := s.dimSQL(d, true)
			c, fargs := filterCond(col, cargs, f.Op, f.Value)
			cond += " AND " + c
			args = append(args, fargs...)
			continue
		}
		col, cargs := s.dimSQL(d, false)
		op, not := f.Op, ""
		if op == FilterIsNot {
			op, not = FilterIs, "NOT "
		}
		c, fargs := filterCond(col, cargs, op, f.Value)
		cond += " AND " + not + "EXISTS(SELECT 1 FROM analytics_pageviews p WHERE p.session_id=s.id AND " + c + ")"
		args = append(args, fargs...)
	}
	return cond, args
}

// focusVisits is the visits ctx is narrowed to that began in [from, until),
// as a subquery of their ids; "" when ctx is not narrowed. A visit is looked
// for from the day before from, so one that began before midnight and went
// on into the range is not lost.
func (s *Store) focusVisits(ctx context.Context, from, until string) (string, []any) {
	cond, args := s.focusVisit(ctx)
	if cond == "" {
		return "", nil
	}
	return "SELECT s.id FROM analytics_sessions s WHERE s.created_at>=date(?,'-1 day') AND s.created_at<? AND " + cond,
		append([]any{from, until}, args...)
}

// focusEvents is the clause keeping the event rows of table alias (alias.
// before its columns, "" for none) that belong to a visit ctx is narrowed
// to; "" when it is not narrowed.
func (s *Store) focusEvents(ctx context.Context, alias, from, until string) (string, []any) {
	sub, args := s.focusVisits(ctx, from, until)
	if sub == "" {
		return "", nil
	}
	return " AND " + alias + "session_id IN (" + sub + ")", args
}

// focusVisitRows is focusEvents for the visit rows themselves.
func (s *Store) focusVisitRows(ctx context.Context, alias, from, until string) (string, []any) {
	sub, args := s.focusVisits(ctx, from, until)
	if sub == "" {
		return "", nil
	}
	return " AND " + alias + "id IN (" + sub + ")", args
}

// FocusVisitors is the clause keeping rows whose visitor column col (a
// visitor's hash, as the event log keeps it) belongs to a visitor with a
// visit ctx is narrowed to, in [from, until); "" when it is not narrowed.
// For a store that records readers but not their visits (the engagement
// beacon): a visitor is recognised for a day, so this keeps that day's
// readers who came the way the page is narrowed to.
func (s *Store) FocusVisitors(ctx context.Context, col, from, until string) (string, []any) {
	cond, args := s.focusVisit(ctx)
	if cond == "" {
		return "", nil
	}
	return " AND " + col + " IN (SELECT s.visitor_id FROM analytics_sessions s WHERE s.created_at>=date(?,'-1 day') AND s.created_at<? AND " + cond + ")",
		append([]any{from, until}, args...)
}

// eventScope is the clause keeping the event rows of scope's site, of the
// table aliased alias ("" for none), that belong to the visits ctx is
// narrowed to over [from, until): domainClause and focusEvents in one, the
// clause every reader of the event log adds.
func (s *Store) eventScope(ctx context.Context, scope, alias, from, until string) (string, []any) {
	clause, args := domainClauseAs(alias, scope)
	f, fargs := s.focusEvents(ctx, dotted(alias), from, until)
	return clause + f, append(args, fargs...)
}

// visitScope is eventScope for readers of the visit rows.
func (s *Store) visitScope(ctx context.Context, scope, alias, from, until string) (string, []any) {
	clause, args := domainClauseAs(alias, scope)
	f, fargs := s.focusVisitRows(ctx, dotted(alias), from, until)
	return clause + f, append(args, fargs...)
}

func domainClauseAs(alias, scope string) (string, []any) {
	if alias == "" {
		return domainClause(scope)
	}
	return domainClauseOn(alias, scope)
}

func dotted(alias string) string {
	if alias == "" {
		return ""
	}
	return alias + "."
}
