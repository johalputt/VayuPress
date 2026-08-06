// SPDX-License-Identifier: Apache-2.0

package main

// admin_os_writestall_test.go — what the panel says about the write connection.
//
// The fault this card exists for produced no evidence anywhere an operator could
// reach: the site 502'd, recovered by itself, and left a running process, a
// healthy database, no restart and an empty log. So these tests are about the
// CARD's claims, not the watchdog's arithmetic — a panel that overstates or
// understates what is being measured is a defect of the same kind as the missing
// measurement, and this repo has shipped that one before.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/johalputt/vayupress/internal/analytics"
	dbpkg "github.com/johalputt/vayupress/internal/db"
)

func quietWriter() (dbpkg.WriteStallState, analytics.CollectorState) {
	return dbpkg.WriteStallState{Watching: true, MaxOpen: 1},
		analytics.CollectorState{Running: true, BufferedHi: 20000}
}

// Mid-incident, the answer must be above the fold and specific. An operator
// opening this page while the site is stalling wants "yes, now, this long, this
// many callers", not a history table to infer it from.
func TestTheCardNamesAStallThatIsHappeningNow(t *testing.T) {
	st, rec := quietWriter()
	st.Stalled = true
	st.Total = 1
	st.Current = &dbpkg.StallEvent{
		Start:    time.Date(2026, 8, 6, 14, 3, 11, 0, time.UTC),
		Duration: 42 * time.Second,
		Blocked:  6 * time.Minute,
		Waits:    137,
		Ongoing:  true,
	}

	card := writeStallCard(st, rec)
	for _, want := range []string{"contended right now", "14:03:11", "42.0s", "137", "6m 0s"} {
		if !strings.Contains(card, want) {
			t.Errorf("the card does not report %q while a stall is in progress:\n%s", want, card)
		}
	}
	// And it must not frighten anyone about the half that still works.
	if !strings.Contains(card, "Reads and cached pages are unaffected") {
		t.Error("the card does not say what still works; an operator reading it mid-incident needs " +
			"to know whether their readers are affected")
	}

	tiles := writeStallStats(st, rec)
	if !strings.Contains(tiles, "stat-card--warn") {
		t.Error("no tile is toned for attention while the writer is stalled")
	}
	if !strings.Contains(tiles, "happening now") {
		t.Errorf("the stall tile does not say it is happening now:\n%s", tiles)
	}
}

// A quiet install must read as quiet — and say so in words that distinguish
// "nothing happened" from "nothing was measured". They are entirely different
// facts and only one of them is good news.
func TestAQuietInstallReadsAsQuietNotAsUnmeasured(t *testing.T) {
	st, rec := quietWriter()
	card := writeStallCard(st, rec)
	if !strings.Contains(card, "No write stall has been recorded") {
		t.Errorf("a quiet install does not say so plainly:\n%s", card)
	}
	if strings.Contains(card, "Not being watched") {
		t.Error("a watched, quiet install is being reported as unwatched")
	}
	if strings.Contains(card, "contended right now") {
		t.Error("an idle install claims to be stalling")
	}
}

// THE claim test. If the watchdog never started, the card must say the numbers
// are not being measured — not show a reassuring zero. A zero that means "no
// data" and a zero that means "no stalls" look identical, and the difference is
// the entire value of the card.
func TestAnUnwatchedInstallIsNotReportedAsHealthy(t *testing.T) {
	st, rec := quietWriter()
	st.Watching = false
	card := writeStallCard(st, rec)
	if !strings.Contains(card, "Not being watched") {
		t.Errorf("the watchdog is not running and the card shows a clean bill of health. A zero "+
			"meaning 'nothing measured' must never render as a zero meaning 'nothing wrong':\n%s", card)
	}
	if !strings.Contains(card, "fault in the install") {
		t.Error("the card does not say that an unwatched install is itself a fault")
	}
}

// Same rule for the recorder. Views buffered into a map nobody drains are views
// silently lost — the defect class that made VayuKeep inert, which this repo
// paid for once already.
func TestViewCountingBeingOffIsStatedPlainly(t *testing.T) {
	st, rec := quietWriter()
	rec.Running = false
	tiles := writeStallStats(st, rec)
	if !strings.Contains(tiles, "views are NOT being written") {
		t.Errorf("the flusher is not running and the panel does not say views are being lost:\n%s", tiles)
	}
	if !strings.Contains(tiles, ">off<") {
		t.Errorf("the view-counting tile does not read off:\n%s", tiles)
	}
}

