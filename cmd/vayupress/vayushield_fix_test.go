// SPDX-License-Identifier: Apache-2.0

package main

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// postFix drives the handler the button calls.
func postFix(t *testing.T, a *App, fix string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/os/api/shield/fix",
		strings.NewReader(url.Values{"fix": {fix}}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()
	a.handleOSShieldFix(rr, req)
	return rr
}

// TestPostureFixesAreOneClickFromTheConsole is the operator-facing requirement:
// the two posture warnings that previously ended in "now open a terminal" are
// actionable from the panel. A finding a product reports but cannot act on has
// been handed back to the person who came here to avoid the command line.
func TestPostureFixesAreOneClickFromTheConsole(t *testing.T) {
	dir := withControlDir(t)
	a := &App{}
	// A helper advertising both capabilities.
	if err := os.WriteFile(filepath.Join(dir, "agent.caps"),
		[]byte("selfupgrade=1 digest=1 defaulthost=1 mcpsurface=1"), 0o600); err != nil {
		t.Fatalf("caps: %v", err)
	}

	for key, fix := range shieldFixes {
		if rr := postFix(t, a, key); rr.Code != http.StatusOK {
			t.Errorf("%s: status %d, want 200 — the button must record the request", key, rr.Code)
			continue
		}
		if _, err := os.Stat(filepath.Join(dir, fix.Flag)); err != nil {
			t.Errorf("%s: no %s flag written, so the agent will never act: %v", key, fix.Flag, err)
		}
	}
}

// TestFixButtonHiddenWhenTheHelperCannotDoIt — an older agent never reads a flag
// it does not know about, so a button that writes one is a control that silently
// does nothing. That is worse than no button: it spends the operator's trust.
func TestFixButtonHiddenWhenTheHelperCannotDoIt(t *testing.T) {
	dir := withControlDir(t)
	a := &App{}
	if err := os.WriteFile(filepath.Join(dir, "agent.caps"),
		[]byte("selfupgrade=1 digest=1"), 0o600); err != nil {
		t.Fatalf("caps: %v", err)
	}
	for key := range shieldFixes {
		row := shieldFixRow(key)
		if strings.Contains(row, "hx-post") {
			t.Errorf("%s: a button is offered to a helper that cannot act on it:\n%s", key, row)
		}
		if !strings.Contains(row, "predates this fix") {
			t.Errorf("%s: the row does not say why there is no button:\n%s", key, row)
		}
		if rr := postFix(t, a, key); rr.Code != http.StatusConflict {
			t.Errorf("%s: handler returned %d for an incapable helper, want 409", key, rr.Code)
		}
		if _, err := os.Stat(filepath.Join(dir, shieldFixes[key].Flag)); err == nil {
			t.Errorf("%s: a flag was written that nothing will ever read", key)
		}
	}
}

// TestFixRequestCannotChooseAPath is the privilege-separation property, stated
// as a test rather than as a comment.
//
// The agent runs as root; this handler runs unprivileged. If a request could
// influence the FILENAME the agent acts on, an unprivileged process would be
// choosing what a root process reads — the escalation the separation exists to
// prevent. The submitted value is only ever a lookup key into a table of
// constants, so traversal attempts resolve to no fix at all.
func TestFixRequestCannotChooseAPath(t *testing.T) {
	dir := withControlDir(t)
	a := &App{}
	if err := os.WriteFile(filepath.Join(dir, "agent.caps"),
		[]byte("defaulthost=1 mcpsurface=1"), 0o600); err != nil {
		t.Fatalf("caps: %v", err)
	}
	for _, hostile := range []string{
		"../../etc/nginx/nginx", "defaulthost/../../../root/x", "agent.upgrade",
		"tier2", "defaulthost.want", "", ".", "..", "defaulthost\x00",
	} {
		rr := postFix(t, a, hostile)
		if rr.Code != http.StatusBadRequest {
			t.Errorf("fix=%q returned %d, want 400 — only the table's keys may resolve",
				hostile, rr.Code)
		}
	}
	// Nothing outside the two known flags may have appeared.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	for _, e := range entries {
		if e.Name() != "agent.caps" {
			t.Errorf("a hostile request created %q in the control directory", e.Name())
		}
	}
}

// TestEveryFixFlagIsAConstant pins the shape rather than the values. Building a
// control filename by concatenating a request value was flagged by code scanning
// once already on the CDN-allow path; the fix there was to derive the name from
// a constant table, and this holds the same line here.
func TestEveryFixFlagIsAConstant(t *testing.T) {
	src, err := os.ReadFile("vayushield_hardening.go")
	if err != nil {
		t.Skipf("source not readable here: %v", err)
	}
	s := string(src)
	for _, bad := range []string{`fix + ".want"`, `key + ".want"`, `key + ".state"`, `key + ".reason"`} {
		if strings.Contains(s, bad) {
			t.Errorf("a control filename is built by concatenation (%q); derive it from the "+
				"constant table so a request value can never reach a path", bad)
		}
	}
	for key, fix := range shieldFixes {
		if fix.Flag == "" || fix.Cap == "" || fix.Button == "" || fix.Explain == "" {
			t.Errorf("%s: incomplete fix definition %+v — a half-declared control renders a "+
				"button with no explanation of what it will do", key, fix)
		}
		if !strings.HasSuffix(fix.Flag, ".want") {
			t.Errorf("%s: flag %q is not a .want intent file; the agent watches for those",
				key, fix.Flag)
		}
	}
}

