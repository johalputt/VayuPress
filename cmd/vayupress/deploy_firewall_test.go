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
