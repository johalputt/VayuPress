package main

import "testing"

// These lock in the go/reflected-xss barrier for the mail params: valid values
// pass through byte-identical (no behaviour change for real mailboxes/folders/
// ids), and any value carrying HTML metacharacters is either rejected to a safe
// default or escaped — so no raw markup can reach an HTML sink from user/folder/
// id, whether read from the query string OR a POST form.

func TestSanitizeMailFolder(t *testing.T) {
	cases := map[string]string{
		"Inbox":       "Inbox",
		"Sent":        "Sent",
		"My Folder-2": "My Folder-2", // spaces, digits, hyphen all valid
		"":            "Inbox",       // empty → default
		`<script>`:    "Inbox",       // markup → default
		`Inbox"onx`:   "Inbox",       // quote → default
		"a'b":         "Inbox",       // apostrophe → default
	}
	for in, want := range cases {
		if got := sanitizeMailFolder(in); got != want {
			t.Fatalf("sanitizeMailFolder(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSanitizeMailLocalPartAndID(t *testing.T) {
	// Valid local parts / ids are byte-identical (html.EscapeString is a no-op
	// on their charset), so engine lookups and comparisons are unaffected.
	if got := sanitizeMailLocalPart("john.doe+tag-1"); got != "john.doe+tag-1" {
		t.Fatalf("valid local part changed: %q", got)
	}
	if got := sanitizeMailID("1700000000.M123P456:2,S"); got != "1700000000.M123P456:2,S" {
		t.Fatalf("valid maildir id changed: %q", got)
	}
	// Hostile values are rejected to empty (never reach HTML).
	for _, bad := range []string{`a<b`, `x"y`, `z'q`, `a&b`, `a b`, `<script>`} {
		if got := sanitizeMailLocalPart(bad); got != "" {
			t.Fatalf("sanitizeMailLocalPart(%q) = %q, want empty", bad, got)
		}
		if got := sanitizeMailID(bad); got != "" {
			t.Fatalf("sanitizeMailID(%q) = %q, want empty", bad, got)
		}
	}
}
