// SPDX-License-Identifier: Apache-2.0

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// deploy/vayushield-firewall.sh runs as root and loads a kernel ruleset. It is
// re-run automatically by the reconcile agent on every boot and on every Tier 2
// toggle, so a regression here is not a one-off — it reapplies itself forever.
// These tests guard the three properties that are expensive to get wrong.

func readFirewallScript(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile("../../deploy/vayushield-firewall.sh")
	if err != nil {
		t.Fatalf("read firewall script: %v", err)
	}
	return string(b)
}

// ruleSetHeredoc returns just the nftables ruleset the script writes, so rule
// ordering is checked against what the kernel will actually evaluate rather than
// against the order lines happen to appear in a shell file.
func ruleSetHeredoc(t *testing.T, src string) string {
	t.Helper()
	const open = "cat >\"$rules\" <<EOF\n"
	i := strings.Index(src, open)
	if i < 0 {
		t.Fatal("could not find the ruleset heredoc in the firewall script")
	}
	body := src[i+len(open):]
	j := strings.Index(body, "\nEOF\n")
	if j < 0 {
		t.Fatal("ruleset heredoc is not terminated")
	}
	return body[:j]
}

// TestAllowlistPrecedesEveryLimiter is the ordering that makes the allowlist
// work at all. nftables evaluates a chain top-down and the first terminal verdict
// wins, so an accept placed after the limiters would never be reached for the
// traffic it is meant to spare.
//
// It must also precede the SYN guard specifically. That guard is a GLOBAL rate,
// not per-IP: behind a proxy, where every connection on the site arrives from the
// edge, a 25/second cap would throttle the whole site rather than an attacker.
func TestAllowlistPrecedesEveryLimiter(t *testing.T) {
	// Order has to be judged inside the RULESET, not across the whole file.
	// "${cdn_rules}" also appears in the shell that builds the variable, above
	// the heredoc, and anchoring on that gives an order that has nothing to do
	// with what nftables will evaluate — an earlier version of this test did
	// exactly that and reported a failure the script did not have.
	src := ruleSetHeredoc(t, readFirewallScript(t))
	i := strings.Index(src, "${cdn_rules}")
	if i < 0 {
		t.Fatal("the ruleset no longer interpolates ${cdn_rules} — the allowlist is not being emitted")
	}
	for _, later := range []string{
		"jump vs_web",   // entry to the per-source limiters
		"meter vs_rate", // per-source new-connection rate
		"meter vs_conn", // per-source concurrent connections
	} {
		j := strings.Index(src, later)
		if j < 0 {
			t.Errorf("expected rule %q missing from the ruleset", later)
			continue
		}
		if j < i {
			t.Errorf("%q is emitted BEFORE the allowlist — allowlisted edge traffic would be limited anyway", later)
		}
	}
	// The invalid-state drop must stay AHEAD of the allowlist: allowlisting a
	// source address should never mean accepting malformed conntrack packets
	// from it.
	if k := strings.Index(src, "ct state invalid drop"); k < 0 || k > i {
		t.Error("`ct state invalid drop` must precede the allowlist so allowlisted sources are still state-checked")
	}
}

// TestLimitersAreReachableAndCoverBothFamilies pins the two defects that made
// Tier 2's operator-facing tunables decorative.
//
// Reachability: in a BASE chain a `return` verdict means the chain policy, and
// the policy here is accept. The old ruleset front-loaded a SYN rule ending in
// `return`, so every new connection was accepted there and the per-source meters
// below it were dead — `CONN_LIMIT` and `NEW_CONN_RATE` changed nothing. The
// limiters now live in a REGULAR chain, where `return` resumes the caller.
//
// Families: in the inet family an `ip saddr` match carries an implicit IPv4-only
// dependency, so a v4-keyed meter never matches a v6 packet — while a bare drop
// beside it carries no dependency and matches everything. In the old ruleset two
// bugs cancelled: the meters were unreachable, so the family-blind drop never
// fired. Fixing reachability alone would have dropped 100% of IPv6 HTTP.
func TestLimitersAreReachableAndCoverBothFamilies(t *testing.T) {
	src := ruleSetHeredoc(t, readFirewallScript(t))

	if !strings.Contains(src, "chain vs_web4") || !strings.Contains(src, "chain vs_web6") {
		t.Error("the ruleset does not split the limiters by address family — " +
			"a v4-keyed meter silently ignores IPv6 while a bare drop beside it does not")
	}
	for _, want := range []string{
		"meter vs_conn4 { ip saddr",
		"meter vs_rate4 { ip saddr",
		"meter vs_conn6 { ip6 saddr",
		"meter vs_rate6 { ip6 saddr",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("missing %q — one address family is unprotected", want)
		}
	}
	// The limiters must NOT sit in the base chain, or `return` means accept and
	// everything after the first one is unreachable again.
	base := src[strings.Index(src, "chain input"):]
	if j := strings.Index(base, "chain vs_web"); j > 0 {
		base = base[:j]
	}
	if strings.Contains(base, "meter vs_") {
		t.Error("a per-source meter is back in the base chain, where return means accept")
	}
	if !strings.Contains(base, "jump vs_web") {
		t.Error("the base chain no longer hands new web connections to the limiter chain")
	}
}

