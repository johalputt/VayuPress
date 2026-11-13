// SPDX-License-Identifier: Apache-2.0

package mail

import "testing"

// TestANameCannotTurnItsOwnLettersAround — the characters that reorder the
// text around them become "_", one by one at each end of both ranges, and
// the characters just outside them, and right-to-left text and its marks,
// are kept as they are.
func TestANameCannotTurnItsOwnLettersAround(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"invoice\u202Efdp.exe", "invoice_fdp.exe"},
		{"a\u202Ab", "a_b"},
		{"a\u202Db", "a_b"},
		{"a\u2066b", "a_b"},
		{"a\u2069b", "a_b"},
		{"a\u2029b", "a\u2029b"},
		{"a\u202Fb", "a\u202Fb"},
		{"a\u2065b", "a\u2065b"},
		{"a\u206Ab", "a\u206Ab"},
		{"تقرير\u200F.pdf", "تقرير\u200F.pdf"},
		{"דוח\u061C.pdf", "דוח\u061C.pdf"},
	} {
		if got := PlainName(c.in); got != c.want {
			t.Errorf("PlainName(%+q) = %+q, not %+q", c.in, got, c.want)
		}
	}
	// What Drive keeps, from the holder or from a link to upload into.
	if got, err := driveName(" invoice\u202Efdp.exe "); err != nil || got != "invoice_fdp.exe" {
		t.Errorf("driveName kept %+q, %v", got, err)
	}
}
