// SPDX-License-Identifier: Apache-2.0

package main

// veilharden_page_test.go — ADR-0150 §5 S6, the surface.
//
// A page that offers to harden a server has two ways to be wrong that a working
// test suite would otherwise miss: it can claim a control the process does not
// have, and it can offer a button that does nothing. Both are asserted here, and
// each assertion extracts THE card before reading it — a whole-page search for a
// class or a phrase passes on any page that uses it elsewhere.

import (
	"strings"
	"testing"
	"time"

	"github.com/johalputt/vayupress/internal/vayuveil"
)

func veilHardenSandboxed() vayuveil.SandboxState {
	return vayuveil.SandboxState{
		Supported:  true,
		NoNewPrivs: true, NoNewPrivsKnown: true,
		PrivateDev: true, PrivateDevKnown: true,
		PrivateTmp: true, PrivateTmpKnown: true,
		ProtectedHome: true, ProtectedHomeKnown: true,
		SwapMaxZero: true, SwapMaxKnown: true,
	}
}

func veilHardenBare() vayuveil.SandboxState {
	return vayuveil.SandboxState{
		Supported: true, NoNewPrivsKnown: true, PrivateDevKnown: true,
		PrivateTmpKnown: true, ProtectedHomeKnown: true, SwapMaxKnown: true,
	}
}

// THE assertion this file exists for. A drop-in written after the process
// started must not produce a card that reads as though anything is now
// protected — the operator has to know a restart is what makes it real.
func TestTheCardNeverSaysAppliedBeforeTheKernelSaysSo(t *testing.T) {
	start := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)
	card := veilHardenCard(vayuveil.HardenState{
		Installed: true, HaveResult: true, DropInPresent: true, DropInAt: start.Add(time.Minute),
		Wrote: []string{"NoNewPrivileges=yes"},
	}, veilHardenBare(), start)

	for _, forbidden := range []string{
		"hardening is applied", "now hardened", "is now in force", "successfully hardened",
	} {
		if strings.Contains(strings.ToLower(card), forbidden) {
			t.Errorf("the card claims %q while the process does not have it", forbidden)
		}
	}
	if !strings.Contains(card, "awaiting restart") {
		t.Error("the collapsed chip must say awaiting restart, so the state reads without opening the card")
	}
	// The written list is still shown — the operator asked for something and is
	// owed a record of what the worker did — but as a record, not as a verdict.
	if !strings.Contains(card, "Written into the drop-in") {
		t.Error("what the worker wrote must still be reported")
	}
}

// A button that cannot do anything must not be rendered. A dead control is worse
// than no control: it converts a fixable setup gap into a mystery.
func TestTheRequestButtonOnlyAppearsWhenThereIsSomethingToRequest(t *testing.T) {
	start := time.Now()

	// Worker not installed: the one command that genuinely needs root, shown with
	// its reason — never a button that writes a request nothing consumes.
	notInstalled := veilHardenCard(vayuveil.HardenState{}, veilHardenBare(), start)
	if strings.Contains(notInstalled, "data-veilharden-run") {
		t.Error("a request button was rendered with no root-side worker to consume the request")
	}
	if !strings.Contains(notInstalled, "data-veilharden-cmd") {
		t.Error("the install command must be shown, copyable, when the worker is missing")
	}

	// Everything already in force: nothing to ask for.
	done := veilHardenCard(vayuveil.HardenState{Installed: true}, veilHardenSandboxed(), start)
	if strings.Contains(done, "data-veilharden-run") {
		t.Error("a request button was rendered on an install that already has every directive")
	}

	// Installed, and something missing: the button appears.
	actionable := veilHardenCard(vayuveil.HardenState{Installed: true}, veilHardenBare(), start)
	if !strings.Contains(actionable, "data-veilharden-run") {
		t.Error("no request button on an install that is missing directives and can consume a request")
	}
}

// The consequences an operator should read BEFORE clicking are on the card, not
// in a changelog: the restart, the auto-revert, and what MemorySwapMax=0 costs.
func TestTheCardStatesWhatClickingCosts(t *testing.T) {
	card := veilHardenCard(vayuveil.HardenState{Installed: true}, veilHardenBare(), time.Now())
	for _, required := range []string{
		"The service restarts to apply this",
		"removes the drop-in and restarts it without one",
		"killed rather than swapped",
	} {
		if !strings.Contains(card, required) {
			t.Errorf("the card does not state %q before offering the button", required)
		}
	}
}

// The refusals are rendered. They are the half of the page that makes the other
// half worth believing, and ProtectSystem is the one that can break an install.
func TestTheCardPublishesWhatItRefusesToWrite(t *testing.T) {
	card := veilHardenCard(vayuveil.HardenState{Installed: true}, veilHardenBare(), time.Now())
	if !strings.Contains(card, "What this will not write") {
		t.Fatal("the refusal section is missing")
	}
	if !strings.Contains(card, "ProtectSystem=strict") {
		t.Error("ProtectSystem=strict must be named as refused — it is the directive that breaks installs")
	}
	for _, ref := range vayuveil.HardenRefusals() {
		if !strings.Contains(card, ref.Directive) {
			t.Errorf("refusal %q is not rendered", ref.Directive)
		}
	}
}

// A skip is reported with its reason. A worker that quietly left a directive out
// and reported success is the same defect as a probe that skips what it cannot
// do and calls the sweep clean.
func TestASkippedDirectiveIsShownWithItsReason(t *testing.T) {
	const reason = "ProtectHome=yes — this install's data directory is /home/vayu, which ProtectHome would make unreadable."
	card := veilHardenCard(vayuveil.HardenState{
		Installed: true, HaveResult: true, DropInPresent: true, DropInAt: time.Now(),
		Wrote: []string{"NoNewPrivileges=yes"}, Skipped: []string{reason},
	}, veilHardenBare(), time.Now())

	if !strings.Contains(card, "Left out, with the reason") {
		t.Fatal("skips are not reported at all")
	}
	// html.EscapeString turns the apostrophe into &#39;, so assert on a fragment
	// that survives escaping rather than on the raw sentence — a test that reads
	// the unescaped string fails on correct output.
	if !strings.Contains(card, "which ProtectHome would make unreadable") {
		t.Error("the skip reason is not rendered")
	}
}

// The whole card must survive the CSP the panel serves under: no inline style
// attribute, no external origin.
func TestTheHardeningCardIsCSPSafe(t *testing.T) {
	assertCSPSafe(t, "veil hardening card",
		veilHardenCard(vayuveil.HardenState{Installed: true}, veilHardenBare(), time.Now()))
}

// When the worker is absent, the page must lead with the fact that it ARRIVES ON
// ITS OWN, not with a command. The daily provisioning sweep installs it from the
// signed release bundle; sending an operator to a terminal for something already
// on its way is the standing failure in its politest form.
func TestTheMissingWorkerCardLeadsWithTheAutomaticPathNotTheCommand(t *testing.T) {
	card := veilHardenCard(vayuveil.HardenState{}, veilHardenBare(), time.Now())

	sweepAt := strings.Index(card, "daily sweep installs it on its own")
	cmdAt := strings.Index(card, "data-veilharden-cmd")
	if sweepAt < 0 {
		t.Fatal("the card never says the worker arrives on its own")
	}
	if cmdAt < 0 {
		t.Fatal("the copyable command is gone; an install without provisioning would have no path at all")
	}
	if sweepAt > cmdAt {
		t.Error("the card offers the terminal command before mentioning that it arrives on its own")
	}
}
