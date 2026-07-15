package main

import (
	"testing"

	"github.com/johalputt/vayupress/internal/users"
)

// TestOperatorSnapshot verifies the public account chip built for a signed-in
// VayuOS operator: it must flag operator + link to the console, fall back to the
// email when the display name is empty, and surface the avatar only when set.
func TestOperatorSnapshot(t *testing.T) {
	// Full profile with a name and avatar.
	got := operatorSnapshot(&users.User{
		Email:     "owner@example.com",
		Name:      "Site Owner",
		AvatarURL: "/media/avatars/owner.png",
	})
	if got["operator"] != true {
		t.Errorf("operator = %v, want true", got["operator"])
	}
	if got["console_url"] != "/os" {
		t.Errorf("console_url = %v, want /os", got["console_url"])
	}
	if got["name"] != "Site Owner" {
		t.Errorf("name = %v, want Site Owner", got["name"])
	}
	if got["avatar"] != "/media/avatars/owner.png" {
		t.Errorf("avatar = %v, want the avatar URL", got["avatar"])
	}

	// No display name → fall back to the email so the chip is never blank.
	noName := operatorSnapshot(&users.User{Email: "admin@example.com"})
	if noName["name"] != "admin@example.com" {
		t.Errorf("name fallback = %v, want the email", noName["name"])
	}
	// No avatar set → the key must be absent (chip falls back to the emoji).
	if _, ok := noName["avatar"]; ok {
		t.Error("avatar key must be omitted when the user has no avatar")
	}
}
