package vayushield

import (
	"context"
	"time"

	"github.com/johalputt/vayupress/internal/vayushield/botdb"
)

// LearningResult summarizes one run of the 24-hour learning cycle.
type LearningResult struct {
	Promoted int64 `json:"promoted"` // unknown candidates promoted to bad_bot
	Purged   int64 `json:"purged"`   // stale low-value signatures removed
}

// RunLearningCycle performs one improvement pass over the adaptive database:
//
//   - Promote: recurring auto-learned "unknown" fingerprints that have been seen
//     often, never verified, and never reported as a false positive are promoted
//     to bad_bot at high confidence. These are the fingerprint clusters that keep
//     reappearing with bot-like signals — exactly what the spec's 24h analysis is
//     meant to catch — while still landing in the operator review queue.
//   - Purge: stale, low-frequency, unverified candidates are removed so the table
//     cannot grow without bound.
//
// It is safe to call at any time and is a no-op without an adaptive store.
func (m *Manager) RunLearningCycle(ctx context.Context, retainDays int) (LearningResult, error) {
	var out LearningResult
	if m.cfg.DB == nil {
		return out, nil
	}
	const minSightings = 5
	res, err := m.cfg.DB.ExecContext(ctx, `UPDATE vayushield_signatures
SET classification='bad_bot', confidence=MAX(confidence,0.85), notes=CASE WHEN notes='' THEN 'auto-promoted: recurring bot-like fingerprint' ELSE notes END
WHERE auto_learned=1 AND operator_verified=0 AND classification='unknown' AND request_count>=? AND false_positive_count=0`, minSightings)
	if err != nil {
		return out, err
	}
	out.Promoted, _ = res.RowsAffected()

	if m.cfg.Bots != nil && retainDays > 0 {
		if n, perr := m.cfg.Bots.PurgeStale(ctx, retainDays); perr == nil {
			out.Purged = n
		}
	}
	return out, nil
}

// StartReporter launches the background learning goroutine. It runs once shortly
// after start (to catch anything missed while down) and then every interval
// (24h in production). It stops when done is closed. onDone, if set, is called
// after each cycle with the result and any error, so the caller can log an
// improvement event and charge the governance error budget.
func (m *Manager) StartReporter(done <-chan struct{}, interval time.Duration, retainDays int, onDone func(LearningResult, error)) {
	if interval <= 0 {
		interval = 24 * time.Hour
	}
	go func() {
		first := time.NewTimer(2 * time.Minute)
		defer first.Stop()
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		run := func() {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			res, err := m.RunLearningCycle(ctx, retainDays)
			if onDone != nil {
				onDone(res, err)
			}
		}
		for {
			select {
			case <-done:
				return
			case <-first.C:
				run()
			case <-ticker.C:
				run()
			}
		}
	}()
}

// Enabled reports whether bot protection is active.
func (m *Manager) Enabled() bool { return m.cfg.Enabled }

// EffectiveThresholds returns the active PoW/JS/block score thresholds.
func (m *Manager) EffectiveThresholds() (pow, js, block float64) {
	return m.cfg.PoWThreshold, m.cfg.JSThreshold, m.cfg.BlockThreshold
}

// BotStore returns the adaptive learning store (may be nil).
func (m *Manager) BotStore() *botdb.Store { return m.cfg.Bots }

// StaticDB returns the compiled-in static classifier.
func (m *Manager) StaticDB() *botdb.StaticDB { return m.cfg.Static }
