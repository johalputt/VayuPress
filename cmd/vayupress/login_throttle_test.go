package main

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestLoginClientIPStripsPort(t *testing.T) {
	r := httptest.NewRequest("POST", "/admin/v3/login", nil)
	r.RemoteAddr = "203.0.113.7:54321"
	if got := loginClientIP(r); got != "203.0.113.7" {
		t.Errorf("loginClientIP with port: want 203.0.113.7, got %q", got)
	}
	// Already-normalised (no port, e.g. set by RealIP from XFF) passes through.
	r.RemoteAddr = "198.51.100.9"
	if got := loginClientIP(r); got != "198.51.100.9" {
		t.Errorf("loginClientIP without port: want 198.51.100.9, got %q", got)
	}
}

func TestLoginLockoutMessage(t *testing.T) {
	until := time.Date(2026, 6, 21, 9, 30, 0, 0, time.UTC)
	msg := loginLockoutMessage(until)
	if !strings.Contains(msg, "Too many failed sign-in attempts") {
		t.Errorf("lockout message missing prefix: %q", msg)
	}
	if !strings.Contains(msg, "09:30 UTC") {
		t.Errorf("lockout message missing formatted time: %q", msg)
	}
}
