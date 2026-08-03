// SPDX-License-Identifier: Apache-2.0

package main

// dep_freshness_test.go — the dependency-freshness check, driven end to end.
//
// The script shells out to `go list`, so it is tested by putting a STUB `go` on
// PATH and scripting its answers. That exercises the real file — its
// classification, its exit status and the words it puts in front of a human —
// rather than a Go reimplementation of what it is believed to do.
//
// Why it is tested at all: the check is the repo's only standing signal that a
// dependency is behind, and it printed "All Go modules are up to date" while
// two direct dependencies had newer majors it had never looked for. A green
// badge trusted for more than it verifies is worse than no badge.

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// runFreshness executes the real script with a stubbed `go`.
//
// stub is the body of a shell script installed as `go` on PATH; it receives the
// script's real arguments and answers them.
func runFreshness(t *testing.T, stub string) (out string, code int) {
	t.Helper()
	bin := t.TempDir()
	shim := "#!/usr/bin/env bash\n" + stub + "\n"
	if err := os.WriteFile(filepath.Join(bin, "go"), []byte(shim), 0o755); err != nil { //nolint:gosec // a test fixture on PATH must be executable
		t.Fatal(err)
	}
	script, err := filepath.Abs("../../scripts/dep-freshness.sh")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("bash", script)
	// PATH is the stub ALONE. Leaking the real toolchain in would let a passing
	// test depend on the network and on whatever the upstream proxy says today,
	// which is the opposite of a fixture.
	// PREPENDED, not replacing: the stub shadows the real `go`, which is all that
	// is needed, while dirname/mktemp/sort stay reachable. Replacing PATH
	// outright removed those too and the script died before reaching anything
	// this file asserts about.
	cmd.Env = append(os.Environ(), "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"),
		"GITHUB_STEP_SUMMARY=")
	b, err := cmd.CombinedOutput()
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatalf("running the script: %v\n%s", err, b)
	}
	return string(b), code
}

// listStub answers the two `go list` shapes the script uses:
// the -u outdated sweep, and the direct-module enumeration. Everything else is
// a major-version probe, answered by probeCases against "$last".
//
// probeCases matches "$last" — the FINAL argument — rather than a positional
// one. The probe runs `go list -m -f '{{.Version}}' <path>@latest`, so the path
// is $5, not $3; a fixture matching $3 silently answers nothing and every probe
// assertion passes against a script that was never asked anything.
func listStub(outdated, directs string, probeCases string) string {
	return `
args="$*"
last="${!#}"
case "$args" in
  *"-u -m -f"*) printf '%s' ` + shQuote(outdated) + `; exit 0 ;;
  *"not .Indirect"*) printf '%s' ` + shQuote(directs) + `; exit 0 ;;
esac
` + probeCases + `
echo "go: module $last: not found" >&2
exit 1`
}

func shQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'" }

// A direct dependency behind its latest release must go RED. That is the whole
// point of the check, and it is asserted first so a change that makes the script
// permanently green cannot pass.
func TestADirectDependencyBehindTurnsTheCheckRed(t *testing.T) {
	out, code := runFreshness(t, listStub(
		"github.com/phuslu/iploc|v1.0.20260715|v1.0.20260802|direct\n",
		"github.com/phuslu/iploc v1.0.20260715\n", ""))
	if code == 0 {
		t.Fatalf("a direct dependency behind latest exited 0 — the one signal this check "+
			"exists to give:\n%s", out)
	}
	if !strings.Contains(out, "v1.0.20260802") {
		t.Errorf("the output never names the version to move to:\n%s", out)
	}
}

// An indirect dependency behind must NOT go red: it updates via its parent, and
// a check that is red for something nobody can act on is one people stop reading.
func TestAnIndirectDependencyAloneDoesNotTurnTheCheckRed(t *testing.T) {
	out, code := runFreshness(t, listStub(
		"github.com/aws/smithy-go|v1.22.2|v1.27.6|indirect\n",
		"github.com/phuslu/iploc v1.0.20260802\n", ""))
	if code != 0 {
		t.Fatalf("an indirect-only drift failed the check:\n%s", out)
	}
	if !strings.Contains(out, "informational") {
		t.Errorf("the indirect list is not marked informational:\n%s", out)
	}
}

