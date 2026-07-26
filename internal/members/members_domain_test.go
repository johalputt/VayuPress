// SPDX-License-Identifier: Apache-2.0

package members

import (
	"context"
	"testing"
)

// TestMemberDomainScoping proves VayuDomains Stage 4 attribution: a new signup is
// tagged with its domain scope ("" = primary), an existing member keeps its
// original domain regardless of the scope it next signs in from (email is
// globally unique), and CountsByDomain buckets members per domain.
func TestMemberDomainScoping(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	// Primary signup (unscoped Upsert → "").
	if m, err := s.Upsert(ctx, "a@example.com"); err != nil || m.DomainID != "" {
		t.Fatalf("primary Upsert: domain=%q err=%v, want ''", m.DomainID, err)
	}
	// Secondary signups.
	for _, e := range []string{"b@example.com", "c@example.com"} {
		if m, err := s.UpsertScoped(ctx, "dom-shop", e); err != nil || m.DomainID != "dom-shop" {
			t.Fatalf("secondary Upsert %s: domain=%q err=%v", e, m.DomainID, err)
		}
	}
	// An existing member is found by email regardless of scope and keeps its domain.
	again, err := s.UpsertScoped(ctx, "dom-shop", "a@example.com")
	if err != nil || again.DomainID != "" {
		t.Fatalf("existing member domain changed: %q err=%v, want ''", again.DomainID, err)
	}

	counts, err := s.CountsByDomain(ctx)
	if err != nil {
		t.Fatalf("counts: %v", err)
	}
	if counts[""] != 1 || counts["dom-shop"] != 2 {
		t.Fatalf("CountsByDomain = %+v, want {'':1,'dom-shop':2}", counts)
	}

	// Get is email-keyed and unscoped (login must work across domains).
	if m, err := s.Get(ctx, "b@example.com"); err != nil || m.DomainID != "dom-shop" {
		t.Fatalf("Get(secondary): domain=%q err=%v", m.DomainID, err)
	}
}
