// SPDX-License-Identifier: Apache-2.0

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// FINDING (found verifying the v3.17.48 artifacts) — the published .sha256 files
// could not be checked with sha256sum.
//
// The hashes were correct; the files were unusable. vayupress.sha256 carried the
// BUILD-TIME path ("…  dist/vayupress"), so a user who downloaded the asset as
// "vayupress" got "dist/vayupress: No such file or directory". The other three
// were emitted through `awk '{print $1}'` — a bare hash with no filename at all,
// which sha256sum -c rejects outright with "no properly formatted checksum lines
// found".
//
// A verification artifact that cannot verify is the same class as a gate that
// does not gate: the user concludes their download is corrupt, or gives up on
// checking. Both are worse than publishing nothing.

// releaseWorkflow returns the release workflow with its COMMENT LINES REMOVED.
//
// Stripping them is not tidiness. The assertions below look for the old, broken
// forms — and the comments explaining why those forms were wrong quote them
// verbatim. Matching raw text finds the explanation and reports the defect it
// documents, which is the third time in this track that a source scan has failed
// to tell code from the prose describing it (the heredoc audit and the
// version-file self-check were the others).
func releaseWorkflow(t *testing.T) string {
	t.Helper()
	var b strings.Builder
	for _, ln := range strings.Split(repoFile(t, ".github/workflows/tag-release.yml"), "\n") {
		if strings.HasPrefix(strings.TrimSpace(ln), "#") {
			continue
		}
		b.WriteString(ln)
		b.WriteByte('\n')
	}
	return b.String()
}

// Every checksum the release publishes must name the file it covers, under the
// name that file is downloaded as.
func TestPublishedChecksumsNameTheFileTheyCover(t *testing.T) {
	wf := releaseWorkflow(t)

	// The bare-hash pipeline must be gone from every asset.
	if strings.Contains(wf, `| awk '{print $1}' > dist/`) {
		t.Error("a checksum is still emitted as a bare hash. sha256sum -c cannot read a file " +
			"with no filename column, so the artifact published for verification cannot verify")
	}
	// And no checksum may carry the build-time directory into the file.
	if strings.Contains(wf, "sha256sum dist/") {
		t.Error("a checksum is computed as `sha256sum dist/<name>`, which writes 'dist/<name>' " +
			"into the file — a path that does not exist for whoever downloaded the asset")
	}
	for _, asset := range []string{
		"vayupress", "vayuprovision-helpers.tar.gz",
		"vayupress-selfhosted-site.zip", "vayushield-agent.tar.gz",
	} {
		want := "(cd dist && sha256sum " + asset + " > " + asset + ".sha256)"
		if !strings.Contains(wf, want) {
			t.Errorf("%s's checksum is not emitted from inside dist/, so the name in the file "+
				"will not match the name the asset is downloaded under", asset)
		}
	}
}

// The format and the command that CONSUMES it must move together.
//
// shieldAgentBootstrapCmd is a root command an operator pastes into a shell. It
// used to hand-assemble the missing filename ("$(cat sum)  <name>") precisely
// because the release emitted a bare hash. Fixing the format without fixing this
// would append the name twice and break the install; fixing this without the
// format would check nothing.
func TestTheAgentBootstrapReadsTheChecksumFormatWePublish(t *testing.T) {
	cmd := shieldAgentBootstrapCmd()
	if strings.Contains(cmd, "$(cat sum)") {
		t.Fatal("the agent bootstrap still hand-assembles a checksum line. The release now " +
			"publishes '<hash>  <name>', so this appends the filename twice and sha256sum -c " +
			"fails on a good download")
	}
	if !strings.Contains(cmd, "sha256sum -c vayushield-agent.tar.gz.sha256") {
		t.Error("the agent bootstrap no longer checks the tarball against its published checksum")
	}
}

// The end-to-end property, proven with the real tool rather than asserted: a file
// written the way the workflow writes it verifies where it lands.
func TestAChecksumWrittenTheWorkflowsWayVerifiesAfterDownload(t *testing.T) {
	if _, err := exec.LookPath("sha256sum"); err != nil {
		t.Skip("sha256sum not available")
	}
	build := t.TempDir() // stands in for dist/
	if err := os.WriteFile(filepath.Join(build, "vayupress"), []byte("pretend binary"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Exactly the workflow's form: computed from inside the build directory.
	out, err := exec.Command("sh", "-c", "cd "+build+" && sha256sum vayupress > vayupress.sha256").CombinedOutput()
	if err != nil {
		t.Fatalf("emit checksum: %v: %s", err, out)
	}

	// Now the user's side: the two files, alone, in a different directory.
	download := t.TempDir()
	for _, n := range []string{"vayupress", "vayupress.sha256"} {
		b, rerr := os.ReadFile(filepath.Join(build, n))
		if rerr != nil {
			t.Fatal(rerr)
		}
		if werr := os.WriteFile(filepath.Join(download, n), b, 0o600); werr != nil {
			t.Fatal(werr)
		}
	}
	if out, err = exec.Command("sh", "-c", "cd "+download+" && sha256sum -c vayupress.sha256").CombinedOutput(); err != nil {
		t.Fatalf("a user checking their download failed: %v: %s", err, out)
	}
	if !strings.Contains(string(out), "vayupress: OK") {
		t.Errorf("sha256sum -c did not report OK: %s", out)
	}
}
