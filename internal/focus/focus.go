// SPDX-License-Identifier: Apache-2.0

// Package focus carries the readers an Analytics page is narrowed to, as the
// chosen dates are carried by package span: in the context, so every reader
// on the page honours the same filters without each of them growing a
// parameter. A filter is the Explorer's (analytics.ExploreFilter is this
// type), so the page and the Explorer have one set of dimensions and rules.
package focus

import "context"

// Filter is one condition: a dimension, an operator and a value, as the
// Explorer names them.
type Filter struct {
	Dim   string `json:"dim"`
	Op    string `json:"op"`
	Value string `json:"value"`
}

type key struct{}

// With returns ctx narrowed to fs; no filters leave ctx as it was.
func With(ctx context.Context, fs []Filter) context.Context {
	if len(fs) == 0 {
		return ctx
	}
	return context.WithValue(ctx, key{}, append([]Filter(nil), fs...))
}

// Of is the filters ctx is narrowed to, or none.
func Of(ctx context.Context) []Filter {
	fs, _ := ctx.Value(key{}).([]Filter)
	return fs
}

// Visitors gives the clause keeping the rows whose visitor column col (a
// visitor's hash, its first 32 characters as the event log keeps it)
// belongs to the readers ctx is narrowed to over [from, until), dates as
// "2006-01-02". It is set by the console, which owns the event log, for a
// store that records readers but not their visits (the engagement beacon),
// so that store needs no knowledge of how a filter is written.
type Visitors func(col, from, until string) (string, []any)

type visitorsKey struct{}

// WithVisitors returns ctx carrying v.
func WithVisitors(ctx context.Context, v Visitors) context.Context {
	return context.WithValue(ctx, visitorsKey{}, v)
}

// VisitorClause is the clause v set on ctx gives for col over [from, until),
// or "" when ctx is not narrowed. A narrowed ctx with no Visitors keeps
// nothing: readings shown under a filter they did not pass would read as the
// filtered readers'.
func VisitorClause(ctx context.Context, col, from, until string) (string, []any) {
	if len(Of(ctx)) == 0 {
		return "", nil
	}
	v, _ := ctx.Value(visitorsKey{}).(Visitors)
	if v == nil {
		return " AND 0", nil
	}
	return v(col, from, until)
}
