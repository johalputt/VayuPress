// SPDX-License-Identifier: Apache-2.0

package botdb

import (
	"context"
	"testing"
	"time"
)

func hourOf(at time.Time) string { return at.UTC().Truncate(time.Hour).Format("2006-01-02 15:00") }

func bucketAt(hours []HourBucket, at time.Time) (HourBucket, bool) {
	for _, b := range hours {
		if b.Hour == hourOf(at) {
			return b, true
		}
	}
	return HourBucket{}, false
}

// TestReadHoursCountsEachHour — refusals and challenges an hour, the hour
// under way included, in order, and nothing from before the window.
func TestReadHoursCountsEachHour(t *testing.T) {
	s := trailStore(t)
	now := time.Now().UTC()
	tenAgo, live := now.Add(-10*time.Hour), now.Add(-time.Minute)
	insertBlock(t, s, tenAgo, "r", "/p", "")
	insertBlock(t, s, tenAgo, "r", "/p", "")
	insertChallenge(t, s, tenAgo, "issued")
	insertBlock(t, s, live, "r", "/p", "")
	insertBlock(t, s, now.Add(-30*time.Hour), "r", "/p", "") // before the window

	hours, err := s.ReadHours(context.Background(), 24)
	if err != nil {
		t.Fatal(err)
	}
	if b, ok := bucketAt(hours, tenAgo); !ok || b.Blocks != 2 || b.Challenges != 1 {
		t.Errorf("ten hours ago: %+v", b)
	}
	if b, ok := bucketAt(hours, live); !ok || b.Blocks != 1 {
		t.Errorf("the hour under way: %+v", b)
	}
	if _, ok := bucketAt(hours, now.Add(-30*time.Hour)); ok {
		t.Error("an hour before the window was counted")
	}
	for i := 1; i < len(hours); i++ {
		if hours[i-1].Hour >= hours[i].Hour {
			t.Fatalf("out of order: %v", hours)
		}
	}
}

// TestReadHoursKeepsAFinishedHourAndRecountsTheLiveOne — a finished hour is
// counted once (its events gone from the table, the count stays), and the
// hour under way is counted afresh on every read.
func TestReadHoursKeepsAFinishedHourAndRecountsTheLiveOne(t *testing.T) {
	s := trailStore(t)
	now := time.Now().UTC()
	tenAgo, live := now.Add(-10*time.Hour), now.Add(-time.Minute)
	insertBlock(t, s, tenAgo, "r", "/p", "")
	insertBlock(t, s, live, "r", "/p", "")
	if _, err := s.ReadHours(context.Background(), 24); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`DELETE FROM shield_events`); err != nil {
		t.Fatal(err)
	}
	insertBlock(t, s, live, "r", "/p", "")
	insertBlock(t, s, live, "r", "/p", "")

	hours, err := s.ReadHours(context.Background(), 24)
	if err != nil {
		t.Fatal(err)
	}
	if b, ok := bucketAt(hours, tenAgo); !ok || b.Blocks != 1 {
		t.Errorf("the finished hour was counted again: %+v", b)
	}
	if b, ok := bucketAt(hours, live); !ok || b.Blocks != 2 {
		t.Errorf("the hour under way was not counted afresh: %+v", b)
	}
}

// TestReadHoursWidensWithoutAGap — a longer window after a shorter one counts
// the hours the shorter one never kept.
func TestReadHoursWidensWithoutAGap(t *testing.T) {
	s := trailStore(t)
	thirtyAgo := time.Now().UTC().Add(-30 * time.Hour)
	insertBlock(t, s, thirtyAgo, "r", "/p", "")
	if _, err := s.ReadHours(context.Background(), 2); err != nil {
		t.Fatal(err)
	}
	hours, err := s.ReadHours(context.Background(), 48)
	if err != nil {
		t.Fatal(err)
	}
	if b, ok := bucketAt(hours, thirtyAgo); !ok || b.Blocks != 1 {
		t.Errorf("thirty hours ago, after a two-hour read: %+v", b)
	}
}

// TestReadHoursWaitsForAnHourToSettle — an event of the hour just ended can
// land after it ends (events go through a queue), so that hour is counted
// afresh until hourSettle has passed, not kept at its first count.
func TestReadHoursWaitsForAnHourToSettle(t *testing.T) {
	s := trailStore(t)
	now := time.Date(2026, 11, 13, 11, 2, 0, 0, time.UTC)
	s.hours.now = func() time.Time { return now }
	late := now.Add(-3 * time.Minute) // 10:59
	insertBlock(t, s, late, "r", "/p", "")
	if _, err := s.ReadHours(context.Background(), 24); err != nil {
		t.Fatal(err)
	}
	insertBlock(t, s, late.Add(30*time.Second), "r", "/p", "") // written after the first read
	hours, err := s.ReadHours(context.Background(), 24)
	if err != nil {
		t.Fatal(err)
	}
	if b, ok := bucketAt(hours, late); !ok || b.Blocks != 2 {
		t.Errorf("the hour just ended was kept before it settled: %+v", b)
	}
}

// TestTheCostOfDecidingIsReadFromTheLatestDecisions — the median and the 95th
// percentile come from the window's latest evalSample events, not from a sort
// of every event in it, which on a busy fortnight held Shield's page for
// seconds. Older events here cost a thousand times more; were they read, the
// median would be theirs.
func TestTheCostOfDecidingIsReadFromTheLatestDecisions(t *testing.T) {
	s := trailStore(t)
	now := time.Now().UTC()
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	ins := func(at time.Time, us int) {
		if _, err := tx.Exec(`INSERT INTO shield_events(action,eval_us,created_at) VALUES('block',?,?)`, us, at.Format("2006-01-02 15:04:05")); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < evalSample; i++ {
		ins(now.Add(-time.Minute), 1)
	}
	for i := 0; i < evalSample+evalSample/2; i++ {
		ins(now.Add(-5*time.Hour), 1000)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	tr, err := s.ReadTrail(context.Background(), 24, 8, 30)
	if err != nil {
		t.Fatal(err)
	}
	if tr.EvalP50 != 1 || tr.EvalP95 != 1 {
		t.Errorf("cost p50 %d, p95 %d; want 1 and 1 µs from the latest %d decisions", tr.EvalP50, tr.EvalP95, evalSample)
	}
}

// TestReadHoursNarrowsToItsWindow — after a longer window, a shorter one shows
// none of the hours before its start, though they were kept.
func TestReadHoursNarrowsToItsWindow(t *testing.T) {
	s := trailStore(t)
	thirtyAgo := time.Now().UTC().Add(-30 * time.Hour)
	insertBlock(t, s, thirtyAgo, "r", "/p", "")
	if _, err := s.ReadHours(context.Background(), 48); err != nil {
		t.Fatal(err)
	}
	hours, err := s.ReadHours(context.Background(), 24)
	if err != nil {
		t.Fatal(err)
	}
	if b, ok := bucketAt(hours, thirtyAgo); ok {
		t.Errorf("an hour before a 24-hour window was shown: %+v", b)
	}
}
