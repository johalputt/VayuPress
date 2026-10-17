// SPDX-License-Identifier: Apache-2.0

package queue

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/mode"
)

// enterMode puts the process in m for one test.
func enterMode(t *testing.T, m mode.Mode) {
	t.Helper()
	mode.Global.ForceTransition(m, "test")
	t.Cleanup(mode.Global.Reset)
}

// Read-only is entered on repeated WAL write faults, and nothing refused a post
// write in it: the API, the connector and the scheduler enqueued as usual. Each
// refusing mode is its own seed, and the assertion is the reason, not just an
// error, so a refusal for any other cause does not pass.
func TestEnqueueIsRefusedWhileTheModeRefusesWrites(t *testing.T) {
	for _, m := range []mode.Mode{mode.ModeReadOnly, mode.ModeQuarantined} {
		t.Run(string(m), func(t *testing.T) {
			enterMode(t, m)
			w := NewSQLiteWriter(dbpkg.DB, 0)
			var before int
			dbpkg.DB.QueryRow(`SELECT COUNT(1) FROM write_jobs`).Scan(&before)
			err := w.Enqueue(context.Background(), dbpkg.Article{Slug: "refused-" + string(m), Title: "x", Content: "x"}, "insert")
			if !errors.Is(err, ErrWritesRefused) {
				t.Fatalf("Enqueue in %s returned %v, want ErrWritesRefused", m, err)
			}
			var after int
			dbpkg.DB.QueryRow(`SELECT COUNT(1) FROM write_jobs`).Scan(&after)
			if after != before {
				t.Errorf("a refused enqueue still wrote a job (%d → %d)", before, after)
			}
		})
	}
}

// A job queued before the mode changed must wait, not apply, and apply once the
// mode is back: refused, not lost.
func TestAQueuedJobWaitsOutReadOnly(t *testing.T) {
	SetCacheWriteFn(func(_, _ string) {})
	a := dbpkg.Article{ID: "held-id", Title: "Held", Slug: "held-in-read-only", Content: "<p>x</p>",
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	payload, _ := json.Marshal(a)
	res, err := dbpkg.DB.Exec(`INSERT INTO write_jobs(article_json,op) VALUES(?,'insert')`, payload)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	status := func() string {
		var s string
		dbpkg.DB.QueryRow(`SELECT status FROM write_jobs WHERE id=?`, id).Scan(&s)
		return s
	}

	mode.Global.ForceTransition(mode.ModeReadOnly, "test")
	t.Cleanup(mode.Global.Reset)
	if !processOneJob(0) {
		t.Error("the worker took a job in read-only mode")
	}
	if s := status(); s != "pending" {
		t.Fatalf("the job is %q after a read-only pass, want pending", s)
	}

	mode.Global.Reset()
	processOneJob(0)
	if s := status(); s == "pending" {
		t.Errorf("the job is still pending after the mode returned to normal")
	}
}
