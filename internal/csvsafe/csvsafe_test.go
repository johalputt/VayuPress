// SPDX-License-Identifier: Apache-2.0

package csvsafe

import "testing"

// A cell a spreadsheet would run as a formula is written as text; one that
// would not is written as it is, a number included.
func TestACellIsNeverAFormula(t *testing.T) {
	for in, want := range map[string]string{
		`=HYPERLINK("https://evil.example","x")`: `'=HYPERLINK("https://evil.example","x")`,
		"+cmd|' /C calc'!A0":                     "'+cmd|' /C calc'!A0",
		"-2+3+cmd|' /C calc'!A0":                 "'-2+3+cmd|' /C calc'!A0",
		"@SUM(1+1)*cmd|' /C calc'!A0":            "'@SUM(1+1)*cmd|' /C calc'!A0",
		"\t=1+1":                                 "'\t=1+1",
		"\r=1+1":                                 "'\r=1+1",
		"Ada Lovelace":                           "Ada Lovelace",
		"-5":                                     "-5",
		"+44.5":                                  "+44.5",
		"":                                       "",
		"a=b":                                    "a=b",
	} {
		if got := Cell(in); got != want {
			t.Errorf("Cell(%q) = %q, want %q", in, got, want)
		}
	}
}
