// SPDX-License-Identifier: Apache-2.0

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/shieldaudit"
)

// The helper's version has to be observable, because without it the upgrade
// button cannot be checked.
//
// The dead end, in the field: an operator pressed "Upgrade the helper", the
// posture report went on showing the same warning, and NOTHING anywhere
// distinguished "the helper upgraded and the finding is real" from "the upgrade
// silently did not happen". agent.caps cannot settle it — the capability string
// is byte-identical across releases that change behaviour — so the panel, the
// operator and the person reading the report were all guessing.

// The version stamp must live in the SCRIPT, because a sidecar file cannot work
// on the upgrade that introduces it.
//
// This shipped: self_upgrade installs a fixed list of files, so the code that
// copies a sidecar arrives WITH the version-aware agent — the old installer has
// already run by then. The helper came up reporting "unknown" and the panel
// showed "Upgraded and running" next to "Helper unknown", which is exactly the
// ambiguity the version was added to remove. The script is the one file every
// install path replaces.
func TestTheVersionStampTravelsInsideTheScript(t *testing.T) {
	src, err := os.ReadFile("../../deploy/vayushield-agent.sh")
	if err != nil {
		t.Skipf("agent script not readable here: %v", err)
	}
	s := string(src)
	if !strings.Contains(s, "\nAGENT_VERSION_STAMP=") {
		t.Fatal("the agent carries no in-script version stamp, so a helper upgraded from " +
			"the panel reports its version as unknown until some LATER upgrade installs a sidecar")
	}
	wf, err := os.ReadFile("../../.github/workflows/tag-release.yml")
	if err != nil {
		t.Skipf("release workflow not readable here: %v", err)
	}
	w := string(wf)
	if !strings.Contains(w, "AGENT_VERSION_STAMP") {
		t.Error("the release workflow never rewrites AGENT_VERSION_STAMP, so every helper " +
			"reports itself as dev and the upgrade button stays uncheckable")
	}
	// A stamp that fails to apply must fail the BUILD. Silently shipping a bundle
	// that reports "dev" would put us straight back to guessing.
	if !strings.Contains(w, "the agent version stamp was not applied") {
		t.Error("the release workflow does not verify the stamp landed, so a sed that " +
			"matches nothing ships a helper that cannot report its version")
	}
}

func TestHelperVersionIsUnknownRatherThanInvented(t *testing.T) {
	withControlDir(t) // empty: a helper predating the version stamp
	if got := shieldAgentVersion(); got != "unknown" {
		t.Fatalf("a helper that reports no version must read as %q, got %q — "+
			"an invented number is worse than an absent one", "unknown", got)
	}
}

func TestHelperVersionIsReported(t *testing.T) {
	dir := withControlDir(t)
	if err := os.WriteFile(filepath.Join(dir, "agent.version"), []byte("3.16.30"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := shieldAgentVersion(); got != "3.16.30" {
		t.Fatalf("version = %q, want %q", got, "3.16.30")
	}
}

// The file is on disk and its contents are rendered into the panel. It is
// written by root and read by the app, so this is a defence-in-depth strip
// rather than the only one — the call site escapes as well — but a value that
// reaches HTML unfiltered is not something to leave to one layer.
func TestHelperVersionRefusesAnythingThatIsNotAVersion(t *testing.T) {
	dir := withControlDir(t)
	if err := os.WriteFile(filepath.Join(dir, "agent.version"),
		[]byte("3.16.30<script>alert(1)</script>\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got := shieldAgentVersion()
	for _, bad := range []string{"<", ">", "(", ")", "/"} {
		if strings.Contains(got, bad) {
			t.Fatalf("version %q still carries %q", got, bad)
		}
	}
	if len(got) > 32 {
		t.Fatalf("version %q is %d chars, want it truncated to 32", got, len(got))
	}
}

// A version alone is not the fix — it has to be ON the card, next to the button
// it makes checkable, or it answers a question nobody can see the answer to.
func TestTheUpgradeCardShowsWhichHelperIsRunning(t *testing.T) {
	dir := withControlDir(t)
	if err := os.WriteFile(filepath.Join(dir, "agent.caps"),
		[]byte("selfupgrade=1 digest=1"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "agent.version"), []byte("3.16.28"), 0o600); err != nil {
		t.Fatal(err)
	}
	row := shieldAgentUpgradeRow()
	if !strings.Contains(row, "3.16.28") {
		t.Fatalf("the upgrade card does not show the running helper's version:\n%s", row)
	}
	// Both numbers, or the operator cannot tell that a helper older than the app
	// is why a server-level fix has not landed — which is the whole point.
	if !strings.Contains(row, Version) {
		t.Fatalf("the upgrade card does not show the app version %q for comparison:\n%s", Version, row)
	}
}

// A warning an operator cannot check is a warning they can only re-read.
//
// "The dedicated MCP host is serving more than the MCP and health endpoints"
// went two releases without naming anything. The operator pressed the
// remediation, the sentence did not change, and nobody — them or the person
// reading their screenshot — could tell a stale detector from a real finding.
// The digest now carries the file and line, and the row has to show it.
func TestTheMCPWarningNamesWhatItObjectedTo(t *testing.T) {
	rows := shieldaudit.Run(shieldaudit.Inputs{
		AgentAlive:  true,
		Tier3Wanted: true,
		Digest: shieldaudit.Digest{
			Present:            true,
			MCPVhostRestricted: shieldaudit.No,
			MCPVhostOpenAt:     "/etc/nginx/sites-enabled/mcp.example.test:16",
		},
	})
	var detail string
	for _, r := range rows {
		if r.Title == "MCP host surface" {
			detail = r.Detail
		}
	}
	if detail == "" {
		t.Fatal("no MCP host surface row was produced")
	}
	if !strings.Contains(detail, "/etc/nginx/sites-enabled/mcp.example.test:16") {
		t.Fatalf("the warning does not say WHERE, so it cannot be checked or refuted:\n%s", detail)
	}
}

// ...and it must not invent a location when the agent did not supply one. An
// older helper writes no such key, and a fabricated line number would send an
// operator to a file to look for something that was never claimed.
func TestTheMCPWarningInventsNoLocation(t *testing.T) {
	rows := shieldaudit.Run(shieldaudit.Inputs{
		AgentAlive:  true,
		Tier3Wanted: true,
		Digest: shieldaudit.Digest{
			Present:            true,
			MCPVhostRestricted: shieldaudit.No,
		},
	})
	for _, r := range rows {
		if r.Title == "MCP host surface" && strings.Contains(r.Detail, "catch-all objected to is at") {
			t.Fatalf("a location was claimed with none in the digest:\n%s", r.Detail)
		}
	}
}

// The reference crosses a privilege boundary — root writes it, the unprivileged
// app renders it into the posture report.
func TestConfigReferenceIsNarrowedToWhatAPathCanContain(t *testing.T) {
	got := sanitiseConfigRef(`/etc/nginx/x.conf:12<script>alert("x")</script>`)
	for _, bad := range []string{"<", ">", "(", ")", `"`, " "} {
		if strings.Contains(got, bad) {
			t.Fatalf("reference %q still carries %q", got, bad)
		}
	}
	if !strings.HasPrefix(got, "/etc/nginx/x.conf:12") {
		t.Fatalf("the real reference was mangled: %q", got)
	}
}
