// SPDX-License-Identifier: Apache-2.0

package main

import (
	"os"
	"strings"
	"testing"
)

// TestPostureHeadlineNeverContradictsItsOwnTally guards a claim, not a count.
//
// The panel rendered "▲ 2 item(s) worth a look — nothing is failing." directly
// above "10 enforcing · 2 warning · 4 informational · 1 failing." Both numbers
// were computed correctly and by different rules: the tally counts raw Fail
// rows, the headline counts failures BEYOND the permanent volumetric limit that
// no install can clear. Neither branch either side of that one dropped the
// qualifier — only the middle branch did, which is the branch an install with
// warnings and no real failure lands in. The reassuring case, shown to the
// operator least likely to go looking.
//
// A posture panel exists to be believed. One sentence on it that a reader can
// disprove by looking two inches down costs more than the row it was hiding.
func TestPostureHeadlineNeverContradictsItsOwnTally(t *testing.T) {
	src, err := os.ReadFile("vayushield_audit.go")
	if err != nil {
		t.Skipf("source not readable here: %v", err)
	}
	s := string(src)

	// No branch may make an unqualified claim that nothing is failing, because
	// the tally beneath it always prints the raw Fail count and that count is
	// never zero on any install.
	for _, bad := range []string{
		"— nothing is failing.",
		"nothing is failing.</p>",
	} {
		if strings.Contains(s, bad) {
			t.Errorf("the posture headline claims %q while the tally below it prints the raw "+
				"Fail count, which includes the permanent limit and is never zero", bad)
		}
	}

	// Every branch of the summary must reference the permanent limit, since every
	// one of them is rendered above the same tally.
	head := funcSource(s, "func (a *App) shieldAuditBody(")
	if head == "" {
		t.Fatal("shieldAuditBody not found; this check is no longer anchored to anything")
	}
	if n := strings.Count(head, "permanent limit"); n < 3 {
		t.Errorf("only %d of the summary branches mention the permanent limit; all three are "+
			"rendered above a tally that counts it, so all three have to account for it", n)
	}
}

// funcSource returns the body of the function whose declaration starts with
// decl, up to the next top-level func.
func funcSource(src, decl string) string {
	i := strings.Index(src, decl)
	if i < 0 {
		return ""
	}
	rest := src[i+len(decl):]
	if j := strings.Index(rest, "\nfunc "); j >= 0 {
		return rest[:j]
	}
	return rest
}
