// SPDX-License-Identifier: Apache-2.0

package botdb

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/config"
	dbpkg "github.com/johalputt/vayupress/internal/db"
)

// The Shield console's reads of its two largest tables each come from an index
// made for them, against the schema the migrations build, not this package's
// test schema. On johal.in (710k signatures, 4.3M challenges) these queries
// read row after row and the page passed the 30-second limit: a 502.
func TestTheShieldConsoleReadsItsIndexes(t *testing.T) {
	prev := config.Cfg.DBPath
	config.Cfg.DBPath = filepath.Join(t.TempDir(), "plans.db")
	if err := dbpkg.Init(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		dbpkg.ClosePools()
		_ = dbpkg.DB.Close()
		config.Cfg.DBPath = prev
	})

	for _, c := range []struct {
		name, sql, want string
		arg             any
	}{
		{"the review queue", reviewQueueSQL, "USING INDEX idx_signatures_queue", 25},
		{"signatures learned in 24 h", learnedSinceSQL, "USING COVERING INDEX idx_signatures_learned", "2026-09-25"},
		{"the trail's challenge counts", challengeOutcomesSQL, "USING COVERING INDEX idx_challenges_created_outcome", "2026-09-25"},
		{"the trail's hourly challenges", challengeHoursSQL, "USING COVERING INDEX idx_challenges_created_outcome", "2026-09-25"},
	} {
		rows, err := dbpkg.DB.Query("EXPLAIN QUERY PLAN "+c.sql, c.arg)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		var plan []string
		for rows.Next() {
			var id, parent, notused int
			var detail string
			if err := rows.Scan(&id, &parent, &notused, &detail); err != nil {
				t.Fatal(err)
			}
			plan = append(plan, detail)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		_ = rows.Close()
		p := strings.Join(plan, " | ")
		if !strings.Contains(p, c.want) {
			t.Errorf("%s does not read %q: %s", c.name, c.want, p)
		}
		// The queue must come off the index in order; a sort means every
		// unverified signature is read first.
		if c.name == "the review queue" && strings.Contains(p, "TEMP B-TREE") {
			t.Errorf("the review queue sorts instead of reading its index in order: %s", p)
		}
	}
}
