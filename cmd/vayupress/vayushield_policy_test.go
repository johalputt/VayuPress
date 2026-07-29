// SPDX-License-Identifier: Apache-2.0

package main

import (
	"net/http"
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/vayushield/policy"
)

// The panel takes free text, because these fields are paste targets: an
// operator arrives with a list already written down somewhere else. Everything
// below is about the gap between what they paste and what the compiler needs.

// TestPolicyLinesAcceptsHowOperatorsActuallyPaste — a list copied out of a
// firewall config arrives newline-separated; one copied out of an allowlist
// field arrives comma-separated. Accepting only one of the two forms reads as a
// bug in the product rather than in the paste.
func TestPolicyLinesAcceptsHowOperatorsActuallyPaste(t *testing.T) {
	got := policyLines("203.0.113.0/24, 198.51.100.7\n  \n2001:db8::/32 ; 10.0.0.0/8\n# a note\n")
	want := []string{"203.0.113.0/24", "198.51.100.7", "2001:db8::/32", "10.0.0.0/8"}
	if len(got) != len(want) {
		t.Fatalf("parsed %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("entry %d = %q, want %q", i, got[i], want[i])
		}
	}
	if len(policyLines("   \n\n")) != 0 {
		t.Error("whitespace produced entries, so an empty field would compile to a live rule set")
	}
}

// TestRouteCostsSkipUnreadableLinesRatherThanDefaultingThem — a line whose
// weight will not parse must be dropped, not silently weighted 1. A default
// looks identical to a correctly-read line while doing nothing, and the entire
// value of the field is that the operator chose the number.
func TestRouteCostsSkipUnreadableLinesRatherThanDefaultingThem(t *testing.T) {
	routes := parseRouteCosts(strings.Join([]string{
		"/search 8      # a full-text query, ~400ms",
		"/feed 3",
		"POST /contact 6",
		"mcp.example.test 2",
		"/broken",        // no weight
		"/worse notanum", // unparseable weight
		"/zero 0",        // a weight of zero is not a weight
		"",
	}, "\n"))
	if len(routes) != 4 {
		t.Fatalf("parsed %d routes, want 4: %+v", len(routes), routes)
	}

	r, bad := policy.Compile(policy.Config{Routes: routes})
	if len(bad) != 0 {
		t.Fatalf("compile rejected parsed routes: %v", bad)
	}
	if got := r.CostOf("h", "/search", http.MethodGet); got != 8 {
		t.Errorf("/search weighted %d, want 8", got)
	}
	if got := r.CostOf("h", "/broken", http.MethodGet); got != 1 {
		t.Errorf("a line with no weight took effect at %d — an unreadable rule must not become "+
			"an invisible one that still does something", got)
	}
	// A leading method scopes the rule, so weighting writes does not weight the
	// reads that share the path.
	if got := r.CostOf("h", "/contact", http.MethodPost); got != 6 {
		t.Errorf("POST /contact weighted %d, want 6", got)
	}
	if got := r.CostOf("h", "/contact", http.MethodGet); got != 1 {
		t.Errorf("GET /contact weighted %d — a method-scoped rule leaked to the read path", got)
	}
	// A bare token is a hostname, which is the in-app answer for a dedicated MCP
	// or API host that until now needed a shell script on the right machine.
	if got := r.CostOf("mcp.example.test", "/anything", http.MethodGet); got != 2 {
		t.Errorf("the host rule weighted %d, want 2", got)
	}
	if got := r.CostOf("example.test", "/anything", http.MethodGet); got != 1 {
		t.Errorf("a host rule leaked to another host at weight %d", got)
	}
}

// TestPolicyFieldIsBounded — these are textareas, and an unbounded one is a way
// to put megabytes into a settings row that is then re-parsed on every save and
// re-rendered on every page load. Truncation is on a line boundary so a clipped
// value never leaves a half-written prefix to be reported back as a parse error
// the operator did not make.
func TestPolicyFieldIsBounded(t *testing.T) {
	var b strings.Builder
	for b.Len() < maxPolicyFieldBytes*2 {
		b.WriteString("203.0.113.0/24\n")
	}
	clipped := clipPolicyField(b.String())
	if len(clipped) > maxPolicyFieldBytes {
		t.Errorf("clipped to %d bytes, over the %d cap", len(clipped), maxPolicyFieldBytes)
	}
	if strings.HasSuffix(clipped, "/2") || strings.HasSuffix(clipped, "203.0.113.0/") {
		t.Error("truncation split a line, so the operator would be shown a parse error for an " +
			"entry they wrote correctly")
	}
	_, bad := policy.Compile(policy.Config{AllowCIDRs: policyLines(clipped)})
	if len(bad) != 0 {
		t.Errorf("a clipped field produced parse failures: %v", bad)
	}
}
