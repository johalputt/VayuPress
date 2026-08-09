// SPDX-License-Identifier: Apache-2.0

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/domain"
)

// FINDING (S8) — the mail switch told the operator it was not a delivery switch,
// and for a secondary domain it is one.
//
// The card under "This domain serves" said, in the sentence an operator acts on:
//
//	Turning mail off … does not stop delivery to a mailbox that already exists —
//	mail is served by address, and this switch is not consulted at delivery.
//
// The chain says otherwise. SMTPServer.recipientAccepted → Config.AcceptsMailDomain
// → MailAccepts, which vayuos.go wires to App.acceptsSecondaryMailDomain, which
// reads d.MailEnabled live from the registry. Turning the switch off therefore
// makes the receiving server decline every recipient on that domain at RCPT TO,
// and the sender gets a bounce.
//
// So an operator who flips it for the reason the card gives — "hides its mail
// card from the client" — silently stops that client's incoming mail. That is
// the worst shape a wrong claim can take: it is not merely inaccurate, it
// actively invites the action whose consequence it denies.
//
// The BEHAVIOUR is not the defect and is deliberately left alone: a domain the
// install is not set up to serve mail for should not be accepting mail for it,
// and recipientAccepted is the open-relay gate. The claim is what was wrong.

// theMailSwitchIsConsultedAtDelivery pins the three source facts that make the
// switch a delivery switch. All three must hold together — the predicate alone
// is inert if nothing wires it in, and the wiring alone proves nothing if the
// predicate stops reading the flag.
func theMailSwitchIsConsultedAtDelivery(t *testing.T) {
	t.Helper()
	pred := goFuncBody(readSourceFile(t, "middleware_domain.go"), "acceptsSecondaryMailDomain")
	if !strings.Contains(pred, "d.MailEnabled") {
		t.Fatal("acceptsSecondaryMailDomain no longer reads MailEnabled, so the switch is not " +
			"consulted at acceptance. If that is intended, the card's copy has to change back " +
			"with it — the two must never disagree again")
	}
	if !strings.Contains(readSourceFile(t, "vayuos.go"), "mailCfg.MailAccepts = a.acceptsSecondaryMailDomain") {
		t.Fatal("the predicate is not wired into the mail config, so nothing reaches it")
	}
	if !strings.Contains(goFuncBody(readSourceFile(t, "../../internal/vayuos/mail/smtpd.go"),
		"recipientAccepted"), "AcceptsMailDomain") {
		t.Fatal("the SMTP receive gate no longer asks AcceptsMailDomain")
	}
}

// The card must not deny a consequence the code produces.
func TestTheMailCardDoesNotDenyThatItStopsDelivery(t *testing.T) {
	theMailSwitchIsConsultedAtDelivery(t)

	card := domainServesCard(domain.Domain{ID: "abc123", Host: "client.example", SiteType: domain.SiteBlog}, true)
	low := strings.ToLower(card)

	for _, denial := range []string{
		"not consulted at delivery",
		"does not stop delivery",
		"not stop delivery",
	} {
		if strings.Contains(low, denial) {
			t.Fatalf("the card still says %q while acceptsSecondaryMailDomain gates RCPT TO on "+
				"this exact flag. An operator turning mail off to hide a card stops the "+
				"client's incoming mail and is told, on the same screen, that they have not", denial)
		}
	}

	// Denying it is half the defect; saying nothing is the other half. The card
	// has to name the consequence, or an operator who never read this test still
	// flips it blind.
	if !strings.Contains(low, "bounce") && !strings.Contains(low, "refus") {
		t.Error("the card does not say that new mail for this domain is refused, so the " +
			"consequence is now merely unstated rather than denied")
	}
}

// mailPredicateCallSites is every file in the mail stack the acceptance
// predicate reaches, with what it does there.
//
// A SET, not a count, and pinned in BOTH spellings — that distinction is the
// finding. The first version of this test checked that imapd.go and pop3d.go do
// not mention AcceptsMailDomain, and the comment beside it said the predicate
// had "exactly two call sites". It has three. The third arrives as an
// `accepts func(domain string) bool` parameter and never names AcceptsMailDomain
// at all, so a search for the name could not see it, and the number went into
// the changelog unchecked.
//
// Nothing was harmed by it — validRecoveryContact REFUSES a recovery address on
// a hosted domain, so turning mail off makes such an address acceptable rather
// than rejected, which is a widening that costs nobody access. That is luck, not
// method. The set is pinned so the next addition has to justify itself.
var mailPredicateCallSites = map[string]string{
	"internal/vayuos/mail/config.go":   "AcceptsMailDomain itself, and the MailAccepts hook it consults",
	"internal/vayuos/mail/smtpd.go":    "the RCPT gate — the delivery refusal the card describes",
	"internal/vayuos/mail/engine.go":   "isLocalRecipient — the same refusal from the other side",
	"internal/vayuos/mail/recovery.go": "validRecoveryContact — refuses a recovery address on a hosted domain",
	"cmd/vayupress/vayuos.go":          "wires MailAccepts to the registry-backed predicate",
	// NOT a consumer of the value: it hands the predicate to the account store.
	// Listed because it is how the recovery.go site is reached, and because it
	// sits outside internal/vayuos/mail — which is exactly the file the
	// one-directory glob could not see.
	"cmd/vayupress/admin_os_mail_recovery.go": "passes the predicate into SetRecoveryContactPending",
	// Prose, not code: the card's own header describes every entry above. It
	// matches because it NAMES them, which is the point — the description and
	// the set it describes fail together.
	"cmd/vayupress/admin_os_domain_serves.go": "the card's copy, which names all of the above",
}

