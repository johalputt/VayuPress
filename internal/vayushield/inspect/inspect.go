// SPDX-License-Identifier: Apache-2.0

// Package inspect recognises hostile request shapes on the public surface.
//
// # What this is NOT, stated first because the name invites the wrong belief
//
// This is not a web application firewall and it protects nothing. Injection
// defence in this product is structural and lives elsewhere: parameterised
// queries, output sanitising, a strict CSP with nonces, path hardening, and
// safefetch for SSRF. If a payload would work, the fact that this package
// noticed it changes nothing about whether it works. Nobody should relax any of
// those controls because this exists, and a WAF a defender trusts is worse than
// no WAF at all.
//
// What it does is CLASSIFY THE CLIENT. A request for /wp-login.php is not an
// attack on a Go binary that serves no PHP — it is a scanner identifying itself
// on its first request, for free, with a confidence the rest of the shield
// cannot reach that quickly.
//
// # Why it earns its place next to the behavioural scorer
//
// The behavioural scorer already catches path scanning through the 404 ratio,
// so the obvious question is what this adds. Two things:
//
//   - Speed. Behaviour needs minSample requests before any ratio means anything.
//     A single request for /.env is conclusive on request one.
//   - Discrimination. A high 404 ratio cannot tell a scanner from a site with
//     broken links or a stale feed. "Asked for a WordPress admin login" can.
//
// # The false-positive budget, and where it is spent
//
// A publishing platform is the worst possible place to run pattern matching,
// because its subject matter includes the patterns. An article about SQL
// injection contains SQL injection. A code block contains a script tag. So:
//
//   - Only the PATH and the QUERY are ever examined. Never a body, never a
//     header. The editor submits bodies; an author writing about attacks must be
//     able to publish.
//   - Rules are tiered by how much legitimate traffic could produce them, and
//     the tiers score very differently. A probe for foreign software is near
//     certain. A payload-shaped string in a query is NOT — someone can search
//     this very site for "UNION SELECT" — so it contributes a fraction as much.
//   - The total is bounded (MaxDelta) below the distance from an unknown client
//     to the block threshold, so this can move someone into a solvable challenge
//     and never on its own into a block.
//
// # Compiled in, and versioned with the binary
//
// The ruleset is a Go constant. It is never fetched. A downloaded ruleset would
// be a clearnet callback that a Tor Space forbids, an unsigned supply-chain
// dependency in a product that claims sovereignty, and a remote party's ability
// to change what this install refuses. RulesetVersion moves with the binary so
// an operator can tell which rules their build actually has.
//
// # Cost
//
// No regular expressions. Not for the usual reason — Go's RE2 does not
// backtrack, so it cannot be made to blow up — but because a substring scan over
// a size-capped buffer is an order of magnitude cheaper. The buffer is capped so
// an attacker cannot make the shield expensive by sending a megabyte of query
// string.
//
// Measured on a 2.1 GHz Xeon: ~420 ns and zero allocations for a typical article
// path with no query; ~4.4 µs and two allocations for a full-length query that
// needs decoding. It runs inside Classify, which already fingerprints the request
// and may consult the signature database, so it is a small fraction of a path
// that was never free — and it never runs at all for a verified session, a
// bypassed prefix, or a confirmed crawler.
//
// The obvious next optimisation is a trigger-byte prefilter that skips the rule
// loop for clean paths. It is deliberately not here: a prefilter is a second,
// simpler parser sitting in front of the first, and a request the prefilter and
// the rules disagree about is a bypass. That is precisely the bug the "+"
// handling below already had to be fixed for once.
package inspect

import (
	"strings"
)

// RulesetVersion identifies the compiled-in rules. It moves with the binary,
// so an operator can tell from the panel which rules this build has rather than
// having to trust that "latest" means anything.
const RulesetVersion = 1

// maxScan bounds how much of a request is examined.
//
// Real paths and queries are far under this. An attacker sending a megabyte of
// query string would otherwise make every rule cost a megabyte of scanning —
// turning an inspection layer into an amplifier, which is the failure mode this
// kind of code is most known for.
const maxScan = 1024

// Class groups rules by how confident they are, because the tiers are not
// remotely equivalent and scoring them equally would be the whole mistake.
type Class uint8

