package users

import (
	"context"
	"strings"
	"testing"
)

func TestCreateEditorRole(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	u, err := s.Create(ctx, "ed@example.com", "Ed", "password123", RoleEditor)
	if err != nil {
		t.Fatal(err)
	}
	if u.Role != RoleEditor {
		t.Errorf("role = %q, want editor", u.Role)
	}
	if _, err := s.Create(ctx, "x@example.com", "X", "password123", "superuser"); err == nil {
		t.Error("unknown role should be rejected")
	}
}

func TestSetRole(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	_, _ = s.Create(ctx, "a@example.com", "A", "password123", RoleAuthor)
	if err := s.SetRole(ctx, "a@example.com", RoleEditor); err != nil {
		t.Fatal(err)
	}
	list, _ := s.List(ctx)
	if len(list) != 1 || list[0].Role != RoleEditor {
		t.Errorf("expected role editor after SetRole, got %+v", list)
	}
	if err := s.SetRole(ctx, "a@example.com", "nope"); err == nil {
		t.Error("invalid role should be rejected")
	}
	if err := s.SetRole(ctx, "missing@example.com", RoleAdmin); err == nil {
		t.Error("setting role on unknown user should error")
	}
}

func TestUpdateProfile(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	u, _ := s.Create(ctx, "writer@example.com", "Writer", "password123", RoleAuthor)

	socials := map[string]string{
		"twitter": "https://twitter.com/writer",
		"github":  "https://github.com/writer",
		"blank":   "", // dropped
	}
	if err := s.UpdateProfile(ctx, u.ID, "Jane Writer", "I write about Go.", "https://cdn.example.com/a.png", socials); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetByID(ctx, u.ID)
	if got.Name != "Jane Writer" || got.Bio != "I write about Go." {
		t.Errorf("profile not saved: %+v", got)
	}
	if got.AvatarURL != "https://cdn.example.com/a.png" {
		t.Errorf("avatar not saved: %q", got.AvatarURL)
	}
	if len(got.Socials) != 2 || got.Socials["twitter"] == "" {
		t.Errorf("socials not saved correctly: %v", got.Socials)
	}
}

func TestSetMailAddress(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	u, _ := s.Create(ctx, "staff@example.com", "Staff", "password123", RoleAuthor)
	if err := s.SetMailAddress(ctx, u.ID, "Staff@Mail.Example.COM"); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetByID(ctx, u.ID)
	if got.MailAddress != "staff@mail.example.com" {
		t.Errorf("mail address = %q, want lowercased staff@mail.example.com", got.MailAddress)
	}
	if err := s.SetMailAddress(ctx, "missing", "x@y.com"); err == nil {
		t.Error("setting mail address on unknown user should error")
	}
	// Clearing the address is allowed.
	if err := s.SetMailAddress(ctx, u.ID, ""); err != nil {
		t.Fatal(err)
	}
	got, _ = s.GetByID(ctx, u.ID)
	if got.MailAddress != "" {
		t.Errorf("mail address should be cleared, got %q", got.MailAddress)
	}
}

func TestUpdateProfileValidation(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	u, _ := s.Create(ctx, "v@example.com", "V", "password123", RoleAuthor)

	if err := s.UpdateProfile(ctx, u.ID, "", "bio", "", nil); err == nil {
		t.Error("empty name should be rejected")
	}
	if err := s.UpdateProfile(ctx, u.ID, "Name", strings.Repeat("x", MaxBioLen+1), "", nil); err == nil {
		t.Error("over-long bio should be rejected")
	}
	if err := s.UpdateProfile(ctx, u.ID, "Name", "bio", "ftp://nope", nil); err == nil {
		t.Error("non-http avatar URL should be rejected")
	}
	if err := s.UpdateProfile(ctx, u.ID, "Name", "bio", "", map[string]string{"x": "javascript:alert(1)"}); err == nil {
		t.Error("non-http social link should be rejected")
	}
	// A valid update should pass.
	if err := s.UpdateProfile(ctx, u.ID, "Name", "bio", "https://example.com/a.png", map[string]string{"site": "https://example.com"}); err != nil {
		t.Fatalf("valid update rejected: %v", err)
	}
}