// Dropped views must be admitted, with the reason. A bounded buffer is a
// deliberate trade and the panel should say so rather than hiding the loss.
func TestDroppedViewsAreAdmittedWithTheReason(t *testing.T) {
	st, rec := quietWriter()
	rec.Dropped = 812
	card := writeStallCard(st, rec)
	if !strings.Contains(card, "812") {
		t.Errorf("812 dropped views are not mentioned:\n%s", card)
	}
	if !strings.Contains(card, "bounded on purpose") {
		t.Error("the drop is reported without the reason, which reads as a bug rather than a trade")
	}
}

// The compression ratio is the number that justifies the whole design, so it
// must be computed from real counters rather than asserted in prose.
func TestTheViewsPerWriteRatioIsComputedNotClaimed(t *testing.T) {
	st, rec := quietWriter()
	rec.Flushed, rec.Writes = 9000, 45
	tiles := writeStallStats(st, rec)
	if !strings.Contains(tiles, "200.0×") {
		t.Errorf("9000 views over 45 statements should read 200.0×:\n%s", tiles)
	}
	// Before anything has been written there is no ratio, and inventing one
	// (1×, 0×) would be a claim about work that has not happened.
	rec.Flushed, rec.Writes = 0, 0
	if !strings.Contains(writeStallStats(st, rec), "—") {
		t.Error("a ratio was rendered before any views had been written")
	}
}

// A snapshot path reaches the page. It is written by this process, but the card
// must still escape it — an assertion that only holds for trusted input is one
// refactor away from not holding.
func TestTheSnapshotPathIsEscaped(t *testing.T) {
	st, rec := quietWriter()
	st.Recent = []dbpkg.StallEvent{{
		Start: time.Now().UTC(), Duration: 3 * time.Second, Waits: 2,
		Dump: `/var/x/"><img onerror=alert(1) src=x>.txt`,
	}}
	card := writeStallCard(st, rec)
	if strings.Contains(card, "<img onerror") {
		t.Errorf("the snapshot path reached the page as markup:\n%s", card)
	}
	if !strings.Contains(card, "&lt;img") {
		t.Error("the hostile path was neither escaped nor rejected")
	}
}

// House style and CSP, per the VayuOS page conventions.
func TestTheWriterCardIsCSPSafe(t *testing.T) {
	st, rec := quietWriter()
	st.Stalled = true
	st.Current = &dbpkg.StallEvent{Start: time.Now().UTC(), Duration: time.Second, Waits: 1}
	st.Recent = []dbpkg.StallEvent{{Start: time.Now().UTC(), Duration: time.Second, Dump: "/tmp/a.txt"}}
	assertCSPSafe(t, "write stall card", writeStallCard(st, rec))
	assertCSPSafe(t, "write stall tiles", writeStallStats(st, rec))
}

// ── Wiring ───────────────────────────────────────────────────────────────────
//
// A perfect recorder that is never started, and a watchdog that never samples,
// are both invisible. This is a source check because it is the WIRING that
// regresses, not the function: both can be flawless and unreferenced.

func TestBootStartsTheCollectorAndTheStallWatch(t *testing.T) {
	b, err := os.ReadFile(filepath.Clean("main.go"))
	if err != nil {
		t.Skipf("main.go not readable: %v", err)
		return
	}
	src := string(b)
	for _, c := range []struct{ call, why string }{
		{"a.analytics.StartCollector(", "views would be counted into a buffer nobody drains, and " +
			"every view would be lost in silence"},
		{"dbpkg.StartStallWatch(", "the write connection could jam exactly as it did before and " +
			"nothing would record that it happened"},
	} {
		if !strings.Contains(src, c.call) {
			t.Errorf("boot does not call %s — %s", c.call, c.why)
		}
	}
}

// The hot path must never go back to a write per view. This is the defect
// itself: a goroutine per visitor, with no deadline, queued on a pool of one.
func TestThePublicPathDoesNotWritePerView(t *testing.T) {
	b, err := os.ReadFile(filepath.Clean("handlers_admin.go"))
	if err != nil {
		t.Skipf("handlers_admin.go not readable: %v", err)
		return
	}
	src := string(b)
	if strings.Contains(src, "a.analytics.Record(") {
		t.Error("a public handler calls analytics.Record directly. That writes to SQLite on the " +
			"single write connection; detaching it in a goroutine does not help, because a " +
			"context.Background() wait has no deadline and no bound. Use RecordAsync.")
	}
	if !strings.Contains(src, "a.analytics.RecordAsync(") {
		t.Error("no public handler counts a view at all any more; the fix removed the feature")
	}
}
