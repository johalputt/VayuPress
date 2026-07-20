package mail

import "testing"

func TestIsReservedLocalpart(t *testing.T) {
	for _, s := range []string{"postmaster", "abuse", "admin", "Security", " root ", "billing", "no-reply", "mailer-daemon"} {
		if !IsReservedLocalpart(s) {
			t.Errorf("expected reserved: %q", s)
		}
	}
	// "hello" is reserved but "hello123" is a distinct, claimable name.
	for _, s := range []string{"john", "jane.doe", "ankush", "hello123", "team7"} {
		if IsReservedLocalpart(s) {
			t.Errorf("expected NOT reserved: %q", s)
		}
	}
}

func TestValidLocalpart(t *testing.T) {
	for _, s := range []string{"john", "jane.doe", "a_b-c", "user+tag", "x", "n123", "UPPER"} {
		if !ValidLocalpart(s) {
			t.Errorf("expected valid: %q", s)
		}
	}
	for _, s := range []string{"", ".john", "john.", "-john", "john-", "a..b", "джон", "john@x", "john doe"} {
		if ValidLocalpart(s) {
			t.Errorf("expected invalid: %q", s)
		}
	}
}
