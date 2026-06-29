package main

import "testing"

func TestForcedChangePathAllowed(t *testing.T) {
	allow := []string{"/os/change-password", "/os/logout", "/os/static/css/admin-os.css"}
	for _, p := range allow {
		if !forcedChangePathAllowed(p) {
			t.Errorf("%q should be allowed during forced change", p)
		}
	}
	deny := []string{"/os", "/os/posts", "/os/settings", "/os/api/profile"}
	for _, p := range deny {
		if forcedChangePathAllowed(p) {
			t.Errorf("%q should be blocked during forced change", p)
		}
	}
}

func TestGenerateInitialPassword(t *testing.T) {
	a, b := generateInitialPassword(), generateInitialPassword()
	if len(a) != 20 || len(b) != 20 {
		t.Fatalf("want 20-char passwords, got %d and %d", len(a), len(b))
	}
	if a == b {
		t.Error("two generated passwords must differ")
	}
}
