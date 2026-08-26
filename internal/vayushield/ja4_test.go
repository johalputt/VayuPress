// SPDX-License-Identifier: Apache-2.0

package vayushield

import (
	"net/http"
	"testing"

	"github.com/johalputt/vayupress/internal/vayushield/brain"
)

func TestJA4OfTrustedHeaderOnly(t *testing.T) {
	r, _ := http.NewRequest("GET", "https://x.test/", nil)
	r.Header.Set("X-JA4", "t13d1516h2_8daaf6152771_b186095e22b6")

	m := New(Config{Enabled: true}) // JA4Header unset: channel OFF
	if got := m.JA4Of(r); got != "" {
		t.Fatalf("channel off but read header: %q", got)
	}

	m2 := New(Config{Enabled: true, JA4Header: "X-JA4"})
	if got := m2.JA4Of(r); got != "t13d1516h2_8daaf6152771_b186095e22b6" {
		t.Fatalf("trusted channel misread: %q", got)
	}

	big := make([]byte, 200)
	for i := range big {
		big[i] = 'a'
	}
	r.Header.Set("X-JA4", string(big))
	if got := m2.JA4Of(r); got != "" {
		t.Fatalf("oversized junk accepted: %q", got)
	}
}

func TestJA4StandingMovesWithVerdicts(t *testing.T) {
	m := New(Config{Enabled: true, JA4Header: "X-JA4"})
	r, _ := http.NewRequest("GET", "https://x.test/?utm_campaign=c", nil)
	r.Header.Set("X-JA4", "t13d1516h2_8daaf6152771_b186095e22b6")

	before := m.JA4Standing(r)
	if before != brain.Neutral {
		t.Fatalf("fresh fingerprint not neutral: %v", before)
	}
	for i := 0; i < 5; i++ {
		m.observeJA4(r, ActionBlock)
	}
	if s := m.JA4Standing(r); s >= brain.Neutral {
		t.Fatalf("poisoned fingerprint standing too high: %v", s)
	}
}