// TestNoGlobalSynLimiter — the removed rule rate-limited SYNs GLOBALLY and then
// dropped the remainder, so anyone willing to emit more than the threshold put
// the chain into its drop state and every NEW visitor was dropped in-kernel
// while established connections carried on. That is an attacker-operated
// site-wide outage for the price of a trivial packet rate, and it presents as
// "works for me, nobody new can load it".
func TestNoGlobalSynLimiter(t *testing.T) {
	src := ruleSetHeredoc(t, readFirewallScript(t))
	if strings.Contains(src, "syn limit rate") || strings.Contains(src, "SYN_RATE") {
		t.Error("a global SYN rate limit is back in the ruleset — it throttles everyone's " +
			"new connections, not the attacker's, and pre-empts tcp_syncookies")
	}
	// syncookies must still be the stateless defence that replaces it.
	if !strings.Contains(readFirewallScript(t), "tcp_syncookies = 1") {
		t.Error("tcp_syncookies is no longer enabled — nothing covers SYN floods")
	}
}

// TestAllowlistEntriesAreValidated RUNS the parser rather than grepping for a
// regex. The textual version of this test passed while the validation had been
// stripped out of the parser, because the same pattern still appeared elsewhere
// in the file — proving only that the characters existed, not that anything used
// them.
//
// What matters: the allowlist is operator-edited text spliced into a ruleset
// loaded as root, so a line like "1.2.3.4/24; drop" must not survive as an nft
// statement.
func TestAllowlistEntriesAreValidated(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash unavailable")
	}
	dir := t.TempDir()

	// Everything above the command dispatcher is the function library.
	src := readFirewallScript(t)
	cut := strings.Index(src, `case "${1:-apply}" in`)
	if cut < 0 {
		t.Fatal("could not find the command dispatcher — cannot isolate the functions")
	}
	lib := filepath.Join(dir, "lib.sh")
	if err := os.WriteFile(lib, []byte(src[:cut]), 0o600); err != nil {
		t.Fatal(err)
	}

	allow := filepath.Join(dir, "cdn-allow.conf")
	if err := os.WriteFile(allow, []byte(strings.Join([]string{
		"# a comment",
		"173.245.48.0/20",
		"",
		"2400:cb00::/32",
		"1.2.3.4/24; drop",             // injection attempt
		"$(touch /tmp/pwned)/24",       // command substitution attempt
		"0.0.0.0/0 accept; ip saddr {", // rule-breakout attempt
		"not-an-address",
	}, "\n")), 0o600); err != nil {
		t.Fatal(err)
	}

	out, err := exec.Command("bash", "-c",
		"set -u; CDN_ALLOW_FILE="+allow+"; source "+lib+" 2>/dev/null; "+
			`printf 'V4=%s\nV6=%s\n' "$(read_cdn_allow 4 2>/dev/null)" "$(read_cdn_allow 6 2>/dev/null)"`).Output()
	if err != nil {
		t.Fatalf("running the parser: %v", err)
	}
	got := string(out)

	// The good entries survive...
	for _, want := range []string{"173.245.48.0/20", "2400:cb00::/32"} {
		if !strings.Contains(got, want) {
			t.Errorf("valid CIDR %q was dropped by the parser:\n%s", want, got)
		}
	}
	// ...and nothing that could change the meaning of a rule does.
	for _, bad := range []string{"drop", "accept", "touch", "$(", ";", "not-an-address"} {
		if strings.Contains(got, bad) {
			t.Errorf("parser emitted %q — an allowlist line can alter the ruleset:\n%s", bad, got)
		}
	}
}

