package render

import "testing"

// TestUnsafePathComponent verifies the cache-path guard flags traversal and
// separator payloads (defence in depth for CodeQL "uncontrolled data in path
// expression") while accepting legitimate slugs/tags.
func TestUnsafePathComponent(t *testing.T) {
	safe := []string{"hello", "a-b-c", "go_lang.v2", "Post123", "tag"}
	for _, s := range safe {
		if unsafePathComponent(s) {
			t.Errorf("expected %q to be accepted", s)
		}
	}

	unsafe := []string{
		"..",
		"../etc/passwd",
		"../../etc/shadow",
		"posts/../../secret",
		"/etc/passwd",
		"a/b/c",
		`a\b`,
		"x\x00y",
	}
	for _, s := range unsafe {
		if !unsafePathComponent(s) {
			t.Errorf("expected %q to be rejected", s)
		}
	}
}
