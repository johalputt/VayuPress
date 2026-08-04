// SPDX-License-Identifier: Apache-2.0

package vayuflow

import (
	"testing"
	"time"
)

func mustCron(t *testing.T, expr string) Schedule {
	t.Helper()
	s, err := ParseCron(expr)
	if err != nil {
		t.Fatalf("ParseCron(%q): %v", expr, err)
	}
	return s
}

func at(t *testing.T, s string) time.Time {
	t.Helper()
	v, err := time.Parse("2006-01-02 15:04", s)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestCronMatchesTheMinutesItShould(t *testing.T) {
	for _, tc := range []struct {
		expr, when string
		want       bool
	}{
		{"* * * * *", "2026-08-04 09:17", true},
		{"0 9 * * *", "2026-08-04 09:00", true},
		{"0 9 * * *", "2026-08-04 09:01", false},
		{"*/15 * * * *", "2026-08-04 09:30", true},
		{"*/15 * * * *", "2026-08-04 09:31", false},
		{"0 9-17 * * *", "2026-08-04 17:00", true},
		{"0 9-17 * * *", "2026-08-04 18:00", false},
		{"0 0 1 * *", "2026-08-01 00:00", true},
		{"0 0 1 * *", "2026-08-02 00:00", false},
		{"30 6 * * 2", "2026-08-04 06:30", true},  // 2026-08-04 is a Tuesday
		{"30 6 * * 3", "2026-08-04 06:30", false}, // Wednesday
		{"0 0 * * 0", "2026-08-09 00:00", true},   // Sunday as 0
		{"0 0 * * 7", "2026-08-09 00:00", true},   // and as 7
		{"0 12 1,15 * *", "2026-08-15 12:00", true},
		{"0 12 1,15 * *", "2026-08-16 12:00", false},
		{"0 0 * 8 *", "2026-08-04 00:00", true},
		{"0 0 * 9 *", "2026-08-04 00:00", false},
	} {
		t.Run(tc.expr+" @ "+tc.when, func(t *testing.T) {
			if got := mustCron(t, tc.expr).Matches(at(t, tc.when)); got != tc.want {
				t.Errorf("Matches = %v, want %v", got, tc.want)
			}
		})
	}
}

// The one piece of the grammar that surprises people, and the one where getting
// it wrong is discovered by a digest that never arrived: when BOTH day-of-month
// and day-of-week are restricted, standard cron ORs them.
func TestDayOfMonthAndDayOfWeekAreOredWhenBothRestricted(t *testing.T) {
	s := mustCron(t, "0 0 1 * 1") // the 1st, OR any Monday
	if !s.Matches(at(t, "2026-08-01 00:00")) {
		t.Error("should fire on the 1st even though it is a Saturday")
	}
	if !s.Matches(at(t, "2026-08-03 00:00")) {
		t.Error("should fire on a Monday even though it is the 3rd")
	}
	if s.Matches(at(t, "2026-08-04 00:00")) {
		t.Error("should not fire on a Tuesday that is not the 1st")
	}
	// And when only one is restricted it is a plain AND with the rest.
	only := mustCron(t, "0 0 5 * *")
	if only.Matches(at(t, "2026-08-04 00:00")) {
		t.Error("day-of-month only should not fire on the 4th")
	}
}

func TestMalformedCronIsRefusedWithAReason(t *testing.T) {
	for _, expr := range []string{
		"", "* * * *", "* * * * * *",
		"60 * * * *", "* 24 * * *", "* * 0 * *", "* * 32 * *", "* * * 13 *", "* * * * 8",
		"a * * * *", "5-1 * * * *", "*/0 * * * *", "*/999 * * * *",
		"@daily", "MON * * * *", "* * ? * *",
	} {
		t.Run("expr="+expr, func(t *testing.T) {
			if _, err := ParseCron(expr); err == nil {
				t.Errorf("ParseCron(%q) was accepted", expr)
			}
		})
	}
}

// Two ticks in the same minute derive the same identity, so the SECOND collides
// on the run table's unique index rather than firing again. The guard against
// double-firing is the same guard as for redelivery — one mechanism, so there
// is nothing for a second one to disagree with.
func TestTwoTicksInOneMinuteShareAnIdentity(t *testing.T) {
	a := at(t, "2026-08-04 09:00").Add(3 * time.Second)
	b := at(t, "2026-08-04 09:00").Add(57 * time.Second)
	if FireIdentity(a) != FireIdentity(b) {
		t.Errorf("same minute produced different identities: %q vs %q", FireIdentity(a), FireIdentity(b))
	}
	c := at(t, "2026-08-04 09:01")
	if FireIdentity(a) == FireIdentity(c) {
		t.Error("different minutes must produce different identities, or a schedule fires once and stops")
	}
}

// A schedule that parsed must be able to say what it was, or an operator
// reading the panel cannot check it against what they typed.
func TestAScheduleRemembersItsExpression(t *testing.T) {
	if got := mustCron(t, " 0 9 * * 1 ").String(); got != "0 9 * * 1" {
		t.Errorf("String = %q", got)
	}
}
