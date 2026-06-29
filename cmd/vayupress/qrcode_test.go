package main

import (
	"strings"
	"testing"
)

func TestQRDataURI(t *testing.T) {
	if got := qrDataURI(""); got != "" {
		t.Errorf("empty input should yield empty string, got %q", got)
	}
	got := qrDataURI("otpauth://totp/VayuPress:you@example.com?secret=ABC&issuer=VayuPress")
	if !strings.HasPrefix(got, "data:image/png;base64,") {
		t.Errorf("expected a PNG data URI, got %.40q", got)
	}
	if len(got) < 200 {
		t.Errorf("data URI looks too short to be a real QR PNG: %d bytes", len(got))
	}
}