// TestApplyNeverReachesTheNetwork pins the offline/onion property. Enabling a
// firewall must not make an outbound call: VayuPress supports onion-only installs
// whose whole point is that nothing calls out to the clearnet, and a host being
// hardened may well have no egress at all. Fetching ranges is a separate, explicit
// subcommand for exactly this reason.
func TestApplyNeverReachesTheNetwork(t *testing.T) {
	src := readFirewallScript(t)
	fetchStart := strings.Index(src, "cdn_allow_fetch() {")
	if fetchStart < 0 {
		t.Fatal("cdn_allow_fetch is gone — the explicit-fetch boundary this test relies on no longer exists")
	}
	// Find where the fetch function ends: the next top-level function definition.
	rest := src[fetchStart+len("cdn_allow_fetch() {"):]
	fetchEnd := fetchStart + len("cdn_allow_fetch() {")
	if m := regexp.MustCompile(`\n[a-z_]+\(\) \{`).FindStringIndex(rest); m != nil {
		fetchEnd += m[0]
	} else {
		fetchEnd = len(src)
	}

	netCall := regexp.MustCompile(`\b(curl|wget|nc|ftp)\b`)
	for _, m := range netCall.FindAllStringIndex(src, -1) {
		if m[0] >= fetchStart && m[0] < fetchEnd {
			continue // inside the deliberate fetch subcommand
		}
		line := src[strings.LastIndex(src[:m[0]], "\n")+1:]
		if i := strings.Index(line, "\n"); i >= 0 {
			line = line[:i]
		}
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue // a comment mentioning curl is fine
		}
		t.Errorf("network call outside cdn_allow_fetch: %q — `apply` must work with no egress", strings.TrimSpace(line))
	}
}

// TestStatusSurfacesAMissingAllowlist — a proxied origin with no allowlist is the
// single most likely reason traffic disappears into the kernel, and it produces
// no log line anywhere. `status` has to say so rather than printing a ruleset that
// looks healthy.
func TestStatusSurfacesAMissingAllowlist(t *testing.T) {
	src := readFirewallScript(t)
	if !strings.Contains(src, "proxy allowlist: none") {
		t.Error("`status` no longer reports a missing proxy allowlist")
	}
	if !strings.Contains(src, "cdn-allow cloudflare") {
		t.Error("`status` no longer tells the operator how to populate the allowlist")
	}
}

// --- Tier 2 kernel tunables ---------------------------------------------------

