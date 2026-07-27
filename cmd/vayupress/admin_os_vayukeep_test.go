// SPDX-License-Identifier: Apache-2.0

package main

// admin_os_vayukeep_test.go — the replication panel must never flatter.
//
// Every state below is one an operator can actually be in, and the assertion is
// always the same in spirit: a page that says "backups: enabled" while nothing
// has ever been restored is worse than no page, because it converts an unknown
// risk into a false certainty. So the tests check that each broken state
// produces a warning the operator can see, and that the reassuring words appear
// only when they are earned.

import (
	"strings"
	"testing"
	"time"

	"github.com/johalputt/vayupress/internal/mode"
	"github.com/johalputt/vayupress/internal/vayukeep"
)

var vkNow = time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)

// TestPanelWarnsWhenNothingHasBeenRestored is the core of it. Writing files is
// not backing up; until a drill has passed the panel must say so.
func TestPanelWarnsWhenNothingHasBeenRestored(t *testing.T) {
	st := vayukeep.Status{
		Enabled: true, Target: "/mnt/replica", Generations: 12,
		NewestGen: vkNow.Add(-5 * time.Minute), LastSuccess: vkNow.Add(-5 * time.Minute),
		// LastDrill deliberately zero.
	}
	got := osVayuKeepBody("n", st, "", nil, vkNow, "", false)
	if !strings.Contains(got, "badge--warn") {
		t.Error("a never-verified replica rendered without a warning badge")
	}
	if !strings.Contains(got, "none has been restored yet") {
		t.Errorf("the panel does not say that nothing has been restored:\n%s", got)
	}
	if strings.Contains(got, "badge--ok") {
		t.Error("an unverified replica showed a success badge")
	}
	if !strings.Contains(got, ">never<") {
		t.Error("the last-verified tile must read 'never', not a dash")
	}
}

