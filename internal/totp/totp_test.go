// SPDX-License-Identifier: Apache-2.0

package totp

import (
	"strings"
	"testing"
	"time"
)

// rfcSecret is the ASCII seed "12345678901234567890" from RFC 4226 Appendix D /
// RFC 6238 Appendix B, base32-encoded (no padding).
const rfcSecret = "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ"

// TestHOTPRFC4226Vectors checks the HOTP core against the ten published 6-digit
// vectors in RFC 4226 Appendix D.
func TestHOTPRFC4226Vectors(t *testing.T) {
	want := []string{
		"755224", "287082", "359152", "969429", "338314",
		"254676", "287922", "162583", "399871", "520489",
	}
	for i, w := range want {
		got, err := hotp(rfcSecret, uint64(i), 6)
		if err != nil {
			t.Fatalf("counter %d: %v", i, err)
		}
		if got != w {
			t.Errorf("counter %d: got %s, want %s", i, got, w)
		}
	}
}

// TestGenerateAtMatchesRFC verifies TOTP at a time whose 30s step equals HOTP
// counter 1 (t=59 → 59/30 = 1 → 287082).
func TestGenerateAtMatchesRFC(t *testing.T) {
	code, err := GenerateAt(rfcSecret, time.Unix(59, 0))
	if err != nil {
		t.Fatal(err)
	}
	if code != "287082" {
		t.Errorf("got %s, want 287082", code)
	}
}

func TestMatchAtRoundTrip(t *testing.T) {
	secret, err := GenerateSecret()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	code, err := GenerateAt(secret, now)
	if err != nil {
		t.Fatal(err)
	}
	step, ok := MatchAt(secret, code, now)
	if !ok {
		t.Error("freshly generated code failed validation")
	}
	if want := uint64(now.Unix() / Period); step != want {
		t.Errorf("matched step = %d, want %d", step, want)
	}
}

func TestMatchAtSkewTolerance(t *testing.T) {
	secret, _ := GenerateSecret()
	base := time.Unix(1_700_000_000, 0)
	// A code from the previous step must still validate at the current time,
	// and the matched counter is the PREVIOUS step (what a single-use consumer
	// must record).
	prev, _ := GenerateAt(secret, base.Add(-Period*time.Second))
	step, ok := MatchAt(secret, prev, base)
	if !ok || step != uint64(base.Unix()/Period-1) {
		t.Errorf("previous-step code: ok=%v step=%d — want ok with step one behind", ok, step)
	}
	// A code two steps away must NOT validate.
	old, _ := GenerateAt(secret, base.Add(-3*Period*time.Second))
	if _, ok := MatchAt(secret, old, base); ok {
		t.Error("code three steps old should be rejected")
	}
}

func TestMatchAtRejectsGarbage(t *testing.T) {
	secret, _ := GenerateSecret()
	for _, bad := range []string{"", "12345", "1234567", "abcdef", "  12 "} {
		if _, ok := MatchAt(secret, bad, time.Now()); ok {
			t.Errorf("garbage code %q should not validate", bad)
		}
	}
}

func TestGenerateSecretUnique(t *testing.T) {
	a, _ := GenerateSecret()
	b, _ := GenerateSecret()
	if a == b {
		t.Error("two generated secrets collided")
	}
	if len(a) < 16 {
		t.Errorf("secret too short: %q", a)
	}
}

func TestProvisioningURI(t *testing.T) {
	uri := ProvisioningURI(rfcSecret, "VayuPress", "admin@example.com")
	if !strings.HasPrefix(uri, "otpauth://totp/") {
		t.Errorf("bad scheme: %s", uri)
	}
	for _, want := range []string{"secret=" + rfcSecret, "issuer=VayuPress", "digits=6", "period=30", "algorithm=SHA1"} {
		if !strings.Contains(uri, want) {
			t.Errorf("URI missing %q: %s", want, uri)
		}
	}
}
