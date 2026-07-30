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
