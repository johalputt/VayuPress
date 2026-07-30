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

// nginxReasonHarness runs the agent's own nginx helpers against stubbed nginx
// output, so what the panel would show is produced rather than described.
const nginxReasonHarness = `
set -uo pipefail
D=$(mktemp -d)
sed -n '/^nginx_has_reject_handshake() {/,/^}/p' "$AGENT" > "$D/v.sh"
sed -n '/^nginx_try_reload() {/,/^}/p'          "$AGENT" > "$D/r.sh"
[ -s "$D/v.sh" ] && [ -s "$D/r.sh" ] || { echo "FAIL helpers not found in the agent script"; exit 1; }

check_ver() { # $1=version $2=expect(yes|no)
  r=$(V="$1" bash -c 'nginx() { echo "nginx version: nginx/$V" >&2; }; source "'"$D"'/v.sh"; if nginx_has_reject_handshake; then echo yes; else echo no; fi')
  [ "$r" = "$2" ] || { echo "FAIL nginx $1 -> $r, want $2"; exit 1; }
}
# ssl_reject_handshake landed in 1.19.4 and the catch-all cannot exist without
# it: that directive is what allows a TLS server block with no certificate.
check_ver 1.18.0 no
check_ver 1.19.3 no
check_ver 1.19.4 yes
check_ver 1.24.0 yes
check_ver 2.0.0  yes

# nginx's own words must reach the operator. Reporting only "nginx rejected it"
# sends them to a terminal, which is the thing this surface exists to avoid.
out=$(bash -c 'NGINX_TRY_WHY=""
  nginx() { echo "nginx: [emerg] unknown directive \"ssl_reject_handshake\" in /etc/nginx/conf.d/x.conf:5"; return 1; }
  systemctl() { return 0; }
  source "'"$D"'/r.sh"
  f=$(mktemp); nginx_try_reload "$f" "" || true
  printf "%s" "$NGINX_TRY_WHY"')
case "$out" in
  *"unknown directive"*) ;;
  *) echo "FAIL nginx's rejection reason was discarded: [$out]"; exit 1 ;;
esac
# The catch-all must work on nginx older than 1.19.4 by borrowing a certificate
# the host already serves. Telling an operator on a healthy LTS release to
# upgrade their web server is not an answer, it is the problem restated.
sed -n '/^nginx_first_certificate() {/,/^}/p' "$AGENT" > "$D/c.sh"
[ -s "$D/c.sh" ] || { echo "FAIL nginx_first_certificate not found"; exit 1; }
mkdir -p "$D/certs"; : > "$D/certs/real.pem"; : > "$D/certs/real.key"
cat > "$D/dump.txt" <<DUMP
server {
    ssl_certificate /nonexistent/missing.pem;
    ssl_certificate_key /nonexistent/missing.key;
}
server {
    ssl_certificate $D/certs/real.pem;
    ssl_certificate_key $D/certs/real.key;
}
DUMP
got=$(bash -c 'nginx() { cat "'"$D"'/dump.txt"; }; source "'"$D"'/c.sh"; nginx_first_certificate' || true)
[ "$got" = "$D/certs/real.pem $D/certs/real.key" ] || { echo "FAIL cert pick: [$got]"; exit 1; }

# ssl_certificate is a PREFIX of ssl_certificate_key. Matching without a
# following space collects both into the cert list, shifting it out of alignment
# with the key list and pairing a certificate with the wrong key.
n=$(sed -n 's/^[[:space:]]*ssl_certificate[[:space:]]\{1,\}\([^;]*\);.*/\1/p' "$D/dump.txt" | wc -l)
[ "$n" = "2" ] || { echo "FAIL ssl_certificate matched $n lines, want 2 — it is aliasing ssl_certificate_key"; exit 1; }

printf 'server {\n ssl_certificate /nope/a.pem;\n ssl_certificate_key /nope/a.key;\n}\n' > "$D/dump2.txt"
if bash -c 'nginx() { cat "'"$D"'/dump2.txt"; }; source "'"$D"'/c.sh"; nginx_first_certificate' >/dev/null 2>&1; then
  echo "FAIL an unreadable certificate was offered for the catch-all"; exit 1
fi
echo OK
`

// TestNginxFailuresExplainThemselves covers the half of a remediation that only
// matters when it goes wrong.
//
// An operator pressed "Add the catch-all server" and got "nginx rejected the
// catch-all server; the previous config was restored" — true, and useless. The
// rollback worked; the explanation did not exist. nginx emits a precise,
// line-numbered reason and the helper was throwing it away with >/dev/null,
// which is the same defect as the 160-character cosign log: a panel that reports
// failure without cause has delegated the diagnosis to a shell.
func TestNginxFailuresExplainThemselves(t *testing.T) {
	agent, err := filepath.Abs("../../deploy/vayushield-agent.sh")
	if err != nil {
		t.Skipf("cannot resolve the agent script: %v", err)
	}
	if _, err := os.Stat(agent); err != nil {
		t.Skipf("agent script not present here: %v", err)
	}
	cmd := exec.Command("bash", "-c", nginxReasonHarness) //nolint:gosec // fixed literal harness
	cmd.Env = append(os.Environ(), "AGENT="+agent)
	out, err := cmd.CombinedOutput()
	got := strings.TrimSpace(string(out))
	if err != nil || !strings.HasSuffix(got, "OK") {
		t.Fatalf("nginx reason handling: %s (err %v)", got, err)
	}
}
