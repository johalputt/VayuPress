// SPDX-License-Identifier: Apache-2.0

package verifiedbot

import (
	"context"
	"net"
	"testing"
)

// trackingResolver records whether it was ever consulted.
type trackingResolver struct{ called bool }

func (t *trackingResolver) LookupAddr(_ context.Context, _ string) ([]string, error) {
	t.called = true
	return []string{"crawl-66-249-66-1.googlebot.com."}, nil
}

func (t *trackingResolver) LookupHost(_ context.Context, _ string) ([]string, error) {
	t.called = true
	return []string{"66.249.66.1"}, nil
}

// TestOnionModeRefusesTheResolver — in an onion-only install nothing may make a
// clearnet callback (ADR-0141). safefetch closes the HTTP feed half; a DNS
// lookup has no equivalent switch and goes straight out through the system
// stack, past every guard the shield has.
//
// The call site carried a comment promising the verifier "degrades to
// UA-recognition rather than leaking a clearnet DNS/HTTP call" while passing
// net.DefaultResolver unconditionally — a guarantee asserted, not implemented.
func TestOnionModeRefusesTheResolver(t *testing.T) {
	tr := &trackingResolver{}
	v := New(Config{Resolver: tr, OnionMode: true})
	if v.resolver != nil {
		t.Error("a resolver survived into an onion-mode verifier — DNS can still leave the host")
	}

	// Control: with onion mode off the same wiring keeps the resolver, or the
	// assertion above would pass for the wrong reason.
	v2 := New(Config{Resolver: tr, OnionMode: false})
	if v2.resolver == nil {
		t.Error("clearnet mode dropped the resolver — reverse-DNS verification is disabled everywhere")
	}
}

// TestNilResolverIsTheDocumentedOffState — onion mode reuses the package's own
// "nil disables reverse-DNS verification" contract rather than inventing a
// second disabled state, so nothing downstream needs to learn a new rule.
func TestNilResolverIsTheDocumentedOffState(t *testing.T) {
	v := New(Config{Resolver: nil})
	if v.resolver != nil {
		t.Error("a nil Resolver did not stay nil")
	}
	var _ net.Addr // keep the net import meaningful if the file grows
}
