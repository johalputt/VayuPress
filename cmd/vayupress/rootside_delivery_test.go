// SPDX-License-Identifier: Apache-2.0

package main

// rootside_delivery_test.go — does the root-side half actually REACH an install?
//
// This file exists because it did not. The VayuVeil hardening worker shipped
// with its Go side tested to death and its script left out of the release
// bundle, so on every existing install the panel would have gone on showing a
// copyable curl-to-root command forever. Every gate was green. The feature was
// in the changelog and on nobody's machine.
//
// The standing rule is that a repair reaching only operators willing to open a
// shell has reached nobody, and the delivery chain that honours it has three
// links: the release workflow packs the helper, the daily worker installs it,
// and a systemd unit watches for the request. A helper missing from any one of
// them is a button that cannot work, and nothing in a Go test suite notices —
// which is exactly why these assertions are about the shell and the workflow.

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func readRepoFile(t *testing.T, rel string) (string, bool) {
	t.Helper()
	b, err := os.ReadFile(filepath.Clean(filepath.Join("../..", rel)))
	if err != nil {
		t.Skipf("%s not readable from here: %v", rel, err)
		return "", false
	}
	return string(b), true
}

// installerHelpers pulls the HELPERS=( ... ) array out of the one-time installer.
func installerHelpers(t *testing.T) []string {
	t.Helper()
	src, ok := readRepoFile(t, "scripts/install-provisioning.sh")
	if !ok {
		return nil
	}
	start := strings.Index(src, "HELPERS=(")
	if start < 0 {
		t.Fatal("install-provisioning.sh no longer declares a HELPERS array; this guard is blind")
	}
	end := strings.Index(src[start:], ")")
	if end < 0 {
		t.Fatal("the HELPERS array is unterminated")
	}
	var out []string
	for _, f := range strings.Fields(src[start+len("HELPERS=(") : start+end]) {
		if strings.HasSuffix(f, ".sh") {
			out = append(out, f)
		}
	}
	if len(out) == 0 {
		t.Fatal("no helpers parsed out of the HELPERS array")
	}
	return out
}

// THE test. Every helper the installer places must also be packed into the
// signed release bundle, because that bundle is the ONLY way a helper reaches an
// install that has already been deployed. A helper the installer knows about and
// the release does not is one that arrives on a fresh install and never on an
// existing one — the shape that shipped and was caught only by unpacking the
// published artifact by hand.
func TestEveryRootSideHelperIsPackedIntoTheReleaseBundle(t *testing.T) {
	helpers := installerHelpers(t)
	workflow, ok := readRepoFile(t, ".github/workflows/tag-release.yml")
	if !ok {
		return
	}
	// The block that builds dist/provision, so a mention of the filename in a
	// comment somewhere else in the workflow cannot satisfy this.
	start := strings.Index(workflow, "mkdir -p dist/provision")
	tarAt := strings.Index(workflow, "vayuprovision-helpers.tar.gz")
	if start < 0 || tarAt < start {
		t.Fatal("the release workflow no longer builds dist/provision before taring it; this guard is blind")
	}
	block := workflow[start:tarAt]

	for _, h := range helpers {
		if !strings.Contains(block, h) {
			t.Errorf("%s is installed by install-provisioning.sh but never copied into "+
				"dist/provision, so it reaches a fresh install and NEVER an existing one", h)
		}
	}
}

// The worker path the binary checks for must be a helper something actually
// delivers. A constant in Go pointing at a file no script installs is a button
// that renders its own install command forever.
func TestTheHardeningWorkerTheBinaryLooksForIsOneThatGetsDelivered(t *testing.T) {
	want := filepath.Base(veilHardenWorkerPath)
	for _, h := range installerHelpers(t) {
		if h == want {
			return
		}
	}
	t.Fatalf("the binary looks for %s, which no script installs", veilHardenWorkerPath)
}

// A helper is only half of it. Its request is consumed by a systemd unit, and
// the self-upgrade path installs SCRIPTS ONLY — so a unit written nowhere but
// the one-time installer never arrives on an install that was deployed before
// the feature existed. Something reachable by the daily sweep has to write it.
func TestTheHardeningWatcherIsWrittenBySomethingTheDailySweepRuns(t *testing.T) {
	sweep, ok := readRepoFile(t, "scripts/provision-subdomains.sh")
	if !ok {
		return
	}
	// Asserting on the unit's NAME is not enough, and this was found by mutating
	// the redirect's target to /dev/null: the name still appears in the unit body
	// and in the is-enabled check, so a sweep that writes the file nowhere passes
	// a contains-check comfortably. What has to be pinned is the DESTINATION.
	written := regexp.MustCompile(`(?m)^\s*cat\s*>\s*` + regexp.QuoteMeta(veilHardenUnitPath) + `\s`)
	if !written.MatchString(sweep) {
		t.Fatalf("the daily provisioning sweep never writes %s itself, so an install that "+
			"predates this feature can only get it by running an installer over SSH", veilHardenUnitPath)
	}
	// The .path unit is useless without the .service it starts.
	svc := strings.TrimSuffix(veilHardenUnitPath, ".path") + ".service"
	if !regexp.MustCompile(`(?m)^\s*cat\s*>\s*` + regexp.QuoteMeta(svc) + `\s`).MatchString(sweep) {
		t.Errorf("the sweep writes the watcher but not %s, which is the unit it starts", svc)
	}
	// And it must be ENABLED, not merely written: a .path unit that exists and is
	// not enabled is the dead-button state — the request is created, nothing
	// consumes it, and the panel says "requested" until it times out.
	if !regexp.MustCompile(`systemctl enable[^\n]*vayupress-veilharden\.path`).MatchString(sweep) {
		t.Error("the watcher is written but never enabled, which is a request nothing consumes")
	}
}

// The sweep must not rewrite the unit it is itself running under. Replacing your
// own unit file mid-run is a distinct and much worse failure than the one this
// change fixes.
func TestTheSweepDoesNotRewriteItsOwnUnit(t *testing.T) {
	sweep, ok := readRepoFile(t, "scripts/provision-subdomains.sh")
	if !ok {
		return
	}
	for _, own := range []string{
		"/etc/systemd/system/vayupress-provision.service",
		"/etc/systemd/system/vayupress-provision.path",
		"/etc/systemd/system/vayupress-provision.timer",
	} {
		for _, line := range strings.Split(sweep, "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "#") || !strings.Contains(trimmed, own) {
				continue
			}
			if strings.Contains(trimmed, ">") || strings.Contains(trimmed, "cat ") {
				t.Errorf("the sweep writes its own unit %s: %s", own, trimmed)
			}
		}
	}
}
