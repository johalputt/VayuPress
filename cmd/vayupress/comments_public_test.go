// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/johalputt/vayupress/internal/comments"
)

// TestToPublicCommentOmitsPrivateFields pins the privacy contract of the public
// comment projection: the reader-facing JSON must carry the author, body, coarse
// country (for the flag) and timestamp, but NEVER the commenter's email or the
// finer region/city — the raw store record holds those, and returning it verbatim
// leaked emails to anyone who fetched a thread.
func TestToPublicCommentOmitsPrivateFields(t *testing.T) {
	c := &comments.Comment{
		ID: "c1", ArticleID: "a1", ParentID: "p1",
		Author: "Ankush", Email: "secret@example.com",
		Body: "hello", Status: "approved",
		Country: "IN", Region: "Punjab", City: "Ludhiana",
		CreatedAt: time.Date(2026, 7, 1, 15, 42, 0, 0, time.UTC),
	}
	b, err := json.Marshal(toPublicComment(c))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	s := string(b)

	// Private fields must be absent.
	for _, leak := range []string{"secret@example.com", "email", "Punjab", "Ludhiana", "region", "city", "article_id"} {
		if strings.Contains(s, leak) {
			t.Errorf("public comment leaked %q: %s", leak, s)
		}
	}
	// Public fields must be present.
	for _, want := range []string{`"id":"c1"`, `"author":"Ankush"`, `"body":"hello"`, `"country":"IN"`, `"parent_id":"p1"`, `"created_at":`} {
		if !strings.Contains(s, want) {
			t.Errorf("public comment missing %q: %s", want, s)
		}
	}
}
