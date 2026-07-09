package main

import (
	"strings"
	"testing"
)

func TestHxValsSafeValuesUnchanged(t *testing.T) {
	// Literal/sanitized values must emit the plain JSON form (byte-identical to
	// the pre-hardening output the HTMX tests assert).
	got := hxVals("status", "approved")
	if got != `hx-vals='{"status":"approved"}'` {
		t.Fatalf("safe hxVals = %q", got)
	}
	got = hxVals("user", "alice.bob", "folder", "Inbox", "id", "42")
	if got != `hx-vals='{"user":"alice.bob","folder":"Inbox","id":"42"}'` {
		t.Fatalf("safe multi hxVals = %q", got)
	}
}

func TestHxValsEscapesHostileValue(t *testing.T) {
	// A value carrying attribute/JSON metacharacters must not break out of the
	// single-quoted attribute or the JSON string. (In production user/folder are
	// already charset-restricted; this is defence in depth + the CodeQL barrier.)
	got := hxVals("user", `a'"><script>`)
	if strings.Contains(got, `'><script>`) || strings.Contains(got, `"><script>`) {
		t.Fatalf("hostile value broke out of the attribute: %q", got)
	}
	// The raw single quote (attribute delimiter) and angle brackets must be
	// HTML-escaped.
	if strings.Contains(got, `'`+"a") || strings.Contains(got, "<script") {
		t.Fatalf("dangerous characters not escaped: %q", got)
	}
}
