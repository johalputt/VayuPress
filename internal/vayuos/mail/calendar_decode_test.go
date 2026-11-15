// SPDX-License-Identifier: Apache-2.0

package mail

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"
)

// A calendar the decoder cannot read is an error the caller handles, never a
// panic: found by fuzzing parseFeed, "0;0=" crashed the decoder, and a
// subscribed feed is read where nothing recovers one.
func TestAMalformedCalendarIsAnErrorNotACrash(t *testing.T) {
	// The first three end a parameter with nothing after its "=", which
	// indexes an empty slice; the last puts a character after a quoted value,
	// which the decoder answers with a panic of its own.
	for _, data := range []string{"0;0=", "BEGIN:VCALENDAR\r\nX;A=", "BEGIN:VCALENDAR\r\nBEGIN:VEVENT\r\nUID;X=",
		"BEGIN:VCALENDAR\r\nX;A=\"b\"c:d\r\nEND:VCALENDAR\r\n"} {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("reading %q panicked: %v", data, r)
				}
			}()
			if _, err := decodeCalendar([]byte(data)); err == nil {
				t.Errorf("%q read as a calendar", data)
			}
			if _, err := parseFeed([]byte(data)); err == nil {
				t.Errorf("%q read as a feed", data)
			}
		}()
	}
}

// Every calendar is read through calendar_decode.go, which turns the
// decoder's panics into errors. A new call to ical.NewDecoder elsewhere
// would read outside input with nothing to catch one.
func TestEveryCalendarIsDecodedSafely(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fs := token.NewFileSet()
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") || f == "calendar_decode.go" {
			continue
		}
		af, err := parser.ParseFile(fs, f, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(af, func(n ast.Node) bool {
			if sel, ok := n.(*ast.SelectorExpr); ok && sel.Sel.Name == "NewDecoder" {
				if pkg, ok := sel.X.(*ast.Ident); ok && pkg.Name == "ical" {
					t.Errorf("%s: ical.NewDecoder outside calendar_decode.go; read it with decodeCalendar or calendarsIn", fs.Position(sel.Pos()))
				}
			}
			return true
		})
	}
}
