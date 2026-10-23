// SPDX-License-Identifier: Apache-2.0

package botdb

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/config"
	dbpkg "github.com/johalputt/vayupress/internal/db"
)

// The Shield console's reads of its largest tables each come from an index
// made for them, against the schema the migrations build, not this package's
// test schema. On johal.in (710k signatures, 4.3M challenges, now shield events) these queries
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

	allW, allArgs := trailScope(AllSites, "2026-09-25")
	siteW, siteArgs := trailScope("d1", "2026-09-25")
	allA, siteA := any(allArgs), any(siteArgs)
	for _, c := range []struct {
		name, sql, want string
		arg             any
	}{
		{"the review queue", reviewQueueSQL, "USING INDEX idx_signatures_queue", 25},
		{"signatures learned in 24 h", learnedSinceSQL, "USING COVERING INDEX idx_signatures_learned", "2026-09-25"},
		{"the trail's challenge counts", outcomes + allW, "USING COVERING INDEX idx_shield_events_created", allA},
		{"the trail's hourly challenges", outcomeHours + allW + ` GROUP BY h`, "USING COVERING INDEX idx_shield_events_created", allA},
		{"the trail's refusals", `SELECT COUNT(1) FROM shield_events` + allW + refused, "USING COVERING INDEX idx_shield_events_created", allA},
		{"the trail's reasons", `SELECT reason AS k, COUNT(1) FROM shield_events` + allW + refused + ` GROUP BY k`, "USING INDEX idx_shield_events_created", allA},
		{"one site's challenge counts", outcomes + siteW, "USING COVERING INDEX idx_shield_events_site", siteA},
		{"one site's hourly challenges", outcomeHours + siteW + ` GROUP BY h`, "USING COVERING INDEX idx_shield_events_site", siteA},
		{"one site's refusals", `SELECT COUNT(1) FROM shield_events` + siteW + refused, "USING COVERING INDEX idx_shield_events_site", siteA},
		{"one site's paths", `SELECT path AS k, COUNT(1) FROM shield_events` + siteW + refused + ` GROUP BY k`, "USING INDEX idx_shield_events_site", siteA},
		{"one site's newest refusals", recentBlocksSQL, "USING INDEX idx_shield_events_site", []any{"d1", 50}},
		{"the trail's gates", layersSQL + allW + ` AND layer<>'' GROUP BY k`, "USING INDEX idx_shield_events_created", allA},
		{"one site's client classes", classesSQL + siteW + refused + ` GROUP BY k`, "USING INDEX idx_shield_events_site", siteA},
		{"one site's deciding cost", `SELECT eval_us FROM shield_events` + siteW + ` AND eval_us>0 ORDER BY eval_us LIMIT 1 OFFSET 0`, "USING INDEX idx_shield_events_site", siteA},
	} {
		args, many := c.arg.([]any)
		if !many {
			args = []any{c.arg}
		}
		rows, err := dbpkg.DB.Query("EXPLAIN QUERY PLAN "+c.sql, args...)
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
		if (c.name == "the review queue" || c.name == "one site's newest refusals") && strings.Contains(p, "TEMP B-TREE") {
			t.Errorf("the review queue sorts instead of reading its index in order: %s", p)
		}
	}
}
