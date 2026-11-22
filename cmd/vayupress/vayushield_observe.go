// SPDX-License-Identifier: Apache-2.0

package main

// vayushield_observe.go — observe-only mode ends by itself.
//
// Observe-only turns every gate into a counter: nothing is blocked, limited,
// challenged or banned. It is the right way to try a threshold and the wrong
// state to leave a site in, and one switch on the Shield form turns it on. On
// 2026-11-22 a save that meant to turn on two network lists left johal.in in
// it. So it is asked about before it is saved on (the Shield page's script),
// it is shown across the console while it is on, and it ends by itself
// observeFor after it was switched on.

import (
	"context"
	"strconv"
	"strings"
	"time"

	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/logging"
	"github.com/johalputt/vayupress/internal/queue"
	"github.com/johalputt/vayupress/internal/settings"
)

// observeFor is how long observe-only lasts once switched on: a day of real
// traffic is what a threshold trial needs.
const observeFor = 24 * time.Hour

// observeUntil is when observe-only ends, and whether it is on. A mode
// switched on before its start was recorded counts from now, and is
// recorded so: an install that upgrades while in it gets its day from then.
func (a *App) observeUntil(ctx context.Context) (time.Time, bool) {
	if a.siteSettings == nil || a.siteSettings.Get(ctx, settings.ForPrimary(), settings.KeyShieldObserve) != "on" {
		return time.Time{}, false
	}
	since, err := strconv.ParseInt(strings.TrimSpace(a.siteSettings.Get(ctx, settings.ForPrimary(), settings.KeyShieldObserveSince)), 10, 64)
	if err != nil || since <= 0 {
		since = time.Now().Unix()
		_ = a.siteSettings.SetMany(ctx, settings.ForPrimary(), map[string]string{settings.KeyShieldObserveSince: strconv.FormatInt(since, 10)})
	}
	return time.Unix(since, 0).Add(observeFor), true
}

// endObserveIfDue switches observe-only off once its day is over, and
// applies it at once, so the gates enforce again without a save.
func (a *App) endObserveIfDue(ctx context.Context) {
	until, on := a.observeUntil(ctx)
	if !on || time.Now().Before(until) {
		return
	}
	if err := a.siteSettings.SetMany(ctx, settings.ForPrimary(), map[string]string{
		settings.KeyShieldObserve: "off", settings.KeyShieldObserveSince: "",
	}); err != nil {
		logging.LogError("vayushield", "observe-only is over but could not be switched off", err.Error())
		return
	}
	dbpkg.AuditLog("vayushield.settings", "system", "shield", settings.KeyShieldObserve+": on → off (ended after "+observeFor.String()+")")
	logging.LogWarn("vayushield", "observe-only ended after "+observeFor.String()+": every gate enforces again")
	if a.vayuShield != nil {
		a.vayuShield.ApplySettings(a.shieldSettings(ctx))
	}
}

// watchObserve checks once a minute whether observe-only's day is over.
func (a *App) watchObserve() {
	a.endObserveIfDue(context.Background())
	go func() {
		t := time.NewTicker(time.Minute)
		defer t.Stop()
		for {
			select {
			case <-queue.DoneCh:
				return
			case <-t.C:
				a.endObserveIfDue(context.Background())
			}
		}
	}()
}

// observeSince is the start to store with a save: now when the save
// switches observe-only on, the stored start when it stays on, nothing when
// it is off.
func (a *App) observeSince(ctx context.Context, posted string) string {
	if posted != "on" {
		return ""
	}
	if a.siteSettings.Get(ctx, settings.ForPrimary(), settings.KeyShieldObserve) == "on" {
		if s := strings.TrimSpace(a.siteSettings.Get(ctx, settings.ForPrimary(), settings.KeyShieldObserveSince)); s != "" {
			return s
		}
	}
	return strconv.FormatInt(time.Now().Unix(), 10)
}