// writeSysctlStubs plants fake `sysctl` and `modprobe` binaries on PATH and
// returns the directory to prepend, plus the paths of the two recorder files.
//
// The `sysctl` stub emulates the real thing closely enough to matter: `-p`
// commits each key from the drop-in into a state store, and `-n` reads a key
// back, exiting 1 when it does not exist. Setting stubSkip to a shell glob makes
// `-p` silently ignore matching keys — which is precisely what a real kernel does
// on a fresh boot before nf_conntrack is loaded, and precisely the failure this
// whole read-back exists to surface.
func writeSysctlStubs(t *testing.T, dir, stubSkip string) (state, modprobeLog string) {
	t.Helper()
	state = filepath.Join(dir, "sysctl.state")
	modprobeLog = filepath.Join(dir, "modprobe.log")
	if err := os.WriteFile(state, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	sysctl := "#!/usr/bin/env bash\n" +
		"case \"$1\" in\n" +
		"  -p)\n" +
		"    while IFS= read -r line; do\n" +
		"      case \"$line\" in ''|\\#*) continue ;; esac\n" +
		"      k=\"${line%%=*}\"; k=\"$(printf '%s' \"$k\" | tr -d '[:space:]')\"\n" +
		"      v=\"${line#*=}\"; v=\"$(printf '%s' \"$v\" | tr -d '[:space:]')\"\n" +
		"      skip=0\n" +
		"      if [ -n \"" + stubSkip + "\" ]; then case \"$k\" in " + orGlob(stubSkip) + ") skip=1 ;; esac; fi\n" +
		"      [ \"$skip\" = 1 ] && continue\n" +
		"      printf '%s %s\\n' \"$k\" \"$v\" >>\"$STUB_STATE\"\n" +
		"    done <\"$2\"\n" +
		"    ;;\n" +
		"  -n)\n" +
		"    v=\"$(awk -v k=\"$2\" '$1==k{print $2}' \"$STUB_STATE\" | tail -1)\"\n" +
		"    [ -n \"$v\" ] || exit 1\n" +
		"    printf '%s\\n' \"$v\"\n" +
		"    ;;\n" +
		"esac\n" +
		"exit 0\n"
	modprobe := "#!/usr/bin/env bash\nprintf '%s\\n' \"$*\" >>\"$STUB_MODPROBE\"\n"

	for name, body := range map[string]string{"sysctl": sysctl, "modprobe": modprobe} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	return state, modprobeLog
}

// orGlob keeps an empty skip pattern from becoming a bare `)` in the stub's case.
func orGlob(pat string) string {
	if pat == "" {
		return "__none__"
	}
	return pat
}

// runApplySysctl sources the script's function library and calls apply_sysctl
// against a temporary drop-in, with the stubs above on PATH.
func runApplySysctl(t *testing.T, stubSkip string) (stdout, stderr, conf, modprobeLog string) {
	t.Helper()
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash unavailable")
	}
	dir := t.TempDir()

	src := readFirewallScript(t)
	cut := strings.Index(src, `case "${1:-apply}" in`)
	if cut < 0 {
		t.Fatal("could not find the command dispatcher — cannot isolate the functions")
	}
	lib := filepath.Join(dir, "lib.sh")
	if err := os.WriteFile(lib, []byte(src[:cut]), 0o600); err != nil {
		t.Fatal(err)
	}

	state, modLog := writeSysctlStubs(t, dir, stubSkip)
	conf = filepath.Join(dir, "99-vayushield.conf")

	cmd := exec.Command("bash", "-c", "source "+lib+"; apply_sysctl")
	cmd.Env = append(os.Environ(),
		"PATH="+dir+":"+os.Getenv("PATH"),
		"SYSCTL_CONF="+conf,
		"STUB_STATE="+state,
		"STUB_MODPROBE="+modLog,
	)
	var outBuf, errBuf strings.Builder
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		// A non-zero exit is itself a finding: apply_sysctl is called from
		// `apply` before the nftables ruleset is loaded, so aborting here means
		// the firewall never gets installed at all.
		t.Fatalf("apply_sysctl exited non-zero (%v) — `apply` would abort before loading any rules\nstdout:\n%s\nstderr:\n%s",
			err, outBuf.String(), errBuf.String())
	}
	return outBuf.String(), errBuf.String(), conf, modLog
}

// TestSysctlIsVerifiedNotAssumed — the original code was
// `sysctl --quiet -p … 2>/dev/null || true`, which cannot distinguish a setting
// that took from one that did not. That is not a cosmetic gap: every rule in the
// Tier 2 table is ct-stateful, so the conntrack table is the shield's own
// dependency, and exhausting it produces unattributable packet loss for every
// visitor with nothing in the panel to say why.
func TestSysctlIsVerifiedNotAssumed(t *testing.T) {
	stdout, stderr, conf, modLog := runApplySysctl(t, "")

	if !strings.Contains(stdout, "verified") {
		t.Errorf("a fully-applied run did not report verification:\nstdout:\n%s", stdout)
	}
	if strings.Contains(stderr, "did not take") {
		t.Errorf("a fully-applied run reported a failure:\nstderr:\n%s", stderr)
	}

	// nf_conntrack has to be loaded BEFORE the write, or the net.netfilter.*
	// keys do not exist yet and the conntrack half of the drop-in is inert.
	b, err := os.ReadFile(modLog)
	if err != nil || !strings.Contains(string(b), "nf_conntrack") {
		t.Error("nf_conntrack was never modprobed — its sysctl keys do not exist until it loads")
	}

	// The settings that bound an attacker's slice of the connection table must
	// actually be in the drop-in.
	c, err := os.ReadFile(conf)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"nf_conntrack_max",
		"nf_conntrack_buckets",              // default max/8 makes each bucket a long chain
		"nf_conntrack_tcp_timeout_syn_recv", // a slot per SYN, held for a minute
		"nf_conntrack_tcp_timeout_established",
		"nf_conntrack_tcp_loose = 0", // no state from mid-stream packets
		"tcp_syncookies = 1",
	} {
		if !strings.Contains(string(c), want) {
			t.Errorf("drop-in is missing %q", want)
		}
	}
}

