package users

import (
	"context"
	"testing"
)

func TestNormalizeAndDeriveUsername(t *testing.T) {
	cases := map[string]string{
		"Ankush":          "ankush",
		"ankush.kumar":    "ankush-kumar",
		"  Hello  World ": "hello-world",
		"@@@":             "",
	}
	for in, want := range cases {
		if got := normalizeUsername(in); got != want {
			t.Errorf("normalizeUsername(%q)=%q, want %q", in, got, want)
		}
	}
	if got := deriveUsername("ankush@johal.in", "Ankush Johal"); got != "ankush" {
		t.Errorf("deriveUsername from email local part = %q, want ankush", got)
	}
}

func TestUsernameUniquenessAndLookup(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	u1, err := s.Create(ctx, "ankush@johal.in", "Ankush", "password123", "admin")
	if err != nil {
		t.Fatal(err)
	}
	if u1.Username != "ankush" {
		t.Fatalf("first user username = %q, want ankush", u1.Username)
	}
	// A second account whose local-part also slugs to "ankush" must get -2.
	u2, err := s.Create(ctx, "ankush@other.com", "Ankush", "password123", "author")
	if err != nil {
		t.Fatal(err)
	}
	if u2.Username != "ankush-2" {
		t.Errorf("second username = %q, want ankush-2", u2.Username)
	}
	got, err := s.GetByUsername(ctx, "ankush")
	if err != nil || got == nil || got.ID != u1.ID {
		t.Errorf("GetByUsername(ankush) should return the first user")
	}
}
