// SPDX-License-Identifier: Apache-2.0

package main

import (
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/domain"
)

// A studio grants a client a number of branded mailboxes on request. Without a
// per-domain cap nothing stops one client's mail filling the disk that thirty
// clients share — STORAGE_QUOTA_GB is global and MailQuotaMB is per membership
// tier, so neither is a per-client limit.

// An allowance of 0 means NONE GRANTED, never unlimited. This is the whole
// safety property: a domain nobody has provisioned must not be able to create
// mailboxes because a struct field defaulted helpfully.
func TestAnUnsetAllowanceGrantsNothing(t *testing.T) {
	d := domain.Domain{Host: "client.test"}
	if got := d.Limits().Mailboxes; got != 0 {
		t.Fatalf("a domain with no config has allowance %d, want 0", got)
	}
	body := goFuncBody(readSourceFile(t, "admin_os_mysite.go"), "mailboxAllowanceExceeded")
	if body == "" {
		t.Fatal("mailboxAllowanceExceeded not found")
	}
	if !strings.Contains(body, "granted <= 0") {
		t.Error("a zero allowance is not treated as 'none granted'. If it means unlimited, " +
			"every domain the operator has not configured can create mailboxes without limit")
	}
	if !strings.Contains(body, "used >= granted") {
		t.Error("the used-vs-granted comparison is missing or not inclusive; `used > granted` " +
			"lets a domain create exactly one mailbox beyond its allowance")
	}
}

// An unresolvable domain must fail CLOSED. Treating it as unmetered would make
// the limit depend on the registry being reachable, which is the wrong thing for
// a limit to depend on.
func TestAnUnknownDomainGetsNoAllowance(t *testing.T) {
	body := goFuncBody(readSourceFile(t, "admin_os_mysite.go"), "mailboxAllowanceUsage")
	if body == "" {
		t.Fatal("mailboxAllowanceUsage not found")
	}
	if !strings.Contains(body, "err != nil || d.IsPrimary") {
		t.Error("mailboxAllowanceUsage does not return a zero grant for an unresolvable or " +
			"primary domain — an unknown host would then be unmetered")
	}
}

// The check must sit on the one path that can create a mailbox on a SECONDARY
// domain. The member self-claim paths pass an empty mailDomain and so only ever
// touch the primary, which is the agency's own install.
func TestTheAllowanceIsEnforcedWhereMailboxesAreCreated(t *testing.T) {
	body := goFuncBody(readSourceFile(t, "vayuos_mail.go"), "handleVayuOSAccountCreate")
	if body == "" {
		t.Fatal("handleVayuOSAccountCreate not found")
	}
	if !strings.Contains(body, "mailboxAllowanceExceeded") {
		t.Error("mailbox creation on a secondary domain does not consult the allowance, so a " +
			"client domain can hold unlimited mailboxes and the capacity claim is fiction")
	}
	// ...and it must be inside the secondary-domain branch, not applied to the
	// primary: metering the agency's own install would stop it creating its own
	// mailboxes for no reason.
	i := strings.Index(body, "acceptsSecondaryMailDomain")
	j := strings.Index(body, "mailboxAllowanceExceeded")
	if i < 0 || j < 0 || j < i {
		t.Error("the allowance check is not inside the secondary-domain branch")
	}
}

// The allowance lives in the same config_json blob as the brand a CLIENT can
// edit, so the merge behaviour is a security property here, not tidiness: a
// whole-blob brand save would let a client reset their own limit.
func TestAClientBrandSaveCannotResetTheirOwnAllowance(t *testing.T) {
	withLimit, err := domain.EncodeLimitsInto("", domain.Limits{Mailboxes: 5})
	if err != nil {
		t.Fatal(err)
	}
	afterBrand, err := domain.EncodeBrandConfigInto(withLimit, domain.Brand{SiteName: "Client Ltd"})
	if err != nil {
		t.Fatal(err)
	}
	if got := (domain.Domain{ConfigJSON: afterBrand}).Limits().Mailboxes; got != 5 {
		t.Fatalf("a brand save changed the allowance to %d, want 5 — a client could raise "+
			"their own mailbox limit by editing their site colours", got)
	}
	// Clearing the brand entirely must not clear it either.
	cleared, err := domain.EncodeBrandConfigInto(afterBrand, domain.Brand{})
	if err != nil {
		t.Fatal(err)
	}
	if got := (domain.Domain{ConfigJSON: cleared}).Limits().Mailboxes; got != 5 {
		t.Fatalf("clearing the brand reset the allowance to %d, want 5", got)
	}
	// ...and the site override survives an allowance change, in the other
	// direction, because the bug is symmetric.
	withSite, err := domain.EncodeSiteConfigInto(afterBrand, domain.SiteConfig{Mode: "custom"})
	if err != nil {
		t.Fatal(err)
	}
	bumped, err := domain.EncodeLimitsInto(withSite, domain.Limits{Mailboxes: 9})
	if err != nil {
		t.Fatal(err)
	}
	d := domain.Domain{ConfigJSON: bumped}
	if s, ok := d.Site(); !ok || s.Mode != "custom" {
		t.Errorf("raising the allowance erased the website override: %+v", s)
	}
	if b, ok := d.Brand(); !ok || b.SiteName != "Client Ltd" {
		t.Errorf("raising the allowance erased the brand: %+v", b)
	}
}

// A negative allowance is a typo. Clamping it to 0 silently REVOKES every
// mailbox the operator meant to grant, and reports success either way.
func TestANegativeAllowanceIsRefusedRatherThanClamped(t *testing.T) {
	body := goFuncBody(readSourceFile(t, "admin_os_domains.go"), "handleOSDomainAllowance")
	if body == "" {
		t.Fatal("handleOSDomainAllowance not found")
	}
	if !strings.Contains(body, "Mailboxes < 0") {
		t.Error("a negative allowance is not refused; clamped to 0 it revokes the grant " +
			"while telling the operator it saved")
	}
	if !strings.Contains(body, "isAdminRequest") {
		t.Error("the allowance endpoint has no admin gate")
	}
}

// A client must not be able to reach the endpoint that sets their own limit.
func TestTheAllowanceEndpointIsNotOnTheClientSurface(t *testing.T) {
	for _, p := range []string{
		"/os/api/domains", "/os/api/domains/abc/allowance", "/os/domains/abc",
	} {
		if clientPathAllowed(p) {
			t.Errorf("a client can reach %q — they could set their own allowance", p)
		}
	}
}