// TestPanelSurfacesAFailedDrill — a failing restore is an outage of the recovery
// path and has to read like one.
func TestPanelSurfacesAFailedDrill(t *testing.T) {
	st := vayukeep.Status{
		Enabled: true, Target: "/mnt/replica", Generations: 3,
		NewestGen: vkNow.Add(-2 * time.Minute),
		LastDrill: vkNow.Add(-time.Hour), LastDrillOK: false,
		LastDrillError: "integrity_check reported \"malformed database\"",
	}
	got := osVayuKeepBody("n", st, "", nil, vkNow, "", false)
	for _, want := range []string{"Test restore FAILED", "badge--warn", "malformed database"} {
		if !strings.Contains(got, want) {
			t.Errorf("a failed drill must surface %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "badge--ok") {
		t.Error("a failed drill showed a success badge")
	}
}

// TestPanelReportsVerifiedOnlyWhenEarned — the one state allowed to reassure.
func TestPanelReportsVerifiedOnlyWhenEarned(t *testing.T) {
	st := vayukeep.Status{
		Enabled: true, Target: "/mnt/replica", Generations: 9, TotalBytes: 5 << 20,
		NewestGen: vkNow.Add(-3 * time.Minute), LastSuccess: vkNow.Add(-3 * time.Minute),
		LastDrill: vkNow.Add(-20 * time.Minute), LastDrillOK: true, LastDrillRows: 431,
	}
	got := osVayuKeepBody("n", st, "", nil, vkNow, "", false)
	if !strings.Contains(got, "badge--ok") || !strings.Contains(got, "Protected") {
		t.Errorf("a healthy, drilled replica should read as protected:\n%s", got)
	}
	if strings.Contains(got, "badge--warn") {
		t.Errorf("a healthy replica showed a warning:\n%s", got)
	}
	if !strings.Contains(got, "431 posts read back") {
		t.Error("the drill result should state what was actually read back")
	}
}

// TestPanelFlagsAStaleReplica — a target that stopped accepting writes looks
// exactly like a working one unless the age is checked.
func TestPanelFlagsAStaleReplica(t *testing.T) {
	st := vayukeep.Status{
		Enabled: true, Target: "/mnt/replica", Generations: 4,
		NewestGen: vkNow.Add(-72 * time.Hour),
		LastDrill: vkNow.Add(-time.Hour), LastDrillOK: true,
	}
	got := osVayuKeepBody("n", st, "", nil, vkNow, "", false)
	if !strings.Contains(got, "Stale") || !strings.Contains(got, "badge--warn") {
		t.Errorf("a three-day-old newest generation must be flagged stale:\n%s", got)
	}
}

// TestPanelDistinguishesOffFromRefused — "I never set this up" and "I set this
// up and it is not running" need different answers.
func TestPanelDistinguishesOffFromRefused(t *testing.T) {
	off := osVayuKeepBody("n", vayukeep.Status{}, "", nil, vkNow, "", false)
	if !strings.Contains(off, "Not set up") {
		t.Errorf("an unconfigured install should say so:\n%s", off)
	}
	if strings.Contains(off, "Refused to start") {
		t.Error("an unconfigured install must not claim it refused to start")
	}

	refused := osVayuKeepBody("n", vayukeep.Status{}, "the target /var/lib/vayupress/backups is inside the data directory", nil, vkNow, "", false)
	if !strings.Contains(refused, "Refused to start") {
		t.Errorf("a refused configuration must say so:\n%s", refused)
	}
	if !strings.Contains(refused, "nothing is being backed up") {
		t.Error("a refused configuration must state the consequence in plain words")
	}
	if !strings.Contains(refused, "inside the data directory") {
		t.Error("a refused configuration must show the actual reason")
	}
}

// TestPanelReportsPaused — the circuit breaker must be visible, not silent.
func TestPanelReportsPaused(t *testing.T) {
	st := vayukeep.Status{
		Enabled: true, Target: "/mnt/replica", Generations: 2,
		NewestGen: vkNow.Add(-9 * time.Hour),
		Paused:    true, PauseWhy: "paused after 5 consecutive failures — fix the target and re-enable",
		LastError: "no space left on device",
		LastDrill: vkNow.Add(-time.Hour), LastDrillOK: true,
	}
	got := osVayuKeepBody("n", st, "", nil, vkNow, "", false)
	for _, want := range []string{"Paused", "badge--warn", "no space left on device", "Nothing new is being saved"} {
		if !strings.Contains(got, want) {
			t.Errorf("a paused engine must surface %q:\n%s", want, got)
		}
	}
}

// TestPanelEscapesUntrustedText — error strings come from the filesystem and the
// operator's own configuration, so they reach the page as data, never markup.
func TestPanelEscapesUntrustedText(t *testing.T) {
	st := vayukeep.Status{
		Enabled: true, Generations: 1, NewestGen: vkNow,
		Target:         `/mnt/<script>alert(1)</script>`,
		LastDrill:      vkNow,
		LastDrillOK:    false,
		LastDrillError: `<img src=x onerror="alert(2)">`,
		LastError:      `<b>boom</b>`,
	}
	got := osVayuKeepBody("n", st, "", nil, vkNow, "", false)
	// Assert on the injected VALUES, not on generic markup. Two traps here:
	// a bare `onerror=` carries no HTML-special character so it survives escaping
	// as inert text, and the page legitimately contains its own `<script>` block —
	// asserting on either would fail correct code while proving nothing.
	for _, bad := range []string{"<script>alert(1)", "<img src=x", "<b>boom</b>"} {
		if strings.Contains(got, bad) {
			t.Errorf("unescaped %q reached the page:\n%s", bad, got)
		}
	}
	if !strings.Contains(got, "&lt;script&gt;") {
		t.Error("the target should appear escaped rather than dropped")
	}

	refused := osVayuKeepBody("n", vayukeep.Status{}, `<script>alert(3)</script>`, nil, vkNow, "", false)
	if strings.Contains(refused, "<script>alert(3)") {
		t.Errorf("the boot error was not escaped:\n%s", refused)
	}
}

// TestStatsStripNeverShowsAGreenLieWhenOff — the strip is read at a glance and
// must not imply a working replica when there is none.
func TestStatsStripNeverShowsAGreenLieWhenOff(t *testing.T) {
	got := osVayuKeepStats(vayukeep.Status{}, vkNow)
	if strings.Count(got, "stat-card--warn") < 2 {
		t.Errorf("with replication off, both recovery-point and last-verified must warn:\n%s", got)
	}
	healthy := osVayuKeepStats(vayukeep.Status{
		Enabled: true, Generations: 3, NewestGen: vkNow.Add(-time.Minute),
		LastDrill: vkNow.Add(-time.Minute), LastDrillOK: true,
	}, vkNow)
	if strings.Contains(healthy, "stat-card--warn") {
		t.Errorf("a healthy replica should not warn:\n%s", healthy)
	}
}

// TestHumanAgoSaysNeverNotDash — "—" reads as "not applicable"; the difference
// matters when the value means "this has never happened".
func TestHumanAgoSaysNeverNotDash(t *testing.T) {
	if got := humanAgo(time.Time{}, vkNow); got != "never" {
		t.Errorf("humanAgo(zero) = %q, want %q", got, "never")
	}
	for _, c := range []struct {
		d    time.Duration
		want string
	}{
		{10 * time.Second, "just now"},
		{5 * time.Minute, "5 min ago"},
		{3 * time.Hour, "3 h ago"},
		{72 * time.Hour, "3 days ago"},
	} {
		if got := humanAgo(vkNow.Add(-c.d), vkNow); got != c.want {
			t.Errorf("humanAgo(-%s) = %q, want %q", c.d, got, c.want)
		}
	}
}

// TestConsoleIsReachableAndOperable — the page must carry its controls, and the
// Operations hub must carry a badge whenever backups are not actually working.
// A backup console nobody can find, or a hub that looks calm while the recovery
// path is broken, is the failure this whole feature exists to prevent.
func TestConsoleIsReachableAndOperable(t *testing.T) {
	live := osVayuKeepBody("n", vayukeep.Status{
		Enabled: true, Target: "/mnt/replica", Generations: 2, NewestGen: vkNow,
		LastDrill: vkNow, LastDrillOK: true,
	}, "", []vayukeep.Generation{
		{Name: "vk-20260727-120000.vpbk", Taken: vkNow, Bytes: 4 << 20},
	}, vkNow, "/mnt/replica", false)
	for _, want := range []string{
		"data-vk-backup>", "data-vk-drill>", `data-vk-verify="`, `data-vk-restore="`, "data-vk-disable>",
		"/os/api/vayukeep/backup", "/os/api/vayukeep/drill", "/os/api/vayukeep/verify",
		"vk-20260727-120000.vpbk", // the restore point is listed, not just counted
		"vayupress restore",       // the recovery runbook is on the page
	} {
		if !strings.Contains(live, want) {
			t.Errorf("the console is missing %q", want)
		}
	}

	// The hub links to it, and badges it only when something is wrong.
	healthy := osOperationsGrid(mode.ModeNormal, 40, false, "")
	if !strings.Contains(healthy, `href="/os/vayukeep"`) {
		t.Error("the Operations hub does not link to the Backup & Recovery console")
	}
	broken := osOperationsGrid(mode.ModeNormal, 40, false, "Test restore FAILED")
	if !strings.Contains(broken, "Test restore FAILED") {
		t.Error("the Operations hub hides a broken recovery path instead of badging it")
	}
}

// TestSetupIsClickableNotTypable — the whole point of the console. An operator
// must be able to turn backups on without opening a terminal, because the ones
// who never open a terminal are exactly the ones running without backups.
func TestSetupIsClickableNotTypable(t *testing.T) {
	got := osVayuKeepBody("n", vayukeep.Status{}, "", nil, vkNow, "", false)
	// Match the BUTTON, not the selector string. The page's own script contains
	// '[data-vk-setup]' unconditionally, so asserting on the bare attribute name
	// passes even when no form was rendered — which is precisely the bug this
	// test exists to catch.
	for _, want := range []string{
		`id="vk-target"`, `id="vk-pass"`, "data-vk-setup>",
		"/os/api/vayukeep/setup", "Turn on automatic backup",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the setup form is missing %q", want)
		}
	}
	// It must not send the operator to a shell.
	for _, shell := range []string{"systemctl restart vayupress", "/etc/vayupress/env"} {
		if strings.Contains(got, shell) {
			t.Errorf("setup still tells the operator to use a terminal: %q", shell)
		}
	}
	// And it must be honest about the passphrase being unrecoverable.
	if !strings.Contains(got, "There is no reset") {
		t.Error("the form does not warn that a lost passphrase cannot be recovered")
	}
	// The password field must not be a plain text input.
	if !strings.Contains(got, `type="password"`) {
		t.Error("the passphrase field is not a password input")
	}

	// An env-managed install must be told the console will not override it,
	// rather than being shown a form whose Save button silently does nothing.
	env := osVayuKeepBody("n", vayukeep.Status{}, "", nil, vkNow, "/mnt/x", true)
	if strings.Contains(env, "data-vk-setup>") {
		t.Error("an env-managed install was shown a setup form the console cannot apply")
	}
	if !strings.Contains(env, "VAYUKEEP_TARGET") {
		t.Error("an env-managed install is not told where its configuration comes from")
	}
}
