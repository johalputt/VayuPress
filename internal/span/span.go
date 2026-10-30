// SPDX-License-Identifier: Apache-2.0

// Package span is the period a report counts: the last N days by default, or
// a range of days an operator chose. A chosen range travels in the request's
// context, so every reader a page calls (the event log's, the engagement
// store's, the server's measures) counts the same days, and none has to be
// told twice.
package span

import (
	"context"
	"time"
)

// Range is a run of whole UTC days, From to To, both counted.
type Range struct{ From, To time.Time }

type key struct{}

// Open is the end bound of a period with no end: later than any stored time,
// and later than any text a timestamp column holds that is not a date.
const Open = "9999-12-31"

// With makes every count read under ctx cover r.
func With(ctx context.Context, r Range) context.Context {
	return context.WithValue(ctx, key{}, r)
}

// Of is the range ctx carries, if any.
func Of(ctx context.Context) (Range, bool) {
	r, ok := ctx.Value(key{}).(Range)
	return r, ok
}

// Days is the period under ctx as dates ("2006-01-02"): the first day
// counted and the first day after the last. Without a chosen range it is the
// last days days up to now, today included, and has no end (Open).
func Days(ctx context.Context, now time.Time, days int) (from, until string) {
	if r, ok := Of(ctx); ok {
		return r.From.UTC().Format("2006-01-02"), r.To.UTC().AddDate(0, 0, 1).Format("2006-01-02")
	}
	return now.UTC().AddDate(0, 0, -(days - 1)).Format("2006-01-02"), Open
}

// Length is the number of days in the period under ctx: days, or the chosen
// range's.
func Length(ctx context.Context, days int) int {
	if r, ok := Of(ctx); ok {
		return int(r.To.Sub(r.From).Hours()/24) + 1
	}
	return days
}