// TestSysctlFailureIsReportedAndNonFatal is the case that actually happens: a
// container without CAP_SYS_ADMIN, or a boot where the module is not loaded, so
// the net.netfilter.* keys do not exist.
//
// Two properties, and they pull against each other. The run must SAY the setting
// did not take — that is the entire point of reading it back. And it must still
// exit 0, because apply_sysctl runs before apply_nft: under `set -euo pipefail` a
// failed read-back would abort the script and the host would end up with no
// firewall at all, which is strictly worse than the silent success it replaced.
func TestSysctlFailureIsReportedAndNonFatal(t *testing.T) {
	stdout, stderr, _, _ := runApplySysctl(t, "net.netfilter.*")

	if !strings.Contains(stderr, "did not take") {
		t.Errorf("conntrack keys never applied, yet nothing was reported:\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
	}
	if !strings.Contains(stderr, "nf_conntrack_max") {
		t.Errorf("the report does not name the key that failed, so it is not actionable:\nstderr:\n%s", stderr)
	}
	if strings.Contains(stdout, "verified") {
		t.Errorf("a partially-applied run still claimed verification:\nstdout:\n%s", stdout)
	}
	// The non-netfilter keys did apply and must not be reported as failures.
	if strings.Contains(stderr, "tcp_syncookies") {
		t.Errorf("a setting that applied cleanly was reported as failed:\nstderr:\n%s", stderr)
	}
}

// TestAcceptQueuesAreNotSilentlyShrunk — deploy/vps-kernel-tuning.sh writes
// 99-vayupress.conf with somaxconn and tcp_max_syn_backlog at 65535 for a
// high-concurrency workload, and deploy/vayupress.service asks for
// LimitNOFILE=65536 citing 10k+ concurrent connections. sysctl.d applies
// lexicographically and 'p' < 's', so this file loads last: the hardening
// drop-in shipped both at 4096, which meant enabling the DDoS firewall quietly
// cut both backlogs 16x on the box that had just been tuned for the opposite.
//
// A deep accept queue is the partner to tcp_syncookies, not its rival — a short
// queue reaches cookie fallback sooner, and cookies cost window scaling and SACK
// on every connection that takes that path.
func TestAcceptQueuesAreNotSilentlyShrunk(t *testing.T) {
	shield := readFirewallScript(t)

	tuning, err := os.ReadFile("../../deploy/vps-kernel-tuning.sh")
	if err != nil {
		t.Fatalf("read the kernel-tuning script: %v", err)
	}

	// Read the value each script sets, and require the hardening file never to
	// be the smaller of the two. Pinning a literal 65535 here would pass even if
	// the tuning script later moved.
	for _, key := range []string{"net.core.somaxconn", "net.ipv4.tcp_max_syn_backlog"} {
		want := sysctlValue(t, string(tuning), key)
		got := sysctlValue(t, shield, key)
		if want == 0 || got == 0 {
			t.Errorf("%s: could not read both values (tuning=%d shield=%d)", key, want, got)
			continue
		}
		if got < want {
			t.Errorf("%s: the shield sets %d and loads AFTER the tuning file's %d — "+
				"turning on the firewall shrinks the accept queue by %dx", key, got, want, want/got)
		}
	}
}

func sysctlValue(t *testing.T, src, key string) int {
	t.Helper()
	m := regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(key) + `\s*=\s*(\d+)\s*$`).FindStringSubmatch(src)
	if m == nil {
		return 0
	}
	n, err := strconv.Atoi(m[1])
	if err != nil {
		return 0
	}
	return n
}

