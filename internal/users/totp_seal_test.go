// SPDX-License-Identifier: Apache-2.0

package users

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/johalputt/vayupress/internal/totp"
)

// xorCodec is a reversible stand-in for the real at-rest codec so tests can
// prove sealing happens without pulling in the crypto stack.
type xorCodec struct{}

func (xorCodec) SealField(plain string) (string, error) {
	return "f1." + strings.Map(func(r rune) rune { return r + 1 }, plain), nil
}

func (xorCodec) OpenField(stored string) (string, error) {
	if !strings.HasPrefix(stored, "f1.") {
		return stored, nil // legacy plaintext passthrough
	}
	return strings.Map(func(r rune) rune { return r - 1 }, strings.TrimPrefix(stored, "f1.")), nil
}

func newTOTPTestStore(t *testing.T) *Store {
	t.Helper()
	s := newTestStore(t)
	s.UseTOTPCodec(xorCodec{})
	return s
}

// TestTOTPSecretSealedAtRest proves SetTOTPSecret never leaves the raw base32
// seed in the column while readers still see the plaintext value.
func TestTOTPSecretSealedAtRest(t *testing.T) {
	s := newTOTPTestStore(t)
	ctx := context.Background()
	u, err := s.Create(ctx, "seeded@example.com", "Seed", "a-long-password", RoleAdmin)
	if err != nil {
		t.Fatal(err)
	}
	const secret = "JBSWY3DPEHPK3PXP"
	if err := s.SetTOTPSecret(ctx, u.ID, secret); err != nil {
		t.Fatal(err)
	}
	var raw string
	if err := s.db.QueryRow(`SELECT totp_secret FROM users WHERE id=?`, u.ID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if raw == secret {
		t.Fatalf("totp_secret stored as plaintext: %q", raw)
	}
	if !strings.HasPrefix(raw, "f1.") {
		t.Fatalf("sealed blob missing f1 prefix: %q", raw)
	}
	got, enabled, err := s.TOTPSecretByEmail(ctx, "SEED@example.com")
	if err != nil || got != secret || enabled {
		t.Fatalf("open roundtrip: secret=%q enabled=%v err=%v", got, enabled, err)
	}
}

// TestConsumeTOTPStepRejectsReplay proves a matched code's step can be consumed
// exactly once: the first consume wins, any later one with the same or older
// step is refused — that is what kills replay of a captured code inside its
// validity window.
func TestConsumeTOTPStepRejectsReplay(t *testing.T) {
	s := newTestStore(t) // no codec needed; step logic is orthogonal
	ctx := context.Background()
	if _, err := s.Create(ctx, "replay@example.com", "R", "a-long-password", RoleAdmin); err != nil {
		t.Fatal(err)
	}
	secret := "JBSWY3DPEHPK3PXP"
	now := time.Now()
	step, ok := totp.MatchAt(secret, mustCode(t, secret, now), now)
	if !ok {
		t.Fatal("code did not validate")
	}
	if ok2, err := s.ConsumeTOTPStep(ctx, "replay@example.com", int64(step)); err != nil || !ok2 {
		t.Fatalf("first consume: ok=%v err=%v", ok2, err)
	}
	if ok2, err := s.ConsumeTOTPStep(ctx, "replay@example.com", int64(step)); err != nil || ok2 {
		t.Fatalf("replay accepted: ok=%v err=%v", ok2, err)
	}
	// An older step (skew window behind the consumed one) is also refused.
	if ok2, err := s.ConsumeTOTPStep(ctx, "replay@example.com", int64(step-1)); err != nil || ok2 {
		t.Fatalf("older step accepted: ok=%v err=%v", ok2, err)
	}
	// A strictly newer step still wins.
	if ok2, err := s.ConsumeTOTPStep(ctx, "replay@example.com", int64(step+1)); err != nil || !ok2 {
		t.Fatalf("newer step refused: ok=%v err=%v", ok2, err)
	}
}

func mustCode(t *testing.T, secret string, at time.Time) string {
	t.Helper()
	code, err := totp.GenerateAt(secret, at)
	if err != nil {
		t.Fatal(err)
	}
	return code
}
