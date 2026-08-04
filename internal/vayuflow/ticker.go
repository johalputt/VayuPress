// SPDX-License-Identifier: Apache-2.0

package vayuflow

import (
	"context"
	"errors"
	"time"
)

// Ticker fires schedule-triggered flows.
//
// It holds no state between ticks — deliberately. A ticker that remembered
// "last fired at" would be a second source of truth about whether work has
// happened, and it would disagree with the run table across a restart. The run
// table's UNIQUE idempotency index is the only such source: every firing in a
// given minute derives the same identity, so a second attempt collides rather
// than running. That makes overlapping ticks, a slow drain and two processes
// all safe by the same mechanism.
type Ticker struct {
	flows  *Store
	runner *Runner
	// loc is the install timezone the cron expressions are read in.
	loc *time.Location
	now func() time.Time
}

// NewTicker creates a Ticker. A nil location is treated as UTC rather than
// local: "local" on a server is whatever the host was configured with, which is
// not a property an operator reading a cron expression can see.
func NewTicker(flows *Store, runner *Runner, loc *time.Location) *Ticker {
	if loc == nil {
		loc = time.UTC
	}
	return &Ticker{flows: flows, runner: runner, loc: loc,
		now: func() time.Time { return time.Now() }}
}

// TickResult reports what one tick did. Skipped flows are named rather than
// counted, because a flow that is silently not firing is the failure mode this
// whole subsystem is trying to make impossible to have without noticing.
type TickResult struct {
	Fired       int
	Duplicate   int
	RateLimited int
	// Broken maps flow ID to why it could not be considered at all.
	Broken map[string]error
}

// Tick fires every enabled schedule flow whose expression matches the current
// minute. It is called once a minute; calling it more often is harmless,
// because the identity is the minute.
func (tk *Ticker) Tick(ctx context.Context) (TickResult, error) {
	res := TickResult{Broken: map[string]error{}}
	loadable, rejected, err := tk.flows.LoadableFlows(ctx)
	if err != nil {
		return res, err
	}
	for id, e := range rejected {
		res.Broken[id] = e
	}

	now := tk.now().In(tk.loc)
	identity := FireIdentity(now)

	for _, f := range loadable {
		if f.Trigger.Kind != TriggerSchedule {
			continue
		}
		sched, err := ParseCron(f.Trigger.Cron)
		if err != nil {
			// UNREACHABLE BY CONSTRUCTION, and kept anyway.
			//
			// LoadableFlows calls Flow.Complete, and Trigger.Complete parses the
			// cron — so any flow arriving here has an expression that already
			// parsed. TestSaveTimeValidationIsWhatKeepsThisBranchUnreachable
			// pins that relationship, because it is the only thing making this
			// true: if the two validators ever drift apart, this branch becomes
			// live again and a silent skip would be exactly the invisible
			// non-firing the whole design refuses.
			//
			// It is deliberately NOT claimed as tested. A mutation deleting the
			// line below survives the suite, which is the honest state of an
			// unreachable defence rather than something to paper over with an
			// assertion that would really be exercising LoadableFlows.
			res.Broken[f.ID] = err
			continue
		}
		if !sched.Matches(now) {
			continue
		}
		_, err = tk.runner.Execute(ctx, f, "schedule "+f.Trigger.Cron, identity, Subject{})
		switch {
		case err == nil:
			res.Fired++
		case errors.Is(err, ErrDuplicateRun):
			// Already fired this minute — by an overlapping tick, another
			// process, or a retry. This is the mechanism working.
			res.Duplicate++
		case errors.Is(err, ErrRateCeiling):
			res.RateLimited++
		default:
			res.Broken[f.ID] = err
		}
	}
	return res, nil
}

// Run drives Tick once a minute until ctx is cancelled. It aligns to the start
// of each minute so a flow written as "0 9 * * *" fires close to 09:00:00
// rather than at whatever offset the process happened to start at.
func (tk *Ticker) Run(ctx context.Context, onResult func(TickResult)) {
	for {
		now := tk.now()
		next := now.Truncate(time.Minute).Add(time.Minute)
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Until(next)):
		}
		res, err := tk.Tick(ctx)
		if err != nil && onResult == nil {
			continue
		}
		if onResult != nil {
			onResult(res)
		}
	}
}
