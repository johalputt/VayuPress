// SPDX-License-Identifier: Apache-2.0

package vayuflow

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// A five-field cron expression: minute hour day-of-month month day-of-week.
//
// Written here rather than taken as a dependency. The grammar is small and
// closed, the evaluation has to be total for the same reason the condition set
// does, and adding a module for ~120 lines of well-understood parsing would put
// a third-party update path under the one subsystem whose whole claim is that
// nothing runs that was not authorised in advance.
//
// Evaluation is membership in five precomputed bitsets — no searching, no
// iteration over candidate times, no way for an expression to be expensive.
type Schedule struct {
	minute  [60]bool
	hour    [24]bool
	dom     [32]bool // 1..31
	month   [13]bool // 1..12
	dow     [7]bool  // 0..6, Sunday = 0
	domStar bool
	dowStar bool
	expr    string
}

// String returns the expression the schedule was parsed from.
func (s Schedule) String() string { return s.expr }

// ParseCron parses a five-field expression. Supported per field: `*`, a number,
// a range `a-b`, a list `a,b,c`, and a step `*/n` or `a-b/n`.
//
// Deliberately NOT supported: names (JAN, MON), `@daily` shorthands, seconds,
// and `?`. Each is a small convenience that widens the grammar, and a wider
// grammar in this position is more surface for an expression to mean something
// other than what the operator read.
func ParseCron(expr string) (Schedule, error) {
	s := Schedule{expr: strings.TrimSpace(expr)}
	fields := strings.Fields(s.expr)
	if len(fields) != 5 {
		return Schedule{}, fmt.Errorf("vayuflow: cron needs exactly 5 fields "+
			"(minute hour day-of-month month day-of-week), got %d in %q", len(fields), expr)
	}
	type spec struct {
		name     string
		min, max int
		set      func(int)
	}
	specs := []spec{
		{"minute", 0, 59, func(v int) { s.minute[v] = true }},
		{"hour", 0, 23, func(v int) { s.hour[v] = true }},
		{"day-of-month", 1, 31, func(v int) { s.dom[v] = true }},
		{"month", 1, 12, func(v int) { s.month[v] = true }},
		// 7 is accepted as Sunday, the near-universal cron extension, and folded
		// onto 0 so the bitset stays a single representation.
		{"day-of-week", 0, 7, func(v int) { s.dow[v%7] = true }},
	}
	for i, sp := range specs {
		if err := parseField(fields[i], sp.min, sp.max, sp.set); err != nil {
			return Schedule{}, fmt.Errorf("vayuflow: cron %s field %q: %w", sp.name, fields[i], err)
		}
	}
	s.domStar = fields[2] == "*"
	s.dowStar = fields[4] == "*"
	return s, nil
}

func parseField(f string, min, max int, set func(int)) error {
	if f == "" {
		return fmt.Errorf("empty")
	}
	for _, part := range strings.Split(f, ",") {
		step := 1
		if i := strings.Index(part, "/"); i >= 0 {
			n, err := strconv.Atoi(part[i+1:])
			if err != nil || n <= 0 {
				return fmt.Errorf("step must be a positive number, got %q", part[i+1:])
			}
			// A step wider than the field is not an error but it is almost
			// certainly a mistake, and it silently means "just the first value".
			if n > max-min+1 {
				return fmt.Errorf("step %d is wider than the field's range %d-%d", n, min, max)
			}
			step = n
			part = part[:i]
		}
		lo, hi := min, max
		switch {
		case part == "*":
			// full range
		case strings.Contains(part, "-"):
			bits := strings.SplitN(part, "-", 2)
			var err error
			if lo, err = strconv.Atoi(bits[0]); err != nil {
				return fmt.Errorf("range start %q is not a number", bits[0])
			}
			if hi, err = strconv.Atoi(bits[1]); err != nil {
				return fmt.Errorf("range end %q is not a number", bits[1])
			}
			if lo > hi {
				return fmt.Errorf("range %d-%d runs backwards", lo, hi)
			}
		default:
			v, err := strconv.Atoi(part)
			if err != nil {
				return fmt.Errorf("%q is not a number", part)
			}
			lo, hi = v, v
		}
		if lo < min || hi > max {
			return fmt.Errorf("value out of range %d-%d", min, max)
		}
		for v := lo; v <= hi; v += step {
			set(v)
		}
	}
	return nil
}

// Matches reports whether t falls on this schedule, to the minute.
//
// The day-of-month / day-of-week rule follows standard cron and is the one
// piece of this grammar that surprises people, so it is stated: when BOTH are
// restricted the match is an OR, not an AND. "0 0 1 * 1" fires on the 1st of
// the month AND on every Monday. Implementing it as an AND would silently make
// such an expression fire almost never, which is the kind of bug an operator
// discovers by a digest that did not arrive.
func (s Schedule) Matches(t time.Time) bool {
	if !s.minute[t.Minute()] || !s.hour[t.Hour()] || !s.month[int(t.Month())] {
		return false
	}
	domOK, dowOK := s.dom[t.Day()], s.dow[int(t.Weekday())]
	switch {
	case s.domStar && s.dowStar:
		return true
	case s.domStar:
		return dowOK
	case s.dowStar:
		return domOK
	default:
		return domOK || dowOK
	}
}

// FireIdentity is the idempotency identity for one scheduled firing: the minute
// it fired, in UTC.
//
// This is what makes a schedule safe without any locking. Two ticks in the same
// minute — a slow drain, an overlapping tick, two processes — derive the same
// identity, so the second collides on the run table's UNIQUE index. The guard
// against double-firing is the same guard as for redelivery, rather than a
// second mechanism that could disagree with it.
func FireIdentity(t time.Time) string { return t.UTC().Format("2006-01-02T15:04Z") }