const (
	// ClassNone — nothing matched.
	ClassNone Class = iota
	// ClassForeignStack — a probe for software this binary is not. The highest
	// confidence available anywhere in the shield: this is a single Go binary
	// that serves no PHP, has no wp-admin and stores no .env, so a request for
	// one is a scanner working through a list. No reader's browser produces it.
	ClassForeignStack
	// ClassTraversal — path traversal or a null byte. A browser cannot emit
	// these by accident: they survive no normalisation and mean nothing to a
	// human-typed URL.
	ClassTraversal
	// ClassPayload — an injection-shaped string in a query. Deliberately the
	// weakest tier, because a reader can search a site for "UNION SELECT" and a
	// security blog's own search box will see every one of these legitimately.
	ClassPayload
)

// Finding describes what matched.
type Finding struct {
	Class Class
	// Rule is a stable identifier for metrics and the audit trail. Renaming one
	// silently breaks an operator's dashboard, so they are written once, here.
	Rule string
	// Where is "path" or "query", so an operator reading the log can tell a
	// probe for a file from a string inside a search box.
	Where string
}

// Delta is this finding's contribution to the client's score.
//
// The three tiers are far apart on purpose. A foreign-stack probe is as close to
// certain as this shield gets and is scored to reach a challenge on its own. A
// payload string is scored at a level where it takes several, alongside other
// evidence, to move anyone — because the single most likely producer of one on
// this platform is a person using the search box.
func (f Finding) Delta() float64 {
	switch f.Class {
	case ClassForeignStack:
		return 0.25
	case ClassTraversal:
		return 0.2
	case ClassPayload:
		return 0.08
	}
	return 0
}

// MaxDelta bounds this package's entire contribution.
//
// With the shipped defaults an unknown client starts at 0.25 and blocks at 0.8,
// so inspection can move one into a solvable challenge and cannot, alone, move
// one into a block. It shares that discipline with the behavioural scorer, and
// for the same reason: these are heuristics, and a heuristic that can reach a
// hard verdict will eventually reach a wrong one about a real person.
const MaxDelta = 0.3

// foreignStack are paths that belong to software this binary is not.
//
// Every entry is checked against the routes this application actually serves.
// The bar for adding one is that NO configuration of this product can ever
// serve it — not "we don't serve it today", because a rule that fires on a
// future feature is a rule that breaks it.
var foreignStack = []struct{ needle, rule string }{
	{".php", "php"},                                      // nothing in this binary executes PHP
	{"/wp-", "wordpress"},                                // wp-admin, wp-login, wp-content, wp-includes
	{"/xmlrpc", "xmlrpc"},                                //
	{"/.env", "dotenv"},                                  // credential file; never served
	{"/.git/", "git-dir"},                                // repository exposure probe
	{"/.aws/", "aws-creds"},                              //
	{"/.ssh/", "ssh-keys"},                               //
	{"/phpmyadmin", "phpmyadmin"},                        //
	{"/cgi-bin/", "cgi-bin"},                             // no CGI surface exists
	{"/vendor/phpunit", "phpunit-rce"},                   //
	{"/actuator/", "spring-actuator"},                    //
	{"/solr/", "solr"},                                   //
	{"/jenkins/", "jenkins"},                             //
	{"/.svn/", "svn-dir"},                                //
	{"/.ds_store", "ds-store"},                           //
	{"/config.json", "config-json"},                      // this product's config is never web-readable
	{"/.well-known/security.txt/", "security-txt-probe"}, // trailing-slash walk of a real path
}

// traversal are shapes a browser cannot produce by accident. The encoded forms
// are here as well as the decoded ones because the decode below is single-pass
// on purpose — decoding repeatedly until the string stops changing is its own
// well-known class of bug.
var traversal = []struct{ needle, rule string }{
	{"../", "dotdot"},
	{"..\\", "dotdot-win"},
	{"%2e%2e", "dotdot-encoded"},
	{"%252e", "dotdot-double-encoded"},
	{"....//", "dotdot-filtered"}, // survives a naive "strip ../" filter
	{"\x00", "null-byte"},
	{"%00", "null-byte-encoded"},
	{"/etc/passwd", "etc-passwd"},
	{"/proc/self/", "proc-self"},
	{"file://", "file-scheme"},
}

// payload are injection-shaped strings. The weakest tier, and the list is kept
// short deliberately: each entry is one more way for a reader's search to be
// mistaken for an attacker, and the marginal scanner caught by a longer list is
// already caught by the two tiers above.
var payload = []struct{ needle, rule string }{
	{"union select", "sqli-union"},
	{"' or '1'='1", "sqli-tautology"},
	{"or 1=1--", "sqli-or-comment"},
	{"sleep(", "sqli-timing"},
	{"benchmark(", "sqli-benchmark"},
	{"information_schema", "sqli-schema"},
	{"<script", "xss-script-tag"},
	{"javascript:", "xss-js-scheme"},
	{"onerror=", "xss-event-handler"},
	{"${jndi:", "jndi-lookup"},
	{"$(curl", "cmdi-subshell"},
	{"|curl ", "cmdi-pipe"},
	{";wget ", "cmdi-chain"},
	{"/bin/sh", "cmdi-shell"},
}

