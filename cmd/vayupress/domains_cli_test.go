// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"strings"
	"testing"

	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/domain"
)

// TestDomainsCLI covers the P4 read/notify surface the privileged TLS helper
// drives from — listing registered domains, emitting secondary hosts for
// scripts, recording tls_state — plus the P5 manual sync gate: a freshly
// registered domain is on hold and invisible to `hosts` until the operator
// approves it with `domains sync`.
func TestDomainsCLI(t *testing.T) {
	setupSEOTestDB(t) // config.Load + dbpkg.Init with DOMAIN=example.test
	ctx := context.Background()
	reg := domain.New(dbpkg.DB, dbpkg.RDB)
	if err := reg.EnsurePrimary(ctx, "example.test", domain.SiteBlog); err != nil {
		t.Fatalf("ensure primary: %v", err)
	}
	if _, err := reg.Create(ctx, "shop.example", domain.SiteBlog, true); err != nil {
		t.Fatalf("create secondary: %v", err)
	}

	// hosts: a new secondary is on manual hold, so the helper's work list is
	// EMPTY — adding a domain must never auto-provision it (P5).
	var b strings.Builder
	if err := runDomainsCLI([]string{"hosts"}, &b); err != nil {
		t.Fatalf("hosts: %v", err)
	}
	if got := strings.TrimSpace(b.String()); got != "" {
		t.Fatalf("hosts = %q, want empty (new domains are on manual hold)", got)
	}

	// hosts --hold surfaces what is waiting; --all lists regardless of gate.
	b.Reset()
	_ = runDomainsCLI([]string{"hosts", "--hold"}, &b)
	if got := strings.TrimSpace(b.String()); got != "shop.example" {
		t.Fatalf("hosts --hold = %q, want shop.example", got)
	}
	b.Reset()
	_ = runDomainsCLI([]string{"hosts", "--all"}, &b)
	if got := strings.TrimSpace(b.String()); got != "shop.example" {
		t.Fatalf("hosts --all = %q, want shop.example", got)
	}

	// sync: approving puts the domain on the work list (and off --hold).
	b.Reset()
	if err := runDomainsCLI([]string{"sync", "shop.example"}, &b); err != nil {
		t.Fatalf("sync: %v", err)
	}
	if !strings.Contains(b.String(), "sync_state=approved") {
		t.Errorf("sync output missing confirmation: %q", b.String())
	}
	b.Reset()
	if err := runDomainsCLI([]string{"hosts"}, &b); err != nil {
		t.Fatalf("hosts after sync: %v", err)
	}
	if got := strings.TrimSpace(b.String()); got != "shop.example" {
		t.Fatalf("hosts after sync = %q, want shop.example (primary must not be listed)", got)
	}
	b.Reset()
	_ = runDomainsCLI([]string{"hosts", "--hold"}, &b)
	if got := strings.TrimSpace(b.String()); got != "" {
		t.Fatalf("hosts --hold after sync = %q, want empty", got)
	}

	// hosts --mail: still lists the mail_enabled (now approved) secondary.
	b.Reset()
	_ = runDomainsCLI([]string{"hosts", "--mail"}, &b)
	if !strings.Contains(b.String(), "shop.example") {
		t.Errorf("hosts --mail missing the mail_enabled secondary: %q", b.String())
	}

	// hold: pausing takes it back off the work list.
	b.Reset()
	if err := runDomainsCLI([]string{"hold", "shop.example"}, &b); err != nil {
		t.Fatalf("hold: %v", err)
	}
	b.Reset()
	_ = runDomainsCLI([]string{"hosts"}, &b)
	if got := strings.TrimSpace(b.String()); got != "" {
		t.Fatalf("hosts after hold = %q, want empty", got)
	}

	// sync --all: bulk-approve every secondary at once. shop.example is held
	// again above, so exactly one row changes; the primary is never counted.
	b.Reset()
	if err := runDomainsCLI([]string{"sync", "--all"}, &b); err != nil {
		t.Fatalf("sync --all: %v", err)
	}
	if !strings.Contains(b.String(), "1 domain(s) set sync_state=approved") {
		t.Errorf("sync --all output = %q, want 1 domain approved", b.String())
	}
	b.Reset()
	_ = runDomainsCLI([]string{"hosts"}, &b)
	if got := strings.TrimSpace(b.String()); got != "shop.example" {
		t.Fatalf("hosts after sync --all = %q, want shop.example", got)
	}
	// Re-running is idempotent: nothing left on hold, so zero rows change.
	b.Reset()
	if err := runDomainsCLI([]string{"sync", "--all"}, &b); err != nil {
		t.Fatalf("sync --all (idempotent): %v", err)
	}
	if !strings.Contains(b.String(), "0 domain(s) set sync_state=approved") {
		t.Errorf("second sync --all = %q, want 0 changed", b.String())
	}
	// hold --all takes them all back off the work list.
	b.Reset()
	if err := runDomainsCLI([]string{"hold", "--all"}, &b); err != nil {
		t.Fatalf("hold --all: %v", err)
	}
	b.Reset()
	_ = runDomainsCLI([]string{"hosts"}, &b)
	if got := strings.TrimSpace(b.String()); got != "" {
		t.Fatalf("hosts after hold --all = %q, want empty", got)
	}
	// Re-approve for the remaining assertions.
	b.Reset()
	if err := runDomainsCLI([]string{"sync", "shop.example"}, &b); err != nil {
		t.Fatalf("re-sync: %v", err)
	}

	// sync/hold refuse the primary and unknown hosts.
	if err := runDomainsCLI([]string{"sync", "example.test"}, &b); err == nil {
		t.Error("sync should refuse the primary domain")
	}
	if err := runDomainsCLI([]string{"hold", "nowhere.example"}, &b); err == nil {
		t.Error("hold should refuse an unregistered host")
	}

	// list: human-readable table includes both, with roles and sync state.
	b.Reset()
	if err := runDomainsCLI([]string{"list"}, &b); err != nil {
		t.Fatalf("list: %v", err)
	}
	out := b.String()
	if !strings.Contains(out, "example.test") || !strings.Contains(out, "shop.example") || !strings.Contains(out, "primary") {
		t.Errorf("list output incomplete:\n%s", out)
	}
	if !strings.Contains(out, "SYNC") || !strings.Contains(out, "approved") {
		t.Errorf("list output missing sync column:\n%s", out)
	}

	// set-tls: records the state, and refuses the primary + bad states.
	b.Reset()
	if err := runDomainsCLI([]string{"set-tls", "shop.example", "active"}, &b); err != nil {
		t.Fatalf("set-tls: %v", err)
	}
	d, _ := reg.Resolve(ctx, "shop.example")
	if d.TLSState != domain.TLSActive {
		t.Errorf("tls_state = %q, want active", d.TLSState)
	}
	if err := runDomainsCLI([]string{"set-tls", "shop.example", "bogus"}, &b); err == nil {
		t.Error("set-tls accepted an invalid state")
	}
	if err := runDomainsCLI([]string{"set-tls", "example.test", "active"}, &b); err == nil {
		t.Error("set-tls should refuse the primary domain")
	}
}
