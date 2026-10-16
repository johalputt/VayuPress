// SPDX-License-Identifier: Apache-2.0

// Package csvsafe keeps text from outside an install from running as a
// formula when an export is opened in a spreadsheet.
//
// The exports carry what strangers wrote: a contact message's name and text,
// a member's name, a referrer, an address. A cell that begins with =, +, -
// or @ (or a tab or carriage return before one) is a formula to Excel,
// LibreOffice and Sheets, and =HYPERLINK or a DDE call there runs on the
// operator's computer when they open the file. Such a cell is written with a
// leading apostrophe, which every spreadsheet reads as "this is text" and
// does not show (OWASP, CSV injection). A plain number keeps its sign: -5 is
// a value, not a formula.
package csvsafe

import (
	"encoding/csv"
	"strconv"
)

// Cell is s as a spreadsheet will read it as text.
func Cell(s string) string {
	if s == "" {
		return s
	}
	switch s[0] {
	case '=', '+', '-', '@', '\t', '\r':
		if _, err := strconv.ParseFloat(s, 64); err == nil {
			return s
		}
		return "'" + s
	}
	return s
}

// Write writes one record to cw, each cell through Cell.
func Write(cw *csv.Writer, record []string) error {
	safe := make([]string, len(record))
	for i, c := range record {
		safe[i] = Cell(c)
	}
	return cw.Write(safe)
}
