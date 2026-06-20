package render

import (
	"strings"
	"testing"
)

// TestSafePathComponent verifies the cache filename-component sanitizer strips
// directory parts and traversal sequences (defence in depth for CodeQL
// "uncontrolled data in path expression") while preserving legitimate slugs.
func TestSafePathComponent(t *testing.T) {
	keep := map[string]string{
		"hello":      "hello",
		"a-b-c":      "a-b-c",
		"go_lang.v2": "go_lang.v2",
		"Post123":    "Post123",
	}
	for in, want := range keep {
		if got := safePathComponent(in); got != want {
			t.Errorf("safePathComponent(%q) = %q, want %q", in, got, want)
		}
	}

	// Traversal / separator payloads must never yield a separator or "..".
	bad := []string{
		"../etc/passwd",
		"../../etc/shadow",
		"posts/../../secret",
		"..",
		".",
		"/etc/passwd",
		"a/b/c",
		"....//....//",
	}
	for _, in := range bad {
		got := safePathComponent(in)
		if strings.ContainsAny(got, `/\`) || strings.Contains(got, "..") || got == "." {
			t.Errorf("safePathComponent(%q) = %q — still unsafe", in, got)
		}
		if got == "" {
			t.Errorf("safePathComponent(%q) returned empty (should fall back)", in)
		}
	}
}
