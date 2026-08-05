// SPDX-License-Identifier: Apache-2.0

package main

import "testing"

// Locality decides whether a model step is an outbound call — and therefore
// whether it is refused in a Tor Space and charged against the egress ceiling.
//
// It fails CLOSED. Guessing "local" wrongly means a clearnet call out of a Tor
// Space; guessing "remote" wrongly means a refused local model. Those costs are
// not symmetric, so anything not confidently loopback is treated as remote.
func TestProviderLocalityFailsClosed(t *testing.T) {
	local := []string{
		"http://127.0.0.1:11434", "http://localhost:11434", "http://[::1]:11434",
		"HTTP://LocalHost:11434", "  http://127.0.0.1:11434  ",
	}
	for _, e := range local {
		if !isLoopbackEndpoint(e) {
			t.Errorf("%q should read as local; a local model would be refused in a Tor Space", e)
		}
	}
	remote := []string{
		"", "   ",
		"https://api.example.com/v1",
		"http://192.168.1.10:11434",
		"http://10.0.0.5:11434",
		// The near-misses that matter: a hostname that merely CONTAINS a
		// loopback name is not loopback, and treating it as such would be a
		// clearnet call from a Tor Space.
		"http://localhost.evil.example/v1",
		"http://127.0.0.1.evil.example/v1",
		"https://not-localhost.example",
		"ollama://localhost",
		// A userinfo section whose text looks like a loopback host.
		"http://127.0.0.1@evil.example/v1",
		"http://localhost@evil.example/v1",
		// Non-loopback IPs that share a prefix with one.
		"http://127.0.0.1.evil.example",
		"http://1.2.3.4",
		// A public IPv6 address.
		"http://[2001:db8::1]:11434",
	}
	for _, e := range remote {
		if isLoopbackEndpoint(e) {
			t.Errorf("%q read as local; anything not confidently loopback must be treated as remote", e)
		}
	}
}
