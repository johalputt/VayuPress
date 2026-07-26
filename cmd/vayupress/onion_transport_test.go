// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// TestOnionDialerRefusesClearnet is the deanonymisation-critical test: the
// guarded dialer must refuse to dial ANY host that is not a .onion, before it
// opens a connection, so the Tor lane can never touch clearnet.
func TestOnionDialerRefusesClearnet(t *testing.T) {
	dial, err := onionGuardedDialContext("127.0.0.1:9999") // dummy SOCKS addr; never contacted for the refusals
	if err != nil {
		t.Fatalf("build dialer: %v", err)
	}
	for _, addr := range []string{
		"example.com:80",
		"93.184.216.34:443", // bare IP
		"localhost:8080",
		"evil.com:443",
		"notreallyan.onion.evil.com:80", // .onion not the final label
		"onion:80",                      // bare label, no real onion
	} {
		if _, derr := dial(context.Background(), "tcp", addr); !errors.Is(derr, errNotOnion) {
			t.Fatalf("dial(%q) err = %v, want errNotOnion", addr, derr)
		}
	}
}

// TestOnionDialerAllowsOnion proves a .onion host passes the guard and proceeds
// to the SOCKS dial (which then fails to connect to the dummy proxy — a network
// error, NOT the guard's refusal), confirming the guard is host-shaped, not a
// blanket block.
func TestOnionDialerAllowsOnion(t *testing.T) {
	dial, err := onionGuardedDialContext("127.0.0.1:1") // port 1: connection will fail fast
	if err != nil {
		t.Fatalf("build dialer: %v", err)
	}
	_, derr := dial(context.Background(), "tcp", "abcdefghij234567qwerty.onion:80")
	if derr == nil {
		t.Fatal("expected a dial error against the dead SOCKS proxy")
	}
	if errors.Is(derr, errNotOnion) {
		t.Fatalf("a .onion host was wrongly refused by the guard: %v", derr)
	}
}

// TestNewOnionHTTPClientRequiresSocks proves the lane cannot be built — and so
// nothing is ever dialled — unless a Tor SOCKS address is configured.
func TestNewOnionHTTPClientRequiresSocks(t *testing.T) {
	if _, err := newOnionHTTPClient(""); err == nil {
		t.Fatal("newOnionHTTPClient(\"\") = nil error, want a configuration error")
	}
	if _, err := newOnionHTTPClient("   "); err == nil {
		t.Fatal("newOnionHTTPClient(whitespace) = nil error, want a configuration error")
	}
	c, err := newOnionHTTPClient("127.0.0.1:9050")
	if err != nil || c == nil {
		t.Fatalf("newOnionHTTPClient(valid) = (%v, %v), want a client", c, err)
	}
}

// TestOnionDeliverEnvelopeRefusesNonOnionPeer proves the delivery helper refuses a
// non-onion peer host outright.
func TestOnionDeliverEnvelopeRefusesNonOnionPeer(t *testing.T) {
	c, _ := newOnionHTTPClient("127.0.0.1:9050")
	_, _, _, err := onionDeliverEnvelope(context.Background(), c, "example.com", []byte("x"), "a@a.onion", "b@b.onion", 300, "store")
	if !errors.Is(err, errNotOnion) {
		t.Fatalf("deliver to clearnet peer err = %v, want errNotOnion", err)
	}
}

// TestTorSocksAddrDefaultEmpty documents that the lane is inert by default: with
// no env var set, no SOCKS address is returned.
func TestTorSocksAddrDefaultEmpty(t *testing.T) {
	t.Setenv("VAYUOS_TOR_SOCKS_ADDR", "")
	if got := torSocksAddr(); strings.TrimSpace(got) != "" {
		t.Fatalf("torSocksAddr() = %q, want empty when unset", got)
	}
	t.Setenv("VAYUOS_TOR_SOCKS_ADDR", " 127.0.0.1:9050 ")
	if got := torSocksAddr(); got != "127.0.0.1:9050" {
		t.Fatalf("torSocksAddr() = %q, want trimmed value", got)
	}
}
