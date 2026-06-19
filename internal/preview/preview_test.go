package preview

import (
	"errors"
	"testing"
	"time"
)

func TestIssueAndVerify(t *testing.T) {
	s := New("test-secret-key")
	tok := s.Issue("my-post", time.Hour)
	parsed, err := s.Verify(tok)
	if err != nil {
		t.Fatalf("verify failed: %v", err)
	}
	if parsed.Slug != "my-post" {
		t.Errorf("slug mismatch: %q", parsed.Slug)
	}
	if time.Until(parsed.ExpiresAt) < 59*time.Minute {
		t.Errorf("expiry too soon: %v", parsed.ExpiresAt)
	}
}

func TestVerify_Tampered(t *testing.T) {
	s := New("test-secret-key")
	tok := s.Issue("my-post", time.Hour)
	_, err := s.Verify(tok + "x")
	if err == nil {
		t.Fatal("expected error for tampered token")
	}
}

func TestVerify_Expired(t *testing.T) {
	s := New("test-secret-key")
	tok := s.Issue("my-post", -time.Second)
	_, err := s.Verify(tok)
	if !errors.Is(err, ErrExpired) {
		t.Errorf("want ErrExpired, got %v", err)
	}
}

func TestVerify_WrongSecret(t *testing.T) {
	s1 := New("secret-a")
	s2 := New("secret-b")
	tok := s1.Issue("post", time.Hour)
	_, err := s2.Verify(tok)
	if err == nil {
		t.Fatal("expected error for wrong secret")
	}
}
