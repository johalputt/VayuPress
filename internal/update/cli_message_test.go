// SPDX-License-Identifier: Apache-2.0

package update

import (
	"strings"
	"testing"
)

// The CLI's dry-run line once said "checksum + Ed25519 signature" on every
// install, after the Ed25519 env pin it described had been removed and while
// the Sigstore signature that actually decided the install went unnamed. What
// it must name is stated here independently of verifiedControls: Sigstore
// always, the release key exactly when this build compiles one in.
func TestTheDryRunLineNamesTheSignaturesItChecked(t *testing.T) {
	got := verifiedControls()
	if !strings.Contains(got, "Sigstore signature") {
		t.Fatalf("dry-run line %q does not name the Sigstore signature, which every apply requires", got)
	}
	if want := ReleaseRequiresEd25519(); strings.Contains(got, "release key") != want {
		t.Fatalf("dry-run line %q: mentions the release key = %t, but this build requires it = %t",
			got, !want, want)
	}
}
