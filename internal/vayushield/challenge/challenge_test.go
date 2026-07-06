package challenge

import (
	"testing"
	"time"
)

func TestPoWIssueSolveVerify(t *testing.T) {
	s := NewSigner([]byte("secret-key"))
	p, err := s.IssuePoW(DefaultDifficulty, time.Minute)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	nonce, ok := Solve(p.Salt, p.Difficulty, 5_000_000)
	if !ok {
		t.Fatal("solver failed to find nonce")
	}
	if err := s.VerifyPoW(p, nonce); err != nil {
		t.Fatalf("verify valid solution: %v", err)
	}
}

func TestPoWWrongNonceRejected(t *testing.T) {
	s := NewSigner([]byte("k"))
	p, _ := s.IssuePoW(DefaultDifficulty, time.Minute)
	if err := s.VerifyPoW(p, "definitely-not-a-solution"); err != ErrNotSolved {
		t.Fatalf("want ErrNotSolved, got %v", err)
	}
}

func TestPoWTamperedDifficultyRejected(t *testing.T) {
	s := NewSigner([]byte("k"))
	p, _ := s.IssuePoW(HardDifficulty, time.Minute)
	// Attacker lowers difficulty to make solving trivial.
	p.Difficulty = 1
	nonce, _ := Solve(p.Salt, 1, 1000)
	if err := s.VerifyPoW(p, nonce); err != ErrBadSignature {
		t.Fatalf("tampered difficulty must fail signature, got %v", err)
	}
}

func TestPoWExpired(t *testing.T) {
	s := NewSigner([]byte("k"))
	p, _ := s.IssuePoW(DefaultDifficulty, -time.Second) // already expired
	nonce, _ := Solve(p.Salt, p.Difficulty, 5_000_000)
	if err := s.VerifyPoW(p, nonce); err != ErrExpired {
		t.Fatalf("want ErrExpired, got %v", err)
	}
}

func TestPoWCrossSignerRejected(t *testing.T) {
	a := NewSigner([]byte("key-a"))
	b := NewSigner([]byte("key-b"))
	p, _ := a.IssuePoW(DefaultDifficulty, time.Minute)
	nonce, _ := Solve(p.Salt, p.Difficulty, 5_000_000)
	if err := b.VerifyPoW(p, nonce); err != ErrBadSignature {
		t.Fatalf("challenge from another signer must fail, got %v", err)
	}
}

func TestSessionTokenRoundTrip(t *testing.T) {
	s := NewSigner([]byte("k"))
	tok, err := s.IssueSession(time.Hour)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if !s.VerifySession(tok) {
		t.Fatal("valid token rejected")
	}
}

func TestSessionTokenExpired(t *testing.T) {
	s := NewSigner([]byte("k"))
	tok, _ := s.IssueSession(-time.Second)
	if s.VerifySession(tok) {
		t.Fatal("expired token accepted")
	}
}

func TestSessionTokenTamperRejected(t *testing.T) {
	s := NewSigner([]byte("k"))
	tok, _ := s.IssueSession(time.Hour)
	// Flip a character.
	b := []byte(tok)
	b[len(b)-1] ^= 0x01
	if s.VerifySession(string(b)) {
		t.Fatal("tampered token accepted")
	}
	if s.VerifySession("") || s.VerifySession("garbage") {
		t.Fatal("obviously invalid tokens accepted")
	}
}

func TestSessionCrossSignerRejected(t *testing.T) {
	a := NewSigner([]byte("a"))
	b := NewSigner([]byte("b"))
	tok, _ := a.IssueSession(time.Hour)
	if b.VerifySession(tok) {
		t.Fatal("token from another signer accepted")
	}
}

func TestDifficultyClamped(t *testing.T) {
	s := NewSigner([]byte("k"))
	p, _ := s.IssuePoW(999, time.Minute)
	if p.Difficulty > maxDifficulty {
		t.Fatalf("difficulty not clamped: %d", p.Difficulty)
	}
}
