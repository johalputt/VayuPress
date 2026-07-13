package main

import (
	"context"
	"strings"
	"testing"

	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/domain"
)

// TestDomainsCLI covers the P4 read/notify surface the privileged TLS helper
// drives from: listing registered domains, emitting secondary hosts for scripts,
// and recording a domain's tls_state back into the registry.
func TestDomainsCLI(t *testing.T) {
	setupSEOTestDB(t) // config.Load + dbpkg.Init with DOMAIN=primary.example
	ctx := context.Background()
	reg := domain.New(dbpkg.DB, dbpkg.RDB)
	if err := reg.EnsurePrimary(ctx, "primary.example", domain.SiteBlog); err != nil {
		t.Fatalf("ensure primary: %v", err)
	}
	if _, err := reg.Create(ctx, "shop.example", domain.SiteBlog, true); err != nil {
		t.Fatalf("create secondary: %v", err)
	}

	// hosts: secondary hosts only, one per line.
	var b strings.Builder
	if err := runDomainsCLI([]string{"hosts"}, &b); err != nil {
		t.Fatalf("hosts: %v", err)
	}
	if got := strings.TrimSpace(b.String()); got != "shop.example" {
		t.Fatalf("hosts = %q, want shop.example (primary must not be listed)", got)
	}

	// hosts --mail: still lists the mail_enabled secondary.
	b.Reset()
	_ = runDomainsCLI([]string{"hosts", "--mail"}, &b)
	if !strings.Contains(b.String(), "shop.example") {
		t.Errorf("hosts --mail missing the mail_enabled secondary: %q", b.String())
	}

	// list: human-readable table includes both, with roles.
	b.Reset()
	if err := runDomainsCLI([]string{"list"}, &b); err != nil {
		t.Fatalf("list: %v", err)
	}
	out := b.String()
	if !strings.Contains(out, "primary.example") || !strings.Contains(out, "shop.example") || !strings.Contains(out, "primary") {
		t.Errorf("list output incomplete:\n%s", out)
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
	if err := runDomainsCLI([]string{"set-tls", "primary.example", "active"}, &b); err == nil {
		t.Error("set-tls should refuse the primary domain")
	}
}