// Scan examines a request's path and query and returns the strongest finding.
//
// It returns the FIRST match in tier order rather than accumulating every match,
// which keeps a single crafted string from stacking a dozen rules into a score
// it was never meant to reach. One request is one piece of evidence.
func Scan(path, rawQuery string) (Finding, bool) {
	p := fold(path)
	for _, r := range foreignStack {
		if strings.Contains(p, r.needle) {
			return Finding{Class: ClassForeignStack, Rule: r.rule, Where: "path"}, true
		}
	}

	// The query is decoded once before scanning, and the raw form is scanned too.
	// Single-pass is deliberate: decoding in a loop until the value stops changing
	// turns %252e into a traversal that the application itself would never have
	// decoded that far, which is a finding about the scanner rather than about the
	// request.
	q := fold(rawQuery)
	qd := fold(decodeQueryOnce(rawQuery))

	for _, r := range traversal {
		if strings.Contains(p, r.needle) {
			return Finding{Class: ClassTraversal, Rule: r.rule, Where: "path"}, true
		}
		if strings.Contains(q, r.needle) || strings.Contains(qd, r.needle) {
			return Finding{Class: ClassTraversal, Rule: r.rule, Where: "query"}, true
		}
	}
	for _, r := range payload {
		if strings.Contains(qd, r.needle) || strings.Contains(q, r.needle) {
			return Finding{Class: ClassPayload, Rule: r.rule, Where: "query"}, true
		}
	}
	return Finding{}, false
}

// Reason renders a finding for the score's reason list and the audit trail.
func (f Finding) Reason() string {
	switch f.Class {
	case ClassForeignStack:
		return "probed for foreign software (" + f.Rule + ")"
	case ClassTraversal:
		return "path traversal shape in " + f.Where + " (" + f.Rule + ")"
	case ClassPayload:
		return "injection-shaped " + f.Where + " (" + f.Rule + ")"
	}
	return ""
}

// fold lowercases and size-caps in one pass, allocating only when the input
// actually contains an upper-case byte — which most paths do not.
func fold(s string) string {
	if len(s) > maxScan {
		s = s[:maxScan]
	}
	upper := false
	for i := 0; i < len(s); i++ {
		if c := s[i]; c >= 'A' && c <= 'Z' {
			upper = true
			break
		}
	}
	if !upper {
		return s
	}
	return strings.ToLower(s)
}

// decodeQueryOnce decodes a query string the way the application will, in a
// single pass, tolerating malformed escapes rather than rejecting them.
//
// Two decisions, both of which were bugs first.
//
// Malformed escapes are tolerated. net/url.QueryUnescape refuses the WHOLE
// string on one bad escape, which an attacker would use as an opt-out: append a
// stray "%" anywhere and the decoded scan never runs. Decoding what is decodable
// and passing the rest through removes that choice.
//
// "+" becomes a space, because in a query string it already is one — that is
// application/x-www-form-urlencoded, and it is what url.ParseQuery hands the
// handler. Leaving it alone (the first version did) meant "?q=union+select"
// matched nothing while the application read exactly "union select": a scanner
// differing from the parser is the oldest bypass there is, and building one into
// the scanner is worse than having no scanner. This function is only ever called
// on a query; a "+" in a PATH is a literal plus and is never touched.
func decodeQueryOnce(s string) string {
	if len(s) > maxScan {
		s = s[:maxScan]
	}
	if !strings.ContainsAny(s, "%+") {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if s[i] == '+' {
			b.WriteByte(' ')
			continue
		}
		if s[i] == '%' && i+2 < len(s) {
			if h, ok := unhex(s[i+1]); ok {
				if l, ok2 := unhex(s[i+2]); ok2 {
					b.WriteByte(h<<4 | l)
					i += 2
					continue
				}
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

func unhex(c byte) (byte, bool) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', true
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10, true
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10, true
	}
	return 0, false
}

// RuleCount reports how many rules this build carries, for the panel. An
// operator comparing two installs needs a number they can see, not a claim.
func RuleCount() int { return len(foreignStack) + len(traversal) + len(payload) }