// TestSysctlCollisionsReportBootOrder RUNS the detector against a fake
// /etc/sysctl.d. The failure it exists to catch is invisible to the read-back in
// apply_sysctl: `sysctl -p` applies one file immediately, which is not the order
// systemd uses at boot, so a value can verify clean now and be overwritten by a
// later-sorting drop-in on the next reboot.
func TestSysctlCollisionsReportBootOrder(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash unavailable")
	}
	dir := t.TempDir()

	src := readFirewallScript(t)
	cut := strings.Index(src, `case "${1:-apply}" in`)
	if cut < 0 {
		t.Fatal("could not find the command dispatcher")
	}
	lib := filepath.Join(dir, "lib.sh")
	if err := os.WriteFile(lib, []byte(src[:cut]), 0o600); err != nil {
		t.Fatal(err)
	}

	confd := filepath.Join(dir, "sysctl.d")
	if err := os.MkdirAll(confd, 0o755); err != nil {
		t.Fatal(err)
	}
	ours := filepath.Join(confd, "99-vayushield.conf")
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(confd, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("99-vayushield.conf", "# ours\nnet.core.somaxconn = 65535\nnet.ipv4.tcp_syncookies = 1\nnet.ipv4.tcp_rfc1337 = 1\n")
	write("99-vayupress.conf", "net.core.somaxconn = 65535\n") // identical: not a collision
	write("10-earlier.conf", "net.ipv4.tcp_rfc1337 = 0\n")     // we win
	write("99-zz-provider.conf", "net.ipv4.tcp_syncookies = 0\n")

	// The glob is baked into the function, so point it at the fake tree with a
	// sed-free override: run with the temp dir standing in for /etc.
	body, err := os.ReadFile(lib)
	if err != nil {
		t.Fatal(err)
	}
	patched := strings.ReplaceAll(string(body), "/etc/sysctl.d/*.conf /etc/sysctl.conf", confd+"/*.conf")
	if patched == string(body) {
		t.Fatal("the drop-in glob is no longer where the test can redirect it")
	}
	if err := os.WriteFile(lib, []byte(patched), 0o600); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command("bash", "-c", "source "+lib+"; sysctl_collisions")
	cmd.Env = append(os.Environ(), "SYSCTL_CONF="+ours)
	var outBuf, errBuf strings.Builder
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		t.Fatalf("sysctl_collisions exited non-zero (%v): %s", err, errBuf.String())
	}
	got := errBuf.String()

	// The dangerous direction: a later-sorting file silently reverts hardening.
	if !strings.Contains(got, "tcp_syncookies") || !strings.Contains(got, "99-zz-provider.conf") {
		t.Errorf("a later-sorting drop-in disabling syncookies was not reported:\n%s", got)
	}
	if !strings.Contains(got, "NOT in effect") {
		t.Errorf("the report does not say the shield's value loses at boot:\n%s", got)
	}
	// An earlier-sorting file we override is informational, not a warning.
	if !strings.Contains(got, "tcp_rfc1337") {
		t.Errorf("overriding an earlier drop-in went unrecorded:\n%s", got)
	}
	// Agreeing on a value is not a collision — reporting it would train the
	// operator to ignore the whole block.
	if strings.Contains(got, "somaxconn") {
		t.Errorf("two files agreeing on a value was reported as a collision:\n%s", got)
	}
}

// --- Service-aware ports ------------------------------------------------------

// generateRuleset runs the script's own apply_nft and returns the ruleset it
// would load. `nft` is stubbed so nothing touches this machine's kernel, but the
// stub delegates `-c` to the real binary when one is present — so the emitted
// text is syntax-checked by nftables itself rather than by a regex here.
func generateRuleset(t *testing.T, env ...string) string {
	t.Helper()
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash unavailable")
	}
	dir := t.TempDir()

	src := readFirewallScript(t)
	cut := strings.Index(src, `case "${1:-apply}" in`)
	if cut < 0 {
		t.Fatal("could not find the command dispatcher")
	}
	lib := filepath.Join(dir, "lib.sh")
	if err := os.WriteFile(lib, []byte(src[:cut]), 0o600); err != nil {
		t.Fatal(err)
	}

	bin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	capture := filepath.Join(dir, "ruleset.nft")
	real, _ := exec.LookPath("nft")
	stub := "#!/usr/bin/env bash\n" +
		"if [ \"$1\" = \"-c\" ]; then\n" +
		"  cp \"$3\" \"" + capture + "\"\n" +
		"  if [ -x \"" + real + "\" ]; then exec \"" + real + "\" \"$@\"; fi\n" +
		"  exit 0\n" +
		"fi\n" +
		"exit 0\n"
	if err := os.WriteFile(filepath.Join(bin, "nft"), []byte(stub), 0o700); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command("bash", "-c", "set -u; source "+lib+"; apply_nft")
	cmd.Env = append(append(os.Environ(),
		"PATH="+bin+":"+os.Getenv("PATH"),
		"CDN_ALLOW_FILE="+filepath.Join(dir, "absent.conf"),
	), env...)
	var errBuf strings.Builder
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		t.Fatalf("apply_nft failed (%v) — nftables rejected the generated ruleset:\n%s", err, errBuf.String())
	}
	b, err := os.ReadFile(capture)
	if err != nil {
		t.Fatalf("no ruleset was captured: %v", err)
	}
	return string(b)
}

