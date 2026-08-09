// SPDX-License-Identifier: Apache-2.0

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// THE CONNECTOR COULD SEE THE FAULT AND NOT THE ATTEMPT TO FIX IT.
//
// vayushield_posture reported every posture row and nothing about the
// remediations, so an operator's assistant could read "Real visitor IP: fail"
// and had no way to learn that the helper had already recorded WHY — nginx
// rejecting a duplicate directive, a header the CDN does not send, a range list
// that came back malformed. Diagnosing a stuck install therefore meant asking
// the operator to read a paragraph back off the page; that happened twice in one
// session, on a fault whose cause the root helper had written down each time.
//
// Read-only, no new capability: the same text the panel renders, to a caller
// that already holds settings:read.

func withFixState(t *testing.T, files map[string]string) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("VAYUSHIELD_CONTROL_DIR", dir)
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func fixRow(t *testing.T, rows []map[string]string, key string) map[string]string {
	t.Helper()
	for _, r := range rows {
		if r["key"] == key {
			return r
		}
	}
	t.Fatalf("remediation %q missing from the report", key)
	return nil
}

func TestTheRemediationReportCarriesTheHelpersOwnReason(t *testing.T) {
	// A helper that advertises the capability, has run, and failed with the
	// message that actually appeared on a live install.
	reason := `nginx rejected the real-IP config; the previous state was restored. ` +
		`nginx: [emerg] "real_ip_header" directive is duplicate in ` +
		`/etc/nginx/conf.d/vayushield-realip.conf:25`
	withFixState(t, map[string]string{
		"agent.caps":    "realip=1\n",
		"realip.state":  "error",
		"realip.reason": reason,
	})

	row := fixRow(t, shieldFixReport(), "realip")
	if row["state"] != "error" {
		t.Errorf("state = %q, want error", row["state"])
	}
	if !strings.Contains(row["reason"], "duplicate") {
		t.Errorf("the reason the helper recorded is not in the report.\n\nThat text is the whole "+
			"diagnosis — without it the caller sees a failure with no cause and has to ask a "+
			"human to read it out.\n\ngot: %q", row["reason"])
	}
	if row["title"] == "" {
		t.Error("no title, so the report cannot be matched to the posture row it remediates")
	}
}

// "Never asked" and "your helper is too old to offer this" are different
// situations with different next steps. Collapsing them into one empty state is
// how somebody ends up hunting for a button that is not rendered.
func TestNeverRunIsDistinguishedFromUnsupported(t *testing.T) {
	withFixState(t, map[string]string{"agent.caps": "realip=1\n"})
	if got := fixRow(t, shieldFixReport(), "realip")["state"]; got != "never-run" {
		t.Errorf("state = %q, want never-run for a supported fix that has never been asked", got)
	}

	// Same fix, a helper that does not advertise it.
	withFixState(t, map[string]string{"agent.caps": "defaulthost=1\n"})
	row := fixRow(t, shieldFixReport(), "realip")
	if row["state"] != "unsupported" {
		t.Errorf("state = %q, want unsupported when the helper does not advertise the capability", row["state"])
	}
	if !strings.Contains(row["reason"], "upgrade") {
		t.Errorf("the unsupported row does not say to upgrade the helper: %q", row["reason"])
	}
}

// Every remediation the panel can render must appear, or the report is a subset
// that looks complete.
func TestEveryRemediationAppearsInTheReport(t *testing.T) {
	withFixState(t, map[string]string{"agent.caps": ""})
	rows := shieldFixReport()
	if len(rows) != len(shieldFixes) {
		t.Fatalf("report has %d rows for %d remediations", len(rows), len(shieldFixes))
	}
	for key := range shieldFixes {
		fixRow(t, rows, key)
	}
	// Stable order: a map ranges differently each call, and a report that
	// reorders itself cannot be diffed against the previous one.
	first := shieldFixReport()
	for i := 0; i < 8; i++ {
		next := shieldFixReport()
		for j := range first {
			if first[j]["key"] != next[j]["key"] {
				t.Fatalf("row %d moved between calls: %q then %q", j, first[j]["key"], next[j]["key"])
			}
		}
	}
}
