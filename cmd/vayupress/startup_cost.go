// SPDX-License-Identifier: Apache-2.0

package main

// startup_cost.go — ADR-0155 P4. What a restart actually costs, on the page.
//
// # Why this is a feature and not a log line
//
// nginx has no queue in front of the app, so on an install WITHOUT socket
// activation the time this process takes to start is exactly the time every
// visitor gets a 502. That number decided whether P5 was a nicety or the main
// event, and the only place it existed was a journal line — which the operator
// who reported this could not even read: `journalctl -u vayupress` answered
// "No journal files were opened due to insufficient permissions."
//
// A number you need root and a shell to see is a number that never informs a
// decision. So the install records its own startup durations and the panel shows
// them, which is this project's standing rule about diagnostics belonging on the
// page rather than in a command someone pastes back.
//
// # Why a spread and not a figure
//
// One number is the flattering one. A cold boot after a reboot, a warm restart
// after an update and a restart under load are different, and an operator asking
// "can I update now" needs the worst of them, not the best. So a short ring is
// kept and the panel quotes a range.

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/johalputt/vayupress/internal/settings"
)

// startupRingSize is how many recent boots are remembered.
//
// Enough to show a spread, few enough that the value stays a short string in one
// settings row. A longer history would be a table, and a table would be a
// migration for a diagnostic — the wrong trade for something whose whole purpose
// is to answer one question at a glance.
const startupRingSize = 10

// recordStartupCost appends this boot's startup duration to the ring.
//
// Errors are swallowed on purpose and this is called after the service is
// already serving: a diagnostic that can delay or fail a start is worse than no
// diagnostic. The one thing it must not do is make the number up — a boot whose
// duration cannot be stored simply is not in the ring, and the panel says how
// many samples it has rather than implying it has all of them.
func recordStartupCost(ctx context.Context, store *settings.Store, took time.Duration) {
	if store == nil {
		return
	}
	prev := parseStartupRing(store.Get(ctx, settings.ForPrimary(), settings.KeyStartupMillis))
	prev = append(prev, int(took.Milliseconds()))
	if len(prev) > startupRingSize {
		prev = prev[len(prev)-startupRingSize:]
	}
	parts := make([]string, 0, len(prev))
	for _, v := range prev {
		parts = append(parts, strconv.Itoa(v))
	}
	_ = store.SetMany(ctx, settings.ForPrimary(),
		map[string]string{settings.KeyStartupMillis: strings.Join(parts, ",")})
}

// parseStartupRing reads the stored ring, discarding anything that is not a
// plausible duration.
//
// Negatives and absurd values are dropped rather than clamped. A clamp invents a
// sample that never happened, and this whole file exists so a page can quote
// measurements instead of estimates.
func parseStartupRing(raw string) []int {
	var out []int
	for _, f := range strings.Split(raw, ",") {
		f = strings.TrimSpace(f)
		if f == "" {
			continue
		}
		n, err := strconv.Atoi(f)
		// A day is not a startup. An hour is not either, but the bound is set
		// where a value is certainly corrupt rather than merely alarming: a
		// genuinely dreadful ten-minute start is the very thing this is meant to
		// surface, so it must survive the filter.
		if err != nil || n < 0 || n > 24*60*60*1000 {
			continue
		}
		out = append(out, n)
	}
	return out
}

// startupCost is what the panel renders.
type startupCost struct {
	// Samples is how many boots are in the ring. Reported because "1200ms" from
	// one sample and from ten are different claims.
	Samples  int
	FastMS   int
	SlowMS   int
	LatestMS int
	// Queued is whether an inherited socket means a restart costs latency
	// rather than errors. It changes what the numbers MEAN, so it travels with
	// them rather than being looked up separately.
	Queued bool
}

// readStartupCost summarises the ring.
func readStartupCost(ctx context.Context, store *settings.Store, queued bool) startupCost {
	c := startupCost{Queued: queued}
	if store == nil {
		return c
	}
	ring := parseStartupRing(store.Get(ctx, settings.ForPrimary(), settings.KeyStartupMillis))
	if len(ring) == 0 {
		return c
	}
	c.Samples, c.FastMS, c.SlowMS, c.LatestMS = len(ring), ring[0], ring[0], ring[len(ring)-1]
	for _, v := range ring {
		if v < c.FastMS {
			c.FastMS = v
		}
		if v > c.SlowMS {
			c.SlowMS = v
		}
	}
	return c
}

// Describe renders the cost of a restart in words.
//
// Every branch names what the number IS — visitors getting errors, or visitors
// waiting — because "startup: 1.2s" is a statistic and "1.2 seconds of 502 for
// everyone" is a decision an operator can make.
func (c startupCost) Describe() string {
	if c.Samples == 0 {
		return "This install has not recorded a startup yet, so what a restart costs here is " +
			"not known. It is measured from the next one onwards and reported here rather than " +
			"estimated."
	}
	span := fmtMillis(c.FastMS)
	if c.SlowMS != c.FastMS {
		span = fmtMillis(c.FastMS) + "–" + fmtMillis(c.SlowMS)
	}
	sample := "across " + strconv.Itoa(c.Samples) + " recorded starts"
	if c.Samples == 1 {
		sample = "from a single recorded start, so treat it as an indication rather than a range"
	}

	if c.Queued {
		return "This install starts in " + span + " " + sample + ". Because systemd holds the " +
			"listening socket, that time is spent with connections QUEUED rather than refused: a " +
			"visitor mid-restart waits, and nobody sees an error page."
	}
	return "This install starts in " + span + " " + sample + ". nginx has no queue in front of " +
		"the app, so that is how long every visitor gets a 502 when it restarts. Enabling the " +
		"socket unit turns that time into a wait instead of an error."
}

// fmtMillis renders a duration the way an operator thinks about downtime.
func fmtMillis(ms int) string {
	if ms < 1000 {
		return strconv.Itoa(ms) + "ms"
	}
	// One decimal below a minute, whole seconds above: the difference between
	// 1.4s and 1.5s matters to nobody once it is past a minute of outage.
	if ms < 60*1000 {
		return strconv.FormatFloat(float64(ms)/1000, 'f', 1, 64) + "s"
	}
	return strconv.Itoa(ms/1000) + "s"
}