// TestMailPortsAreModelled — the same binary that serves the site binds
// :25/:110/:143/:587/:993/:995, and the allowlist comment in this very script
// notes that an MX record publishes the origin address to the world. The ruleset
// modelled { 80, 443 } only, so SMTP — the classic connection-exhaustion target,
// on a port whose address is published by design — had no per-source cap at all.
//
// The expected ports are read out of vayuos.go rather than written down here, so
// adding a listener there and forgetting this file fails the build.
func TestMailPortsAreModelled(t *testing.T) {
	b, err := os.ReadFile("vayuos.go")
	if err != nil {
		t.Fatalf("read vayuos.go: %v", err)
	}
	// mailCfg.XListen = config.EnvOr("VAYUOS_MAIL_…", ":25")
	bound := regexp.MustCompile(`mailCfg\.\w*Listen\s*=\s*config\.EnvOr\("VAYUOS_MAIL_\w+",\s*":(\d+)"\)`).
		FindAllStringSubmatch(string(b), -1)
	if len(bound) == 0 {
		t.Fatal("could not read the mail listener defaults out of vayuos.go")
	}

	rules := generateRuleset(t)
	for _, m := range bound {
		if !regexp.MustCompile(`\b` + m[1] + `\b`).MatchString(rules) {
			t.Errorf("port %s is bound by the mail engine but absent from the ruleset — "+
				"no per-source connection cap applies to it", m[1])
		}
	}
	if !strings.Contains(rules, "jump vs_mail") {
		t.Error("mail traffic is not handed to a limiter chain")
	}
}

// TestMailAndWebDoNotShareAMeter — an nft meter is a keyed namespace, so pointing
// web and mail at the same one makes them spend a single per-source budget. An
// HTTP flood would then throttle inbound mail as a side effect: the shield
// causing an outage in a service the attack never touched.
func TestMailAndWebDoNotShareAMeter(t *testing.T) {
	rules := generateRuleset(t)
	meters := regexp.MustCompile(`meter (\w+) \{`).FindAllStringSubmatch(rules, -1)
	seen := map[string]int{}
	for _, m := range meters {
		seen[m[1]]++
	}
	if len(seen) < 8 {
		t.Errorf("expected separate web and mail meters per address family, got %d distinct: %v", len(seen), seen)
	}
	// Both families, both services. The v4/v6 split is the same landmine as on
	// the web chains: an `ip saddr` meter never matches a v6 packet, while a bare
	// drop beside it matches everything.
	for _, want := range []string{"vs_mconn4", "vs_mrate4", "vs_mconn6", "vs_mrate6"} {
		if seen[want] == 0 {
			t.Errorf("missing meter %q — one address family or one limit is unprotected on mail", want)
		}
	}
	if !strings.Contains(rules, "chain vs_mail4") || !strings.Contains(rules, "chain vs_mail6") {
		t.Error("the mail limiters are not split by address family")
	}
}

// TestQUICIsModelled — deploy/nginx-vayupress.conf instructs operators to turn on
// HTTP/3, and the ruleset did not model UDP at all. QUIC has no handshake to make
// a flooder pay for state, which makes a per-source cap matter more there than on
// TCP, not less.
func TestQUICIsModelled(t *testing.T) {
	rules := generateRuleset(t)
	if !regexp.MustCompile(`udp dport \{ ?443 ?\}[^\n]*jump vs_web`).MatchString(rules) {
		t.Errorf("UDP/443 does not reach the per-source limiters:\n%s", rules)
	}
}

