package main

// tor_space.go — App-level orchestration for the Anonymous Tor Space (ADR-0141).
// It ties the settings toggle to the child supervisor and the dedicated onion:
// when the operator flips the Space on, the VayuTor engine mints the dedicated
// onion (its Active() is true because the Space is on) and this loop starts the
// isolated child once that onion is live, so the child boots with its real
// .onion DOMAIN. Flipping it off stops the child (graceful drain) and the engine
// tears the onion down (keeping its key for a stable address next time).

import (
	"context"
	"time"

	"github.com/johalputt/vayupress/internal/settings"
)

// torSpaceEnabled reports whether the operator toggled the Anonymous Tor Space on.
func (a *App) torSpaceEnabled() bool {
	return a.siteSettings != nil &&
		a.siteSettings.Get(context.Background(), settings.KeyTorSpaceEnabled) == "on"
}

// torSpaceLoop converges the child to the toggle every tick. No-op in a Tor-Space
// child (no supervisor) or when the feature is unavailable. The child is drained
// deterministically at parent shutdown by a.torSpace.Shutdown() (main.go), which
// also latches the supervisor off so a tick racing shutdown can't respawn it —
// so this loop just needs to tick until the process exits.
func (a *App) torSpaceLoop() {
	if a.torSpace == nil {
		return
	}
	t := time.NewTicker(15 * time.Second)
	defer t.Stop()
	for {
		a.reconcileTorSpace()
		<-t.C
	}
}

// reconcileTorSpace starts/stops the child to match the toggle. It starts the
// child only AFTER its dedicated onion is live, so the child boots with its real
// .onion as DOMAIN (never a stale localhost identity).
func (a *App) reconcileTorSpace() {
	if a.torSpace == nil {
		return
	}
	if !a.torSpaceEnabled() {
		_ = a.torSpace.Ensure(false)
		return
	}
	onion := ""
	if a.vayuTor != nil {
		onion = a.vayuTor.SpaceOnion()
	}
	if onion == "" {
		if a.vayuTor != nil {
			a.vayuTor.Kick() // prompt the engine to mint the dedicated onion
		}
		return
	}
	a.torSpace.SetOnion(onion)
	_ = a.torSpace.Ensure(true)
}

// torSpaceStatus is a snapshot for the admin Spaces page.
type torSpaceStatus struct {
	Enabled bool
	Running bool
	Onion   string
	Port    int
	LastErr string
}

// torSpaceStatusNow reads the live Anonymous Tor Space state.
func (a *App) torSpaceStatusNow() torSpaceStatus {
	st := torSpaceStatus{Enabled: a.torSpaceEnabled()}
	if a.torSpace != nil {
		st.Running = a.torSpace.Running()
		st.Port = a.torSpace.Port()
		st.LastErr = a.torSpace.LastError()
	}
	if a.vayuTor != nil {
		st.Onion = a.vayuTor.SpaceOnion()
	}
	// When the Space is enabled but its onion is not live yet, the supervisor has
	// not even run (reconcileTorSpace waits for the onion first), so its LastError
	// is empty and the card would show "Publishing…" forever with no clue about a
	// genuine blocker. Borrow the VayuTor engine's actionable reason — e.g. "no
	// tor control port available" (tor not installed) — so the operator can act.
	// Benign progress (still connecting / bootstrapping) is deliberately left to
	// the card's existing "Publishing your .onion…" copy, not shown as a warning.
	if st.Enabled && st.Onion == "" && st.LastErr == "" && a.vayuTor != nil {
		if snap := a.vayuTor.Snapshot(); snap.LastError != "" {
			st.LastErr = "Tor: " + snap.LastError
		}
	}
	return st
}
