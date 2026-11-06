// SPDX-License-Identifier: Apache-2.0

package mail

import (
	"testing"
	"time"
)

// Friday 6 November 2026, mid-afternoon UTC.
var sentenceNow = time.Date(2026, 11, 6, 15, 4, 0, 0, time.UTC)

// Each sentence is read into the terms written out beside it, as the search
// field shows and keeps them (String). One shape per row, so a row that
// fails names the shape that broke.
func TestASentenceIsReadIntoSearchTerms(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"pdf from priya last month", "pdf from:priya after:2026-10-01 before:2026-11-01 has:attachment"},
		{"from priya", "from:priya"},
		{"by priya", "from:priya"},
		{"to dana", "to:dana"},
		{"unread", "is:unread"},
		{"invoice with attachments", "invoice has:attachment"},
		{"files", "has:attachment"},
		{"in sent", "in:Sent"},
		{"today", "after:2026-11-06"},
		{"yesterday", "after:2026-11-05 before:2026-11-06"},
		{"this week", "after:2026-11-02"},
		{"last week", "after:2026-10-26 before:2026-11-02"},
		{"this month", "after:2026-11-01"},
		{"this year", "after:2026-01-01"},
		{"last year", "after:2025-01-01 before:2026-01-01"},
		{"october", "after:2026-10-01 before:2026-11-01"},
		{"in december", "after:2025-12-01 before:2026-01-01"},
		{"march 2024", "after:2024-03-01 before:2024-04-01"},
		{"in may", "after:2026-05-01 before:2026-06-01"},
	} {
		if got := ReadSearchSentence(c.in, sentenceNow).String(); got != c.want {
			t.Errorf("%q = %q, want %q", c.in, got, c.want)
		}
	}
}

// What is not one of the shapes stays a word, and a term typed wins over a
// phrase that would set the same thing, which stays as words the person
// sees: a sentence is only ever helped, never silently cut.
func TestASentenceKeepsWhatItDoesNotRead(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"the report may be late", "the report may be late"},
		{"in", "in"},
		{"from", "from"},
		{"in the garden", "in the garden"},
		{"this thing", "this thing"},
		{"from:dana from priya", "from priya from:dana"},
		{"after:2026-01-01 last month", "last month after:2026-01-01"},
		{`"last month" report`, `"last month" report`},
	} {
		if got := ReadSearchSentence(c.in, sentenceNow).String(); got != c.want {
			t.Errorf("%q = %q, want %q", c.in, got, c.want)
		}
	}
}