// TestHardeningActionsAreNotHiddenByCSS is the regression guard for a control
// that rendered correctly and could not be seen.
//
// `.vs-adv` is an advanced disclosure: display:none until a master toggle above
// it is checked. The Network hardening section has no master toggle, so every
// action row there using the bare class was in the DOM and invisible — including
// the helper-upgrade button, whose own status line reads "press the button
// again". An operator was told to press something that was never on screen.
//
// The variant that is always visible is `vs-adv--open`, which exists for exactly
// this case. Asserted on the source because the alternative is a browser.
func TestHardeningActionsAreNotHiddenByCSS(t *testing.T) {
	src, err := os.ReadFile("vayushield_hardening.go")
	if err != nil {
		t.Skipf("source not readable here: %v", err)
	}
	s := string(src)
	if n := strings.Count(s, `class="vs-adv"`); n != 0 {
		t.Errorf("%d action row(s) in the hardening section use the bare vs-adv class, which is "+
			"display:none without a master toggle — this section has none, so they render "+
			"invisibly. Use \"vs-adv vs-adv--open\".", n)
	}
	// And the two controls that must be reachable are still there.
	for _, want := range []string{
		`/os/api/shield/agent-upgrade`,
		`/os/api/shield/fix`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("the hardening section no longer offers %s", want)
		}
	}
}

// TestVsAdvOpenIsAlwaysVisible pins the CSS contract the fix depends on. If the
// stylesheet stops making vs-adv--open visible, the Go side above still passes
// while the buttons vanish again — the two halves live in different files and
// nothing else connects them.
func TestVsAdvOpenIsAlwaysVisible(t *testing.T) {
	css, err := os.ReadFile("../../static/css/admin-os.css")
	if err != nil {
		t.Skipf("stylesheet not readable here: %v", err)
	}
	c := string(css)
	if !strings.Contains(c, `.vs-adv--open { display: flex; }`) {
		t.Error("vs-adv--open no longer forces display; every always-on action row in the " +
			"hardening section depends on it and would silently disappear")
	}
	if !strings.Contains(c, `.vp-os .vs-adv {`) || !strings.Contains(c, "display: none;") {
		t.Error("the bare vs-adv rule changed shape; re-check whether the --open variant is " +
			"still needed, and whether the Go side still matches")
	}
}

// TestRescueRequestIsJustAFlag holds the repair path to the same one-bit
// contract as every other privileged action here.
//
// The rescue exists because the helper was the only thing that could repair the
// helper: when its upgrade path broke, the panel had no way out and the operator
// needed a shell — for the one component whose purpose is to remove the shell
// from these operations. A root-side systemd path unit watches this file, so a
// wedged daemon is irrelevant. What must NOT change is who chooses: the file is
// empty, and its existence is the whole request.
func TestRescueRequestIsJustAFlag(t *testing.T) {
	dir := withControlDir(t)
	a := &App{}
	req := httptest.NewRequest(http.MethodPost, "/os/api/shield/rescue", nil)
	rr := httptest.NewRecorder()
	a.handleOSShieldRescue(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d, want 200", rr.Code)
	}
	b, err := os.ReadFile(filepath.Join(dir, shieldRescueFlag))
	if err != nil {
		t.Fatalf("no rescue flag written, so the path unit never fires: %v", err)
	}
	if len(b) != 0 {
		t.Errorf("the rescue flag carries %d bytes; it must be empty, because anything a "+
			"root process reads from an unprivileged app is a channel for choosing what runs", len(b))
	}
	// Deliberately NOT capability-gated. An agent too old to advertise "rescue"
	// is exactly the agent most likely to need repairing, and the unit that acts
	// on this is installed independently of it.
	if strings.Contains(shieldRescueRow(), "predates this fix") {
		t.Error("the rescue button hides itself from old helpers — the ones that need it most")
	}
}

// TestRescueUnitsDoNotDependOnTheRunningAgent is the property the whole rescue
// path exists for. Asserted on the unit files because that is where it lives.
func TestRescueUnitsDoNotDependOnTheRunningAgent(t *testing.T) {
	pathUnit, err := os.ReadFile("../../deploy/vayushield-rescue.path")
	if err != nil {
		t.Skipf("unit not readable here: %v", err)
	}
	svcUnit, err := os.ReadFile("../../deploy/vayushield-rescue.service")
	if err != nil {
		t.Skipf("unit not readable here: %v", err)
	}
	p, s := string(pathUnit), string(svcUnit)

	if !strings.Contains(p, "PathExists=/var/lib/vayupress/vayushield-control/"+shieldRescueFlag) {
		t.Errorf("the path unit does not watch the flag the panel writes (%s)", shieldRescueFlag)
	}
	// A dependency on vayushield-agent.service would reintroduce the deadlock:
	// the repair would wait on the thing being repaired.
	for _, bad := range []string{"Requires=vayushield-agent", "After=vayushield-agent", "BindsTo=vayushield-agent"} {
		if strings.Contains(p, bad) || strings.Contains(s, bad) {
			t.Errorf("the rescue units declare %q — a repair that depends on the broken component "+
				"is the deadlock this path exists to break", bad)
		}
	}
	if !strings.Contains(s, "Type=oneshot") {
		t.Error("the rescue service is not oneshot; it must run once per request, not stay resident")
	}
	// Without removing the flag, PathExists re-triggers forever and a failing
	// upgrade becomes a download loop against the release server.
	if !strings.Contains(s, "ExecStartPost=-/bin/rm -f") {
		t.Error("the rescue service never clears the request, so a failure would retrigger " +
			"continuously — which an operator experiences as the panel doing nothing, loudly")
	}
	// Same writable HOME as the agent, or the rescue fails for the very reason it
	// was invoked.
	if !strings.Contains(s, "Environment=HOME=") || !strings.Contains(s, "Environment=TUF_ROOT=") {
		t.Error("the rescue service has no writable HOME/TUF_ROOT; cosign would fail exactly as " +
			"it did in the fault being repaired")
	}
}