// NOTE ON WHAT IS ABSENT. cmd/vayupress/middleware_domain.go defines
// acceptsSecondaryMailDomain — the predicate itself — and does not appear here,
// because "acceptsSecondaryMailDomain" does not contain the substring
// "AcceptsMailDomain" and the file names none of the other markers. That is not
// an oversight: theMailSwitchIsConsultedAtDelivery pins that file directly by
// function body, which is a stronger check than a substring scan. Recorded so
// the next reader does not "fix" the list by adding an entry the walk can never
// produce, and watch the test fail for it.

// What the switch does NOT do has to survive the correction, or the fix trades
// one wrong statement for a scarier wrong statement. Mailboxes and their stored
// mail are untouched, and sign-in never consults the flag.
func TestTheMailCardDoesNotOverstateWhatTurningItOffDestroys(t *testing.T) {
	card := strings.ToLower(domainServesCard(domain.Domain{ID: "abc123", Host: "client.example"}, true))
	for _, want := range []string{"mailbox", "delete"} {
		if !strings.Contains(card, want) {
			t.Errorf("the card never mentions %q — an operator cannot tell whether turning mail "+
				"off discards the stored mail, and the answer is that it does not", want)
		}
	}

	// The mechanism behind that reassurance: every place the predicate reaches,
	// found by BOTH the name and the parameter shape it travels under.
	// WALK THE REPO, not one directory.
	//
	// This globbed internal/vayuos/mail/*.go, so anything consulting the
	// predicate from outside that package was invisible — and one already did:
	// cmd/vayupress/admin_os_mail_recovery.go passes it into
	// SetRecoveryContactPending, which is the entry point for the recovery.go
	// site the correction above counts as the third. The claim "a fourth fails
	// the build" was therefore false in the same commit that made it: a fourth
	// consumer anywhere else in the tree changed nothing here.
	found := map[string]bool{}
	err := filepath.Walk("../..", func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			switch info.Name() {
			case ".git", "node_modules", "docs", "security-audit":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return nil
		}
		src, rerr := os.ReadFile(p)
		if rerr != nil {
			return rerr
		}
		body := string(src)
		if strings.Contains(body, "AcceptsMailDomain") ||
			strings.Contains(body, "MailAccepts") ||
			strings.Contains(body, "accepts func(domain string) bool") ||
			strings.Contains(body, "accepts(domain)") {
			rel := filepath.ToSlash(strings.TrimPrefix(filepath.Clean(p), "../../"))
			found[rel] = true
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}

	for f := range found {
		if _, known := mailPredicateCallSites[f]; !known {
			t.Errorf("%s now consults the mail-acceptance predicate and is not in the known set. "+
				"The card promises that turning mail off deletes nothing and locks nobody out — "+
				"re-establish that against this new use before adding it here", f)
		}
	}
	for f, why := range mailPredicateCallSites {
		if !found[f] {
			t.Errorf("%s no longer consults the predicate (%s). If that is deliberate the card's "+
				"copy may need to change with it", f, why)
		}
	}
	// The two that must never appear, named explicitly: these are sign-in, and
	// the card's reassurance is exactly that sign-in is untouched.
	for _, f := range []string{"internal/vayuos/mail/imapd.go", "internal/vayuos/mail/pop3d.go"} {
		if found[f] {
			t.Errorf("%s now consults the acceptance predicate, so turning mail off also locks "+
				"the client out of reading mail they already have — the card says it does not", f)
		}
	}
}

// The file header stated the same falsehood as prose, and a header is what the
// next person edits the copy from. Correcting the card alone would leave the
// source of the wrong sentence in place.
func TestTheFileHeaderDoesNotRestateTheRetractedClaim(t *testing.T) {
	src := readSourceFile(t, "admin_os_domain_serves.go")
	head := src
	if i := strings.Index(src, "\nimport ("); i > 0 {
		head = src[:i]
	}
	if strings.Contains(head, "NOT consulted by the mail stack") ||
		strings.Contains(head, "does not\n// stop delivery") {
		t.Fatal("the file header still describes mail_enabled as a presentation-only flag. " +
			"internal/vayuos/mail does read it, through MailAccepts, at RCPT TO")
	}
}
