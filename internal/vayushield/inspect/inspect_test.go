// SPDX-License-Identifier: Apache-2.0

package inspect

import (
	"strconv"
	"strings"
	"testing"
)

// This package's risk is not that it misses an attacker. It is that it is wrong
// about a reader on a platform whose subject matter includes the patterns it
// matches. Most of what follows is about that.

// TestAScannerIsIdentifiedOnItsFirstRequest is the reason this exists next to
// the behavioural scorer, which needs eight requests before any ratio means
// anything and still cannot tell a scanner from a site with broken links.
func TestAScannerIsIdentifiedOnItsFirstRequest(t *testing.T) {
	for _, p := range []string{
		"/wp-login.php",
		"/wp-admin/",
		"/.env",
		"/.git/config",
		"/phpmyadmin/index.php",
		"/vendor/phpunit/phpunit/src/Util/PHP/eval-stdin.php",
		"/cgi-bin/test.cgi",
		"/actuator/env",
		"/.aws/credentials",
	} {
		f, ok := Scan(p, "")
		if !ok {
			t.Errorf("%q was not recognised — a Go binary that serves no PHP has no legitimate "+
				"reason to receive this", p)
			continue
		}
		if f.Class != ClassForeignStack {
			t.Errorf("%q classified as %v, want ClassForeignStack", p, f.Class)
		}
		if f.Delta() < 0.2 {
			t.Errorf("%q scored %v — the highest-confidence tier in the shield should reach a "+
				"challenge on its own", p, f.Delta())
		}
	}
}

// TestRealRequestsAreNotFindings is the test that matters most. Every entry is
// a URL this product actually serves or a reader plausibly requests, and a
// single false positive here is a challenge in front of someone reading a blog.
func TestRealRequestsAreNotFindings(t *testing.T) {
	for _, tc := range []struct{ path, query string }{
		{"/", ""},
		{"/article/how-we-cut-p99-latency-in-half", ""},
		{"/static/css/site.css", "v=3.14.3"},
		{"/media/hero.webp", ""},
		{"/feed.xml", ""},
		{"/sitemap.xml", ""},
		{"/robots.txt", ""},
		{"/.well-known/security.txt", ""},
		{"/.well-known/acme-challenge/tokenvalue", ""},
		{"/os/settings", ""},
		{"/api/posts", "page=2&per_page=20"},
		{"/search", "q=go+concurrency+patterns"},
		{"/search", "q=100%25+uptime"}, // a percent sign in a real query
		{"/tag/c%2B%2B", ""},           // an encoded plus in a real slug
		{"/article/dot.separated.slug", ""},
		{"/mcp", ""},
		{"/oauth/authorize", "client_id=abc&redirect_uri=https%3A%2F%2Fclaude.ai%2Fcb"},
		{"/theme-assets/og", ""},
		{"/media/2026/07/screenshot-of-config.json.png", ""},
	} {
		if f, ok := Scan(tc.path, tc.query); ok {
			t.Errorf("%q?%q matched %q (%v) — this is a request this product serves or a reader "+
				"makes, and flagging it puts a challenge in front of a real visitor",
				tc.path, tc.query, f.Rule, f.Class)
		}
	}
}

// TestAnArticleAboutAttacksIsStillPublishable — the platform's subject matter
// includes its own attack patterns. Bodies are never inspected, so an author
// writing about SQL injection can publish; this pins the narrower guarantee
// that a payload string reaching the SEARCH box is scored as weak evidence
// rather than as an attack.
func TestAnArticleAboutAttacksIsStillPublishable(t *testing.T) {
	// "+" is a space in a query string, so this is literally "union select
	// explained" by the time the handler reads it. A scan that did not decode the
	// "+" would report nothing here while the application saw the phrase — a
	// scanner/parser differential, and the oldest bypass there is.
	f, ok := Scan("/search", "q=union+select+explained")
	if !ok {
		t.Fatal("a search whose words are joined by \"+\" matched nothing, but url.ParseQuery " +
			"turns those into spaces before the handler sees them — the scanner and the parser " +
			"disagree, which is a bypass")
	}
	if f.Class != ClassPayload {
		t.Fatalf("a search for a SQL phrase classified as %v — that tier is for things a reader "+
			"cannot plausibly type", f.Class)
	}
	const unknownStart, powThreshold = 0.25, 0.4
	if unknownStart+f.Delta() >= powThreshold {
		t.Errorf("one search for %q takes an unknown reader from %v to %v, past the %v challenge "+
			"threshold — a security blog's own search box would challenge its own audience",
			"union select", unknownStart, unknownStart+f.Delta(), powThreshold)
	}
}

// TestInspectionCannotBlockOnItsOwn — every rule here is a heuristic and each
// has some client that trips it legitimately, so this must be able to reach a
// solvable challenge and never a hard block by itself.
func TestInspectionCannotBlockOnItsOwn(t *testing.T) {
	// The worst single request the ruleset can produce.
	worst := 0.0
	for _, c := range []Class{ClassForeignStack, ClassTraversal, ClassPayload} {
		if d := (Finding{Class: c}).Delta(); d > worst {
			worst = d
		}
	}
	if worst > MaxDelta {
		t.Errorf("a single finding scores %v, over the %v bound", worst, MaxDelta)
	}
	const unknownStart, blockThreshold, powThreshold = 0.25, 0.8, 0.4
	if unknownStart+MaxDelta >= blockThreshold {
		t.Errorf("the full budget takes an unknown client from %v to %v, past the %v block "+
			"threshold — pattern matching must never reach a hard verdict alone",
			unknownStart, unknownStart+MaxDelta, blockThreshold)
	}
	if unknownStart+worst < powThreshold {
		t.Errorf("the strongest possible finding reaches only %v, below the %v challenge "+
			"threshold — the package would then change nothing", unknownStart+worst, powThreshold)
	}
}

