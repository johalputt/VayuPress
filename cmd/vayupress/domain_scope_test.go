package main

import "testing"

// TestDomCacheDir verifies the per-domain cache subdirectory name is always a
// safe single path segment — real ids are hex, but a path segment is never
// trusted blindly (it is joined onto the cache directory).
func TestDomCacheDir(t *testing.T) {
	cases := map[string]string{
		"a1b2c3d4e5f6a1b2c3d4e5f6": "a1b2c3d4e5f6a1b2c3d4e5f6", // normal hex id
		"":                         "x",
		"../../etc":                "______etc", // no traversal survives
		"a/b\\c":                   "a_b_c",
		"weird id!":                "weird_id_",
	}
	for in, want := range cases {
		if got := domCacheDir(in); got != want {
			t.Errorf("domCacheDir(%q)=%q, want %q", in, got, want)
		}
	}
	// Overlong ids are bounded.
	long := ""
	for i := 0; i < 200; i++ {
		long += "a"
	}
	if got := domCacheDir(long); len(got) != 64 {
		t.Errorf("domCacheDir over-long id length=%d, want 64", len(got))
	}
}
