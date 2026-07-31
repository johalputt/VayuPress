// SPDX-License-Identifier: Apache-2.0

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
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
# The MCP-surface DETECTOR and the remediation must agree on what "restricted"
# means. The detector used to flag the mere presence of a catch-all location, so
# after the fix narrowed that block to "return 404" the panel still reported it
# as unrestricted — the hardening row said Applied and the posture report said
# it had not been, leaving the operator to decide which panel was lying.
#
# The detector is read OUT OF THE AGENT, not restated here. A copy in the test
# would go on passing after the real one was reverted, which is exactly the
# failure this test exists to catch.
sed -n '/^mcp_catchall_probe() {/,/^}/p' "$AGENT" > "$D/m.sh"
[ -s "$D/m.sh" ] || { echo "FAIL mcp_catchall_probe not found in the agent script"; exit 1; }

mcp_verdict() { # $1=config file -> prints the digest value for mcp_vhost_restricted
  bash -c 'source "'"$D"'/m.sh"; if [ -n "$(mcp_catchall_probe <"'"$1"'")" ]; then echo no; else echo yes; fi'
}
mcp_where() { bash -c 'source "'"$D"'/m.sh"; mcp_catchall_probe <"'"$1"'"'; }
cat > "$D/mcp_open.txt" <<'MCPA'
server {
    server_name johal.in;
    location / { proxy_pass http://127.0.0.1:8080; }
}
server {
    server_name mcp.johal.in;
    location ^~ /mcp { proxy_pass http://127.0.0.1:8080; }
    location / {
        proxy_pass http://127.0.0.1:8080;
    }
}
MCPA
cat > "$D/mcp_narrowed.txt" <<'MCPB'
server {
    server_name johal.in;
    location / { proxy_pass http://127.0.0.1:8080; }
}
server {
    server_name mcp.johal.in;
    location ^~ /mcp { proxy_pass http://127.0.0.1:8080; }
    location / { return 404; }  # narrowed by vayushield-agent
}
MCPB
[ "$(mcp_verdict "$D/mcp_open.txt")" = "no" ] || { echo "FAIL a proxying catch-all on the MCP host was reported as restricted"; exit 1; }
[ "$(mcp_verdict "$D/mcp_narrowed.txt")" = "yes" ] || { echo "FAIL a narrowed MCP host is still reported unrestricted — the fix says Applied while the posture report disagrees"; exit 1; }

# The MCP block must END where nginx says it ends. This config indents its server
# blocks, so the closing brace is not at column zero, and it lists the narrowed
# MCP host BEFORE the apex. A detector that leaves the MCP block only on a bare
# column-zero "}" stays inside it for the rest of the dump and charges the apex
# vhost's proxying catch-all to the MCP host — a permanent warning on a host that
# was correctly narrowed, with no configuration change that could ever clear it.
cat > "$D/mcp_indented.txt" <<'MCPC'
  server {
      server_name mcp.johal.in;
      location ^~ /mcp { proxy_pass http://127.0.0.1:8080; }
      location / { return 404; }  # narrowed by vayushield-agent
  }
  server {
      server_name johal.in;
      location / { proxy_pass http://127.0.0.1:8080; }
  }
MCPC
[ "$(mcp_verdict "$D/mcp_indented.txt")" = "yes" ] || { echo "FAIL the apex vhost's catch-all was charged to the MCP host — the detector never left the MCP server block"; exit 1; }

# ...and the same shape must still catch a genuinely open MCP host, so the rule
# above is a boundary fix and not a blanket "always restricted".
cat > "$D/mcp_indented_open.txt" <<'MCPD'
  server {
      server_name mcp.johal.in;
      location ^~ /mcp { proxy_pass http://127.0.0.1:8080; }
      location / { proxy_pass http://127.0.0.1:8080; }
  }
  server {
      server_name johal.in;
      location / { proxy_pass http://127.0.0.1:8080; }
  }
MCPD
[ "$(mcp_verdict "$D/mcp_indented_open.txt")" = "no" ] || { echo "FAIL an open MCP host went unreported once the block boundaries were tightened"; exit 1; }

# A server block ends where its BRACES say, not at the first indented "}".
#
# Matching a closing brace at the start of a line leaves the server block at the
# end of the FIRST location, so everything after it — including the catch-all —
# is judged as though it were outside the vhost. That reads clean on a template
# whose locations are all one-liners and wrongly on any config with a multi-line
# location, which is every config certbot has touched.
cat > "$D/mcp_multiline.txt" <<'MCPE'
# configuration file /etc/nginx/sites-enabled/mcp.johal.in:
server {
    listen 443 ssl http2;
    server_name mcp.johal.in;
    location ^~ /.well-known/acme-challenge/ {
        root /var/cache;
        try_files $uri =404;
    }
    location / {
        proxy_pass http://127.0.0.1:8080;
    }
}
MCPE
[ "$(mcp_verdict "$D/mcp_multiline.txt")" = "no" ] || { echo "FAIL a proxying catch-all after a multi-line location was missed — the detector left the server block at the first indented brace"; exit 1; }

# The verdict has to say WHERE. Two releases went by with an operator re-pressing
# a remediation and re-reading an unchanged sentence, because neither they nor
# anyone reading their screenshot could tell a stale detector from a real finding.
#
# The line number counts from the file's FIRST line, not from the marker: nginx
# emits "# configuration file <path>:" immediately followed by line 1 of that
# file, so the number the panel prints is the number the operator sees when they
# open it. Verified against real nginx output below.
where=$(mcp_where "$D/mcp_multiline.txt")
case "$where" in
  /etc/nginx/sites-enabled/mcp.johal.in:8) ;;
  *) echo "FAIL the probe did not name the offending catch-all: [$where] want /etc/nginx/sites-enabled/mcp.johal.in:8"; exit 1 ;;
esac
# ...and it must stay silent on a host it does not object to, or the panel would
# print a file and line beside a passing row.
[ -z "$(mcp_where "$D/mcp_narrowed.txt")" ] || { echo "FAIL the probe named a location on a restricted host"; exit 1; }

# Against REAL nginx -T, not a hand-written approximation of it. The whole value
# of the reference is that an operator can open that file at that line and see
# what the agent saw; a fixture that merely resembles nginx output cannot
# establish that, and the marker/offset convention is exactly the sort of detail
# a fixture gets subtly wrong.
if command -v nginx >/dev/null 2>&1; then
  mkdir -p "$D/live/inc"
  cat > "$D/live/inc/mcp.conf" <<'LIVE'
server {
    listen 8099;
    server_name mcp.example.test;
    location ^~ /mcp {
        return 204;
    }
    location / {
        proxy_pass http://127.0.0.1:8080;
    }
}
LIVE
  printf 'events {}
http {
  include %s/live/inc/mcp.conf;
}
' "$D" > "$D/live/n.conf"
  if nginx -T -c "$D/live/n.conf" -p "$D/live" >"$D/live/dump.txt" 2>/dev/null; then
    live=$(bash -c 'source "'"$D"'/m.sh"; mcp_catchall_probe' < "$D/live/dump.txt")
    want="$D/live/inc/mcp.conf:7"
    [ "$live" = "$want" ] || { echo "FAIL against real nginx -T the probe said [$live], want [$want]"; exit 1; }
    # And that line really is the catch-all in the file on disk.
    got_line=$(sed -n '7p' "$D/live/inc/mcp.conf")
    case "$got_line" in
      *"location /"*) ;;
      *) echo "FAIL line 7 of the real file is [$got_line], not the catch-all — the reference would send an operator to the wrong line"; exit 1 ;;
    esac
  fi
fi

# ── The remediation must apply the SAME rule as the detector ──────────────────
#
# It did not. It rewrote every catch-all on the MCP host to "return 404", so
# pressing "Restrict the MCP host" on a vhost written by setup-mcp-subdomain.sh
# replaced the :80 block's "return 301 https://..." with a refusal and broke the
# HTTP-to-HTTPS redirect — on a host whose :443 block ships narrowed already, so
# there was nothing to fix in the first place.
sed -n '/^mcp_narrow_config() {/,/^}/p' "$AGENT" > "$D/n.sh"
[ -s "$D/n.sh" ] || { echo "FAIL mcp_narrow_config not found in the agent script"; exit 1; }
narrow() { bash -c 'source "'"$D"'/n.sh"; mcp_narrow_config' < "$1"; }

# Verbatim shape of scripts/setup-mcp-subdomain.sh: :80 redirects, :443 refuses.
cat > "$D/tpl.conf" <<'TPL'
server {
    listen 80; listen [::]:80;
    server_name mcp.johal.in;
    location ^~ /.well-known/acme-challenge/ { root /var/cache; default_type text/plain; try_files $uri =404; }
    location / { return 301 https://$host$request_uri; }
}
server {
    listen 443 ssl http2; listen [::]:443 ssl http2;
    server_name mcp.johal.in;
    location ^~ /mcp   { proxy_pass http://127.0.0.1:8080; }
    location = /health { proxy_pass http://127.0.0.1:8080; access_log off; }
    location / { return 404; }
}
server {
    listen 443 ssl http2;
    server_name johal.in;
    location / { proxy_pass http://127.0.0.1:8080; }
}
TPL
narrow "$D/tpl.conf" > "$D/tpl.out"
if ! diff -q "$D/tpl.conf" "$D/tpl.out" >/dev/null; then
  echo "FAIL the remediation edited a host that was already correct:"; diff "$D/tpl.conf" "$D/tpl.out"; exit 1
fi

# A genuinely open MCP host still gets narrowed — and ONLY its catch-all.
cat > "$D/open.conf" <<'OPN'
server {
    listen 443 ssl http2;
    server_name mcp.johal.in;
    location ^~ /mcp { proxy_pass http://127.0.0.1:8080; }
    location / { proxy_pass http://127.0.0.1:8080; }
}
server {
    listen 443 ssl http2;
    server_name johal.in;
    location / { proxy_pass http://127.0.0.1:8080; }
}
OPN
narrow "$D/open.conf" > "$D/open.out"
grep -q 'location / { return 404; }' "$D/open.out" || { echo "FAIL a proxying MCP catch-all was not narrowed"; exit 1; }
# The apex must survive untouched: narrowing the whole site to 404 would take the
# install off the internet, which is a worse outcome than the finding.
n=$(grep -c 'location / { proxy_pass' "$D/open.out")
[ "$n" = "1" ] || { echo "FAIL the apex catch-all was rewritten too ($n proxying catch-alls left, want 1)"; exit 1; }

# Repair: an install that already pressed the button gets its :80 redirect back.
sed 's|location / { return 301 https://$host$request_uri; }|location / { return 404; }  # narrowed by vayushield-agent|' \
    "$D/tpl.conf" > "$D/dmg.conf"
grep -q 'return 301' "$D/dmg.conf" && { echo "FAIL the damage fixture did not reproduce the damage"; exit 1; }
narrow "$D/dmg.conf" > "$D/dmg.out"
grep -q 'location / { return 301 https://\$host\$request_uri; }' "$D/dmg.out" || {
  echo "FAIL the clobbered :80 redirect was not restored"; sed -n '1,8p' "$D/dmg.out"; exit 1; }
# ...and the :443 refusal must NOT be turned into a redirect by the repair.
grep -q 'location / { return 404; }' "$D/dmg.out" || { echo "FAIL the :443 refusal was lost during repair"; exit 1; }

# The repair only touches lines this agent wrote. An operator who deliberately
# returns 404 on their own :80 block keeps it.
sed 's|location / { return 301 https://$host$request_uri; }|location / { return 404; }|' \
    "$D/tpl.conf" > "$D/own.conf"
narrow "$D/own.conf" > "$D/own.out"
if ! diff -q "$D/own.conf" "$D/own.out" >/dev/null; then
  echo "FAIL the repair rewrote an operator's own :80 refusal, which carries no agent marker"; exit 1
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

// TestRemediationsRefreshTheDigestImmediately covers the gap between doing a
// thing and the panel saying so.
//
// The posture report reads the enforcement digest, which the agent rebuilds
// about once a minute because it shells out to nft and `nginx -T`. An operator
// who applied both fixes, saw each report "Applied", and then looked at the
// posture report was shown the PREVIOUS state for up to a minute — with nothing
// on the page indicating the two panels disagreed only because one was stale.
// The obvious reading is that the fix did not work, which is the same class of
// defect as a number that is right and a sentence that is not.
func TestRemediationsRefreshTheDigestImmediately(t *testing.T) {
	src, err := os.ReadFile("../../deploy/vayushield-agent.sh")
	if err != nil {
		t.Skipf("agent script not readable here: %v", err)
	}
	s := string(src)
	for _, fn := range []string{"reconcile_defaulthost", "reconcile_mcpsurface"} {
		body := agentFuncBody(s, fn)
		if body == "" {
			t.Errorf("%s not found in the agent script", fn)
			continue
		}
		if !strings.Contains(body, "write_digest") {
			t.Errorf("%s changes nginx but never refreshes the enforcement digest, so the "+
				"posture report keeps reporting the pre-fix state for up to a minute and the "+
				"operator reads that as the fix having failed", fn)
		}
	}
}

// TestSelfUpgradeInstallsEverythingTheBundleCarries covers a gap that only
// exists on the installs least able to notice it.
//
// install_agent laid down the rescue units; self_upgrade did not. So every
// helper that reached its current version through "Upgrade the helper" — the
// path the panel steers everyone to, and the only one that does not need a
// terminal — ended up WITHOUT the root-side watcher that repairs a broken agent.
// The deadlock that path exists to break was therefore unbroken on exactly the
// installs most likely to hit it, and nothing anywhere said so.
func TestSelfUpgradeInstallsEverythingTheBundleCarries(t *testing.T) {
	src, err := os.ReadFile("../../deploy/vayushield-agent.sh")
	if err != nil {
		t.Skipf("agent script not readable here: %v", err)
	}
	body := agentFuncBody(string(src), "self_upgrade")
	if body == "" {
		t.Fatal("self_upgrade not found in the agent script")
	}
	for _, want := range []string{
		"vayushield-rescue.path",
		"vayushield-rescue.service",
		"agent.version",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("self_upgrade never installs %s, so a helper upgraded from the panel "+
				"silently lacks it while one installed by hand has it", want)
		}
	}
	// The release workflow has to put the stamp in the bundle, or the agent reads
	// "unknown" forever and the version row answers nothing.
	wf, err := os.ReadFile("../../.github/workflows/tag-release.yml")
	if err != nil {
		t.Skipf("release workflow not readable here: %v", err)
	}
	if !strings.Contains(string(wf), "dist/agent/agent.version") {
		t.Error("the release workflow does not stamp dist/agent/agent.version, so every " +
			"helper reports its version as unknown and the upgrade button stays uncheckable")
	}
}

// agentFuncBody returns the source of a shell function, ending at the next
// top-level function definition.
//
// Terminating on a column-zero "}" was tried first and is wrong: these functions
// embed heredocs of nginx config, whose own closing braces sit at column zero.
// The body was silently truncated mid-heredoc and the test then failed on code
// that was correct — a test failing for the wrong reason is worth no more than
// one passing for the wrong reason.
func agentFuncBody(src, name string) string {
	i := strings.Index(src, "\n"+name+"() {")
	if i < 0 {
		return ""
	}
	rest := src[i+1:]
	// Search from AFTER this function's own header line. Go's (?m)^ also matches
	// at position 0, so scanning from the start matched the declaration itself
	// and returned a one-character body — the test then failed on correct code,
	// twice, for two different wrong reasons.
	head := strings.Index(rest, "\n")
	if head < 0 {
		return rest
	}
	if loc := shellFuncStart.FindStringIndex(rest[head+1:]); loc != nil {
		return rest[:head+1+loc[0]]
	}
	return rest
}

// shellFuncStart matches the start of a top-level shell function definition.
var shellFuncStart = regexp.MustCompile(`(?m)^[a-z_][a-z0-9_]*\(\) \{`)
