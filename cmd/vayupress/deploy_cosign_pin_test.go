// SPDX-License-Identifier: Apache-2.0

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// cosignPinHarness exercises the agent's ensure_cosign against a fake release
// server, with real bytes and a real sha256sum, so the checksum gate is executed
// rather than asserted about.
//
// ensure_cosign is extracted from the shipped script at run time, so this test
// cannot drift from the code it covers — a copy of the function here would keep
// passing after the real one changed.
const cosignPinHarness = `
set -uo pipefail
D=$(mktemp -d); LIB="$D/lib"; mkdir -p "$LIB"
printf 'genuine-cosign-binary' > "$D/good"
GOOD=$(sha256sum "$D/good" | awk '{print $1}')
printf 'MALICIOUS' > "$D/bad"
sed -n '/^ensure_cosign() {/,/^}/p' "$AGENT" > "$D/fn.sh"
[ -s "$D/fn.sh" ] || { echo "FAIL ensure_cosign not found in the agent script"; exit 1; }

attempt() { # $1=served file, $2=pinned sum -> prints ACCEPTED/REFUSED
  printf 'version=v9.9.9\namd64=%s\narm64=%s\n' "$2" "$2" > "$LIB/cosign.pin"
  rm -f "$LIB/cosign"
  SERVE="$1" LIB_DIR="$LIB" FN="$D/fn.sh" bash -c '
    set -uo pipefail
    COSIGN_PIN="${LIB_DIR}/cosign.pin"; COSIGN="cosign"; COSIGN_BOOTSTRAP_WHY=""
    command() { return 1; }
    curl() { local dst=""; while [ $# -gt 0 ]; do [ "$1" = "-o" ] && { dst="$2"; shift; }; shift; done; cp "$SERVE" "$dst"; }
    uname() { echo x86_64; }
    source "$FN"
    if ensure_cosign; then echo "ACCEPTED"; else echo "REFUSED"; fi'
}

[ "$(attempt "$D/good" "$GOOD")" = "ACCEPTED" ] || { echo "FAIL a cosign matching its pin was refused"; exit 1; }
[ -s "$LIB/cosign" ] && [ "$(cat "$LIB/cosign")" = "genuine-cosign-binary" ] || { echo "FAIL the verified bytes were not installed"; exit 1; }

[ "$(attempt "$D/bad" "$GOOD")" = "REFUSED" ] || { echo "FAIL a SUBSTITUTED cosign passed the checksum gate"; exit 1; }
[ -e "$LIB/cosign" ] && { echo "FAIL substituted bytes were installed anyway"; exit 1; }

# No pin at all must refuse rather than fetch something unverified.
rm -f "$LIB/cosign.pin"
r=$(LIB_DIR="$LIB" FN="$D/fn.sh" bash -c '
  set -uo pipefail
  COSIGN_PIN="${LIB_DIR}/cosign.pin"; COSIGN="cosign"; COSIGN_BOOTSTRAP_WHY=""
  command() { return 1; }
  source "$FN"
  if ensure_cosign; then echo ACCEPTED; else echo REFUSED; fi')
[ "$r" = "REFUSED" ] || { echo "FAIL an unpinned helper downloaded cosign anyway"; exit 1; }
echo OK
`

// TestCosignPinRefusesSubstitutedBytes is the security property of the
// bootstrap: the agent may fetch cosign for itself, but only bytes matching a
// checksum pinned at release build time — inside a bundle that is itself
// cosign-signed, so a verified agent carries the fingerprint of the tool that
// verifies the next one.
//
// The alternative that was NOT taken: falling back to the agent bundle's own
// published SHA-256 when cosign is missing. That checksum ships beside the
// artifact it describes, so it catches corruption and not substitution — and the
// panel would still have said "signature verified".
func TestCosignPinRefusesSubstitutedBytes(t *testing.T) {
	agent, err := filepath.Abs("../../deploy/vayushield-agent.sh")
	if err != nil {
		t.Skipf("cannot resolve the agent script: %v", err)
	}
	if _, err := os.Stat(agent); err != nil {
		t.Skipf("agent script not present here: %v", err)
	}
	cmd := exec.Command("bash", "-c", cosignPinHarness) //nolint:gosec // fixed literal harness
	cmd.Env = append(os.Environ(), "AGENT="+agent)
	out, err := cmd.CombinedOutput()
	got := strings.TrimSpace(string(out))
	if err != nil || !strings.HasSuffix(got, "OK") {
		t.Fatalf("cosign pin gate: %s (err %v)", got, err)
	}
}

// TestAgentAdvertisesWhatItCanDo keeps the capability string honest. The panel
// decides whether to offer a button from this list, so a capability claimed but
// not implemented renders a control that silently does nothing — the failure
// mode the capability gate exists to prevent in the first place.
func TestAgentAdvertisesWhatItCanDo(t *testing.T) {
	src, err := os.ReadFile("../../deploy/vayushield-agent.sh")
	if err != nil {
		t.Skipf("agent script not readable here: %v", err)
	}
	s := string(src)
	for cap, proof := range map[string]string{
		"selfupgrade=1": "self_upgrade()",
		"defaulthost=1": "reconcile_defaulthost()",
		"mcpsurface=1":  "reconcile_mcpsurface()",
		"cosignpin=1":   "ensure_cosign()",
	} {
		if !strings.Contains(s, cap) {
			t.Errorf("the agent no longer advertises %q", cap)
			continue
		}
		if !strings.Contains(s, proof) {
			t.Errorf("the agent advertises %q but has no %s — the panel would offer a button "+
				"for something nothing implements", cap, proof)
		}
	}
}
