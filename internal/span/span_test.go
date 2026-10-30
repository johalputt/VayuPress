// SPDX-License-Identifier: Apache-2.0

package span

import (
	"context"
	"testing"
	"time"
)

// Without a chosen range a period is the last days days, today included,
// with no end; with one it is the range, its last day counted.
func TestAPeriodIsTheLastDaysOrTheChosenRange(t *testing.T) {
	now := time.Date(2026, 10, 30, 15, 4, 0, 0, time.UTC)
	if from, until := Days(context.Background(), now, 30); from != "2026-10-01" || until != Open {
		t.Errorf("the last 30 days = %s..%s, want 2026-10-01..%s", from, until, Open)
	}
	ctx := With(context.Background(), Range{From: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC), To: time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC)})
	if from, until := Days(ctx, now, 30); from != "2026-09-01" || until != "2026-10-01" {
		t.Errorf("September = %s..%s, want 2026-09-01..2026-10-01 (the 30th counted)", from, until)
	}
	if n := Length(ctx, 7); n != 30 {
		t.Errorf("September is %d days long", n)
	}
	if n := Length(context.Background(), 7); n != 7 {
		t.Errorf("the last 7 days are %d days long", n)
	}
}