// FINDING — `go list -u` cannot see a new MAJOR, because in Go that is a
// different module path. The script therefore printed "All Go modules are up to
// date" over two direct dependencies with newer majors it had never looked for.
func TestANewerStableMajorIsReportedAndDoesNotFailTheCheck(t *testing.T) {
	probe := `
case "$last" in
  "github.com/go-chi/chi/v6@latest") echo "v6.1.0"; exit 0 ;;
esac`
	out, code := runFreshness(t, listStub("", "github.com/go-chi/chi/v5 v5.3.1\n", probe))

	if !strings.Contains(out, "github.com/go-chi/chi/v6") || !strings.Contains(out, "v6.1.0") {
		t.Fatalf("a newer stable major was never reported, so the check calls the repo up to "+
			"date on a claim it does not make:\n%s", out)
	}
	// Reported, not enforced. A major bump is a migration somebody schedules; a
	// check that stays red until unrelated work is done gets ignored, and then it
	// is red for a real reason and nobody looks.
	if code != 0 {
		t.Errorf("a newer major failed the check; it is a migration to schedule, not drift:\n%s", out)
	}
	if strings.Contains(out, "All Go modules are up to date") {
		t.Error("the all-clear still fires with a newer stable major outstanding")
	}
}

// FINDING FROM THE FIRST RUN of the probe above: both majors it discovered were
// PRE-RELEASE — goldmark v2.0.0-beta.9 and chroma v3.0.0-alpha.5. Listing those
// as "available" tells an operator to migrate a production markdown renderer
// onto a beta, and it would keep the summary permanently non-clean for work
// nobody should do. Nobody is behind on a major with no stable release.
func TestAPreReleaseMajorIsNotCountedAsBeingBehind(t *testing.T) {
	probe := `
case "$last" in
  "github.com/yuin/goldmark/v2@latest") echo "v2.0.0-beta.9"; exit 0 ;;
esac`
	out, code := runFreshness(t, listStub("", "github.com/yuin/goldmark v1.8.5\n", probe))

	if code != 0 {
		t.Fatalf("a pre-release major failed the check:\n%s", out)
	}
	if !strings.Contains(out, "PRE-RELEASE") || !strings.Contains(out, "v2.0.0-beta.9") {
		t.Errorf("the pre-release major is not surfaced at all, so the migration is a surprise "+
			"later:\n%s", out)
	}
	if strings.Contains(out, "NEWER MAJOR available") {
		t.Fatal("a beta is being presented as a version this repo is behind on")
	}
	// And it must not hold the all-clear hostage, or green never returns.
	if !strings.Contains(out, "up to date") {
		t.Errorf("a pre-release-only major suppressed the all-clear:\n%s", out)
	}
}

// A probe that could not REACH the proxy must claim nothing, in either
// direction. Reading a network failure as "this major does not exist" turns
// every probe into a silent pass and puts the green badge back on a claim
// nothing verified — the defect this section was added to remove, reintroduced
// by the code removing it.
func TestAnUnreachableProxyClaimsNothingRatherThanClean(t *testing.T) {
	probe := `
case "$last" in
  *"@latest") echo "go: dial tcp: lookup proxy.golang.org: i/o timeout" >&2; exit 1 ;;
esac`
	out, code := runFreshness(t, listStub("", "github.com/go-chi/chi/v5 v5.3.1\n", probe))
	if code != 0 {
		t.Fatalf("an unreachable proxy failed the whole check; it is a gap in knowledge, not "+
			"drift:\n%s", out)
	}
	if !strings.Contains(out, "nothing is claimed") {
		t.Fatalf("a network failure was reported as a clean major probe, so the summary asserts "+
			"something it never checked:\n%s", out)
	}
}

// The all-clear must still be reachable, or the check has no green state and
// every one of the assertions above is describing a permanently red badge.
func TestAFullyCurrentRepoReportsClean(t *testing.T) {
	out, code := runFreshness(t, listStub("", "github.com/go-chi/chi/v5 v5.3.1\n", ""))
	if code != 0 {
		t.Fatalf("a fully current repo failed the check:\n%s", out)
	}
	if !strings.Contains(out, "All Go modules are up to date") {
		t.Errorf("no all-clear was printed:\n%s", out)
	}
}