// TestAllowlistCoversQUIC — half-allowlisting an edge is worse than not
// allowlisting it. If only TCP is accepted, a proxy fronting the site over HTTP/3
// is rate-limited on its UDP traffic while its TCP traffic sails through, which
// presents as HTTP/3 being intermittently broken and HTTP/2 being fine — close to
// undiagnosable from either end.
func TestAllowlistCoversQUIC(t *testing.T) {
	dir := t.TempDir()
	allow := filepath.Join(dir, "cdn-allow.conf")
	if err := os.WriteFile(allow, []byte("173.245.48.0/20\n2400:cb00::/32\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	rules := generateRuleset(t, "CDN_ALLOW_FILE="+allow)

	for _, want := range []string{
		"ip saddr { 173.245.48.0/20 } tcp dport",
		"ip saddr { 173.245.48.0/20 } udp dport",
		"ip6 saddr { 2400:cb00::/32 } tcp dport",
		"ip6 saddr { 2400:cb00::/32 } udp dport",
	} {
		if !strings.Contains(rules, want) {
			t.Errorf("allowlist is missing %q:\n%s", want, rules)
		}
	}
}

// TestMailOptOutActuallyOptsOut — the tunable is documented as "set to empty to
// opt out", and `${MAIL_PORTS:-default}` substitutes the default for an empty
// value, so the documented escape hatch did nothing at all. A relay operator
// would have set it, believed mail was unshaped, and still been shaped.
func TestMailOptOutActuallyOptsOut(t *testing.T) {
	rules := generateRuleset(t, "MAIL_PORTS=")
	if strings.Contains(rules, "vs_mail") {
		t.Errorf("MAIL_PORTS= left the mail rules in the ruleset:\n%s", rules)
	}
	// The web side must be untouched by the opt-out.
	if !strings.Contains(rules, "jump vs_web") {
		t.Errorf("opting out of mail shaping also removed the web limiters:\n%s", rules)
	}
}

// --- App-port drop ------------------------------------------------------------

// TestAppPortIsDroppedFromOutside — reaching the Go listener directly bypasses
// nginx, and with it every Tier 3 limit, TLS termination, and the real-client-IP
// headers the app is configured to trust. onionSafeBindAddr binds loopback unless
// it detects a container, but a bind is a runtime decision that a stray
// BIND_ADDR, a container misdetection or a systemd override can reverse — with
// nothing outside the process to notice. This is the backstop that does not
// depend on the app having got it right.
func TestAppPortIsDroppedFromOutside(t *testing.T) {
	rules := generateRuleset(t, "APP_PORT=9000")
	if !strings.Contains(rules, `iif != "lo" tcp dport 9000 drop`) {
		t.Errorf("the app port is reachable from off-host:\n%s", rules)
	}
	// Loopback must still be accepted ahead of it, or nginx cannot reach the app
	// it is proxying to and the site is down.
	lo := strings.Index(rules, `iif "lo" accept`)
	drop := strings.Index(rules, `iif != "lo" tcp dport 9000 drop`)
	if lo < 0 || lo > drop {
		t.Error("the loopback accept no longer precedes the app-port drop — nginx could not reach the app")
	}
	// The chain policy stays accept. The chain runs at priority -10 with an
	// accept policy specifically so a bad rule here cannot lock an operator out
	// of SSH; a default-drop redesign strands people with no way back in.
	if !strings.Contains(rules, "policy accept;") {
		t.Error("the base chain is no longer policy accept — a bad rule can now lock the operator out")
	}
}

// TestAppPortDropRefusesToBreakTheSite — an operator terminating TLS in the Go
// binary on :443, or running the app on a mail port, would lose the service
// outright. This script is re-applied by the reconcile agent on every boot, so
// that would be an outage that reinstates itself.
func TestAppPortDropRefusesToBreakTheSite(t *testing.T) {
	for _, port := range []string{"443", "80", "25", "993"} {
		rules := generateRuleset(t, "APP_PORT="+port)
		if strings.Contains(rules, `iif != "lo" tcp dport `+port+` drop`) {
			t.Errorf("APP_PORT=%s produced a drop on a port the ruleset already serves — "+
				"the site or mail would go dark on every apply", port)
		}
	}
	// And the opt-out has to work, for the same ${VAR-default} reason as MAIL_PORTS.
	if rules := generateRuleset(t, "APP_PORT="); strings.Contains(rules, `iif != "lo" tcp dport`) {
		t.Errorf("APP_PORT= did not suppress the drop:\n%s", rules)
	}
}
