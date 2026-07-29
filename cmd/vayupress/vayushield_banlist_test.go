// SPDX-License-Identifier: Apache-2.0

package main

import (
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

// The banlist is written by the UNPRIVILEGED app and read by the ROOT agent,
// which turns it into nftables input. These tests run that path rather than
// reading it: the previous validation was a character whitelist that looked
// strict and was not a parser.

// agentLib writes the agent script's function library (everything above its
// command dispatcher) to a temp file and returns the path.
func agentLib(t *testing.T, dir string) string {
	t.Helper()
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash unavailable")
	}
	b, err := os.ReadFile("../../deploy/vayushield-agent.sh")
	if err != nil {
		t.Fatalf("read the agent script: %v", err)
	}
	src := string(b)
	cut := strings.Index(src, "# reconcile_cdnallow")
	if cut < 0 {
		t.Fatal("could not find the boundary of the agent's function library")
	}
	lib := filepath.Join(dir, "agentlib.sh")
	if err := os.WriteFile(lib, []byte(src[:cut]), 0o600); err != nil {
		t.Fatal(err)
	}
	return lib
}

// TestBanlistAddressesAreParsedNotPatternMatched — the old filter was
// `*[!0-9a-fA-F.:]*`, which forecloses injection but admits strings that are not
// addresses: `abcd` is entirely hex characters, `999.999.999.999` is entirely
// digits and dots.
//
// That is not merely useless. nft treats an unparseable element as a HOSTNAME and
// calls the resolver ("Could not resolve hostname"), so a malformed line in a file
// written by the unprivileged app became a DNS lookup made by the ROOT agent on
// every poll — and in onion mode, a clearnet callback from the most privileged
// process on the box.
func TestBanlistAddressesAreParsedNotPatternMatched(t *testing.T) {
	dir := t.TempDir()
	lib := agentLib(t, dir)

	cases := []struct {
		addr string
		want bool
		why  string
	}{
		{"1.2.3.4", true, ""},
		{"0.0.0.0", true, ""},
		{"255.255.255.255", true, ""},
		{"66.249.66.1", true, ""},
		{"::1", true, ""},
		{"::", true, ""},
		{"dead::beef", true, ""},
		{"2400:cb00::1", true, ""},
		{"1:2:3:4:5:6:7:8", true, ""},
		{"2001:0db8:85a3:0000:0000:8a2e:0370:7334", true, ""},

		{"abcd", false, "all hex characters — nft resolves it as a hostname"},
		{"deadbeef", false, "all hex characters"},
		{"999.999.999.999", false, "all digits and dots — nft resolves it as a hostname"},
		{"256.1.1.1", false, "octet out of range"},
		{"1.2.3.4.5", false, "five octets"},
		{"1.2.3", false, "three octets"},
		{"010.1.1.1", false, "leading zero: nft reads it as octal, so it would ban a DIFFERENT host"},
		{"::::", false, "not an address"},
		{"1:::2", false, "run of colons"},
		{"12345::1", false, "group wider than four hex digits"},
		{"1:2:3:4:5:6:7:8:9", false, "nine groups"},
		{"1:2:3:4:5:6:7", false, "seven groups, no zero-run"},
		{"1::2::3", false, "two zero-runs"},
		{"g::1", false, "not hex"},
		{"", false, "empty"},
	}

	for _, tc := range cases {
		fn := "valid_ip4"
		if strings.Contains(tc.addr, ":") {
			fn = "valid_ip6"
		}
		cmd := exec.Command("bash", "-c", "set -u; source "+lib+"; "+fn+" "+shellQuote(tc.addr))
		err := cmd.Run()
		got := err == nil
		if got != tc.want {
			t.Errorf("%s(%q) = %v, want %v%s", fn, tc.addr, got, tc.want,
				func() string {
					if tc.why != "" {
						return " — " + tc.why
					}
					return ""
				}())
		}
	}
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// TestParserNeverAdmitsANonLiteral is the invariant that matters, checked against
// an oracle available everywhere: Go's own netip.ParseAddr.
//
// The defect being closed is that a string which is not a literal address reaches
// nft, which then treats it as a HOSTNAME and calls the resolver. "Is this a
// literal address" is exactly the question netip.ParseAddr answers, so anything
// it rejects must not survive our parser either. Being STRICTER than netip is
// fine — it costs at most one ban — while being looser is the bug.
func TestParserNeverAdmitsANonLiteral(t *testing.T) {
	dir := t.TempDir()
	lib := agentLib(t, dir)

	for _, addr := range banlistCandidates {
		fn := "valid_ip4"
		if strings.Contains(addr, ":") {
			fn = "valid_ip6"
		}
		parserOK := exec.Command("bash", "-c", "set -u; source "+lib+"; "+fn+" "+shellQuote(addr)).Run() == nil
		if _, err := netip.ParseAddr(addr); parserOK && err != nil {
			t.Errorf("parser accepted %q, which is not a literal address — nft would resolve it "+
				"as a hostname from the root agent", addr)
		}
	}
}

// banlistCandidates is the shared corpus: valid addresses, near-misses, and the
// strings the old character filter let through.
var banlistCandidates = []string{
	"1.2.3.4", "0.0.0.0", "255.255.255.255", "127.0.0.1", "66.249.66.1",
	"1.2.3.4.5", "999.999.999.999", "256.1.1.1", "1.2.3", "abcd", "0abc",
	"010.1.1.1", "1..2.3", ".1.2.3", "1.2.3.", "deadbeef", "ffff",
	"::1", "::", "dead::beef", "fe80::1", "2400:cb00::1", "1::", "::2",
	"2001:db8:0:0:0:0:0:1", "2001:0db8:85a3:0000:0000:8a2e:0370:7334",
	"1:2:3:4:5:6:7:8", "a::b:c:d:e:f:1", "a::b:c:d:e:f:1:2", "::::", ":::",
	"1:::2", "12345::1", "1:2:3:4:5:6:7:8:9", "1:2:3:4:5:6:7",
	":1:2:3:4:5:6:7:8", "1:2:3:4:5:6:7:8:", "g::1", "1::2::3",
	"0:0:0:0:0:0:0:0", "::ffff:1",
}

// TestParserNeverAdmitsWhatNftRejects runs the same corpus against the real
// consumer. nft is the process that would do the resolving, so where it can be
// run it is the authoritative oracle — but ONLY where it can be run.
//
// An earlier version of this test looked up `nft` on PATH and treated a non-zero
// exit as a rejection. On a GitHub runner the binary is installed and refuses to
// execute unprivileged, including `-c`, so CI reported that nft rejects
// "1.2.3.4". Presence is not capability; nftValidator probes for the difference.
func TestParserNeverAdmitsWhatNftRejects(t *testing.T) {
	nftBin := nftValidator()
	if nftBin == "" {
		t.Skip("nft cannot validate a ruleset here (absent, or refuses to run unprivileged) — " +
			"TestParserNeverAdmitsANonLiteral covers the same invariant against netip")
	}
	dir := t.TempDir()
	lib := agentLib(t, dir)

	for _, addr := range banlistCandidates {
		fn, set := "valid_ip4", "b4"
		if strings.Contains(addr, ":") {
			fn, set = "valid_ip6", "b6"
		}
		parserOK := exec.Command("bash", "-c", "set -u; source "+lib+"; "+fn+" "+shellQuote(addr)).Run() == nil

		rules := "table inet vs_probe {\n  set b4 { type ipv4_addr; flags timeout; }\n" +
			"  set b6 { type ipv6_addr; flags timeout; }\n}\n" +
			"add element inet vs_probe " + set + " { " + addr + " timeout 60s }\n"
		f := filepath.Join(dir, "probe.nft")
		if err := os.WriteFile(f, []byte(rules), 0o600); err != nil {
			t.Fatal(err)
		}
		if parserOK && exec.Command(nftBin, "-c", "-f", f).Run() != nil {
			t.Errorf("parser accepted %q but nft rejects it — this element would abort the batch "+
				"or trigger a hostname resolution from the root agent", addr)
		}
	}
}

// runReconcileBanlist drives the agent's reconcile_banlist against a stubbed nft.
// The stub fails any transaction containing a line matching failOn, which is how
// a batch is made to fail without needing a kernel or a malformed address the
// parser would now reject anyway.
func runReconcileBanlist(t *testing.T, banlist, failOn string) (state, reason, count string, calls []string) {
	t.Helper()
	dir := t.TempDir()
	lib := agentLib(t, dir)

	control := filepath.Join(dir, "control")
	if err := os.MkdirAll(control, 0o750); err != nil {
		t.Fatal(err)
	}
	// tier2.want gates the whole feature.
	if err := os.WriteFile(filepath.Join(control, "tier2.want"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(control, "banlist.txt"), []byte(banlist), 0o600); err != nil {
		t.Fatal(err)
	}

	bin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	callLog := filepath.Join(dir, "nft.calls")
	// `nft list table` succeeds so ensure_dyn_table is a no-op; `nft -f` records
	// the transaction it was given and fails it when it contains failOn.
	stub := "#!/usr/bin/env bash\n" +
		"if [ \"$1\" = list ]; then exit 0; fi\n" +
		"if [ \"$1\" = -f ]; then\n" +
		"  if [ \"$2\" = - ]; then body=\"$(cat)\"; else body=\"$(cat \"$2\")\"; fi\n" +
		"  printf '%s\\n---\\n' \"$body\" >>\"" + callLog + "\"\n" +
		"  if [ -n \"" + failOn + "\" ] && printf '%s' \"$body\" | grep -qF \"" + failOn + "\"; then\n" +
		"    echo 'Error: stubbed nft rejection' >&2; exit 1\n" +
		"  fi\n" +
		"  exit 0\n" +
		"fi\n" +
		"exit 0\n"
	if err := os.WriteFile(filepath.Join(bin, "nft"), []byte(stub), 0o700); err != nil {
		t.Fatal(err)
	}

	// VAYUSHIELD_CONTROL_DIR, not CONTROL_DIR: the script derives CONTROL_DIR from
	// it at source time, so setting the derived variable is silently discarded.
	cmd := exec.Command("bash", "-c", "set -u; source "+lib+"; reconcile_banlist")
	cmd.Env = append(os.Environ(),
		"PATH="+bin+":"+os.Getenv("PATH"),
		"VAYUSHIELD_CONTROL_DIR="+control,
	)
	var errBuf strings.Builder
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		t.Fatalf("reconcile_banlist failed (%v): %s", err, errBuf.String())
	}

	read := func(name string) string {
		b, err := os.ReadFile(filepath.Join(control, name))
		if err != nil {
			return ""
		}
		return strings.TrimSpace(string(b))
	}
	if b, err := os.ReadFile(callLog); err == nil {
		for _, c := range strings.Split(string(b), "\n---\n") {
			if strings.TrimSpace(c) != "" {
				calls = append(calls, c)
			}
		}
	}
	return read("offload.state"), read("offload.reason"), read("offload.count"), calls
}

// TestOneBadElementDoesNotFreezeEveryBan — the whole banlist was applied as ONE
// nft transaction, so a single element nft disliked took the flush down with it:
// nothing flushed, nothing added, every stale ban left in the kernel and — the
// part that harms a real person — every PARDON failing to lift. A visitor who
// solved the challenge stayed banned until the batch happened to become valid
// again, signalled only by offload.state=error.
func TestOneBadElementDoesNotFreezeEveryBan(t *testing.T) {
	const far = "9999999999" // an expiry well in the future
	banlist := strings.Join([]string{
		"1.2.3.4 " + far,
		"5.6.7.8 " + far,
		"9.10.11.12 " + far,
	}, "\n") + "\n"

	state, reason, count, calls := runReconcileBanlist(t, banlist, "5.6.7.8")

	if state == "error" {
		t.Errorf("one rejected element put the whole offload into error: reason=%q", reason)
	}
	if state != "degraded" {
		t.Errorf("state = %q, want %q — the panel must not read as fully healthy while bans are missing", state, "degraded")
	}
	if count != "2" {
		t.Errorf("count = %q, want 2 — the good bans should still have been applied", count)
	}

	// The flush must have gone through on its own, or pardons still do not lift.
	flushed := false
	for _, c := range calls {
		if strings.Contains(c, "flush set") && !strings.Contains(c, "5.6.7.8") {
			flushed = true
		}
	}
	if !flushed {
		t.Errorf("the flush was never applied separately — every pardon stays stuck:\n%v", calls)
	}
	if !strings.Contains(reason, "Pardons DID lift") {
		t.Errorf("reason %q does not tell the operator that pardons were honoured", reason)
	}
}

// TestCleanBanlistStaysOnTheAtomicPath — the fallback is a recovery path, not the
// normal one. A healthy batch must go through as a single transaction, or every
// poll turns into one nft invocation per banned IP.
func TestCleanBanlistStaysOnTheAtomicPath(t *testing.T) {
	state, _, count, calls := runReconcileBanlist(t, "1.2.3.4 9999999999\n5.6.7.8 9999999999\n", "")
	if state != "active" {
		t.Errorf("state = %q, want active", state)
	}
	if count != "2" {
		t.Errorf("count = %q, want 2", count)
	}
	if len(calls) != 1 {
		t.Errorf("a clean banlist took %d nft transactions, want 1 — the fallback is running unnecessarily", len(calls))
	}
}

// TestUnparseableLinesNeverReachNft — the point of parsing is that nft is never
// handed something it would resolve as a hostname. These lines must be dropped
// before the batch is built, not survive to be rejected later.
func TestUnparseableLinesNeverReachNft(t *testing.T) {
	banlist := strings.Join([]string{
		"1.2.3.4 9999999999",
		"abcd 9999999999",
		"999.999.999.999 9999999999",
		":::: 9999999999",
		"# a comment",
		"5.6.7.8 notanumber",
	}, "\n") + "\n"

	state, _, count, calls := runReconcileBanlist(t, banlist, "")
	if state != "active" {
		t.Errorf("state = %q, want active — dropping junk lines is not an error condition", state)
	}
	if count != "1" {
		t.Errorf("count = %q, want 1 — only the one real address should have been applied", count)
	}
	joined := strings.Join(calls, "\n")
	for _, junk := range []string{"abcd", "999.999.999.999", "::::"} {
		if strings.Contains(joined, junk) {
			t.Errorf("%q reached nft — it would be resolved as a hostname by the root agent:\n%s", junk, joined)
		}
	}
}

// TestDegradedOffloadIsNotRenderedAsIdle — the agent's new "degraded" state means
// enforcement is running but some bans were rejected. A switch statement that
// does not know the word falls through to the default, which reads
// "○ Idle — no jail verdicts to push": the most reassuring pill in the set, shown
// at the one moment the operator most needs to look.
func TestDegradedOffloadIsNotRenderedAsIdle(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("VAYUSHIELD_CONTROL_DIR", dir)
	for name, body := range map[string]string{
		"offload.state":  "degraded",
		"offload.count":  "7",
		"offload.reason": "applied 7 ban(s); 2 rejected by nft. Pardons DID lift.",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	row := (&App{}).shieldOffloadRow()
	if strings.Contains(row, "Idle") {
		t.Errorf("a degraded offload rendered as Idle:\n%s", row)
	}
	if !strings.Contains(row, "7") {
		t.Error("the row does not say how many bans ARE in force")
	}
	if !strings.Contains(row, "rejected") {
		t.Error("the row does not say that some bans were rejected")
	}
	// It must not read as fully healthy either.
	if strings.Contains(row, `is-on">● Enforcing — `) {
		t.Errorf("a degraded offload rendered with the healthy pill:\n%s", row)
	}
}

// TestOffloadReasonTruncatesOnRunes — the reason carries an nft error verbatim,
// which can contain non-ASCII. Slicing at a byte offset emits invalid UTF-8 into
// the page.
func TestOffloadReasonTruncatesOnRunes(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("VAYUSHIELD_CONTROL_DIR", dir)
	long := strings.Repeat("é", 300)
	if err := os.WriteFile(filepath.Join(dir, "offload.reason"), []byte(long), 0o600); err != nil {
		t.Fatal(err)
	}
	got := shieldOffloadReason()
	if !utf8.ValidString(got) {
		t.Errorf("truncation produced invalid UTF-8: %q", got)
	}
	if n := len([]rune(got)); n != 161 { // 160 runes + the ellipsis
		t.Errorf("truncated to %d runes, want 161", n)
	}
}
