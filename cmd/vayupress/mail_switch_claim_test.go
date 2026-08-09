// SPDX-License-Identifier: Apache-2.0

package main

import (
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

// What the switch does NOT do has to survive the correction, or the fix trades
// one wrong statement for a scarier wrong statement. Mailboxes and their stored
// mail are untouched, and IMAP/POP3/webmail sign-in never consults the flag —
// AcceptsMailDomain has exactly two delivery call sites, and neither is auth.
func TestTheMailCardDoesNotOverstateWhatTurningItOffDestroys(t *testing.T) {
	card := strings.ToLower(domainServesCard(domain.Domain{ID: "abc123", Host: "client.example"}, true))
	for _, want := range []string{"mailbox", "delete"} {
		if !strings.Contains(card, want) {
			t.Errorf("the card never mentions %q — an operator cannot tell whether turning mail "+
				"off discards the stored mail, and the answer is that it does not", want)
		}
	}

	// The mechanism behind that reassurance: the flag reaches delivery only.
	for _, f := range []string{"imapd.go", "pop3d.go"} {
		if strings.Contains(readSourceFile(t, "../../internal/vayuos/mail/"+f), "AcceptsMailDomain") {
			t.Errorf("%s now consults AcceptsMailDomain, so turning mail off also locks the "+
				"client out of reading mail they already have — the card says it does not", f)
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
