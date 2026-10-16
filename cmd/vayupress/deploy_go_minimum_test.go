// SPDX-License-Identifier: Apache-2.0

package main

import (
	"os"
	"regexp"
	"testing"
)

// The deploy script installs Go only when the host's is older than its
// minimum, so that minimum must be go.mod's go line: below it, a host with an
// older Go skips the install and the build fails. It said 1.22 while go.mod
// required 1.26, and a dependency bump raised go.mod to 1.27.
func TestTheDeployScriptsGoMinimumIsGoMods(t *testing.T) {
	mod, err := os.ReadFile("../../go.mod")
	if err != nil {
		t.Fatal(err)
	}
	script, err := os.ReadFile("../../scripts/deploy-vayupress.sh")
	if err != nil {
		t.Fatal(err)
	}
	want := regexp.MustCompile(`(?m)^go (\d+)\.(\d+)`).FindSubmatch(mod)
	major := regexp.MustCompile(`(?m)^GO_MIN_MAJOR=(\d+)`).FindSubmatch(script)
	minor := regexp.MustCompile(`(?m)^GO_MIN_MINOR=(\d+)`).FindSubmatch(script)
	if want == nil || major == nil || minor == nil {
		t.Fatal("go.mod's go line or the script's GO_MIN_MAJOR/GO_MIN_MINOR is not where this test reads it")
	}
	if string(major[1]) != string(want[1]) || string(minor[1]) != string(want[2]) {
		t.Errorf("deploy-vayupress.sh accepts Go %s.%s; go.mod requires %s.%s", major[1], minor[1], want[1], want[2])
	}
}