// TestOneRequestIsOnePieceOfEvidence — Scan returns the strongest single
// finding rather than accumulating every match. Otherwise one crafted string
// containing a dozen needles stacks into a score the ruleset was never
// calibrated to produce.
func TestOneRequestIsOnePieceOfEvidence(t *testing.T) {
	f, ok := Scan("/wp-login.php", "q=union+select+../../etc/passwd+<script>+${jndi:ldap://x}")
	if !ok {
		t.Fatal("a request stuffed with every pattern matched nothing")
	}
	if f.Delta() > MaxDelta {
		t.Errorf("a request containing many patterns scored %v, over the %v budget — findings "+
			"are accumulating instead of resolving to one", f.Delta(), MaxDelta)
	}
}

// TestEncodingIsNotAnOptOut — an attacker who can avoid the scan by encoding,
// or by appending a stray "%" so a strict decoder gives up on the whole string,
// has an opt-out. net/url.QueryUnescape rejects the entire value on one bad
// escape, which is exactly that opt-out.
func TestEncodingIsNotAnOptOut(t *testing.T) {
	for _, q := range []string{
		"f=%2e%2e%2fetc%2fpasswd",
		"f=..%2f..%2fetc%2fpasswd",
		"f=%2E%2E/",                 // upper-case hex
		"f=%2e%2e%2f&broken=%zz",    // a malformed escape elsewhere in the string
		"f=%2e%2e%2f&trailing=100%", // a trailing percent
		"x=%3Cscript%3Ealert(1)",    // encoded payload
	} {
		if _, ok := Scan("/", q); !ok {
			t.Errorf("query %q was not recognised — encoding, or a deliberately malformed escape "+
				"elsewhere in the string, must not switch the scan off", q)
		}
	}
}

// TestScanIsBounded — an attacker sending a megabyte of query string must not
// make every rule cost a megabyte of scanning. An inspection layer that gets
// more expensive the more an attacker sends is an amplifier.
func TestScanIsBounded(t *testing.T) {
	huge := strings.Repeat("a", 4<<20)
	if _, ok := Scan("/"+huge, huge); ok {
		t.Error("a large benign string matched a rule")
	}
	// The needle is placed past the cap, so a match here would prove the scan is
	// unbounded rather than that the rule works.
	if _, ok := Scan("/", huge+"union select"); ok {
		t.Error("a needle beyond the scan cap was found, so the buffer is not capped and the " +
			"cost of inspection is chosen by the attacker")
	}
	if _, ok := Scan("/", "union select"+huge); !ok {
		t.Error("a needle within the cap was missed")
	}
}

// TestNoRuleMatchesTheEmptyRequest — a needle that is accidentally empty would
// match everything, turning the ruleset into a blanket verdict on all traffic.
func TestNoRuleMatchesTheEmptyRequest(t *testing.T) {
	if f, ok := Scan("", ""); ok {
		t.Errorf("the empty request matched %q — some rule has an empty needle and is therefore "+
			"firing on every request on the site", f.Rule)
	}
	for _, set := range [][]struct{ needle, rule string }{foreignStack, traversal, payload} {
		for _, r := range set {
			if r.needle == "" || r.rule == "" {
				t.Errorf("rule %q has an empty needle or name", r.rule)
			}
			if r.needle != strings.ToLower(r.needle) {
				t.Errorf("rule %q has an upper-case needle, which can never match the folded "+
					"input and is therefore dead", r.rule)
			}
		}
	}
}

// TestRuleNamesAreUnique — they are metrics labels and audit-trail entries. Two
// rules sharing a name make an operator's dashboard silently wrong.
func TestRuleNamesAreUnique(t *testing.T) {
	seen := map[string]bool{}
	for _, set := range [][]struct{ needle, rule string }{foreignStack, traversal, payload} {
		for _, r := range set {
			if seen[r.rule] {
				t.Errorf("rule name %q is used twice", r.rule)
			}
			seen[r.rule] = true
		}
	}
	if RuleCount() != len(seen) {
		t.Errorf("RuleCount() = %d but %d distinct rules exist — the panel would report a number "+
			"that is not the number of rules this build has", RuleCount(), len(seen))
	}
}

// BenchmarkScanTypicalRequest measures the cost on the shape that actually
// dominates: a clean article path with no query. This runs on every public
// request, so a claim that it is cheap needs a number behind it.
func BenchmarkScanTypicalRequest(b *testing.B) {
	for i := 0; i < b.N; i++ {
		Scan("/article/how-we-cut-p99-latency-in-half", "")
	}
}

// BenchmarkScanWorstCase is a full-length query that decodes, so the allocation
// path and every rule tier run.
func BenchmarkScanWorstCase(b *testing.B) {
	q := strings.Repeat("k"+strconv.Itoa(7)+"=%41%42%43&", 60)
	for i := 0; i < b.N; i++ {
		Scan("/search", q)
	}
}
