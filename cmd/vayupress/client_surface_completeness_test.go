// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/auth"
	"github.com/johalputt/vayupress/internal/users"
)

// SECTION 4 AUDIT — the confinement attacked from the side nobody attacks it
// from: the customer's.
//
// Every existing test on clientSurface asks "what can a client reach that they
// were not given?" and the answers are good. None asks the inverse, and the
// inverse is where this one failed: a bound client could open the composer and
// could NOT send, because /os/vayumail/compose is on the surface and
// /os/vayumail/send is not. Same for the draft autosave, mailbox search, the
// contacts panel the inbox renders, attachment downloads, and the app-password
// button that is the entire purpose of the Connect page they were given.
//
// Why this belongs in a security audit rather than a bug list: the cheapest
// repair for "Send is broken" is to widen /os/vayumail/compose to /os/vayumail,
// and that one-word diff re-admits accounts/create, accounts/delete,
// accounts/update, TOTP, DNS, PGP and the security page to every client on the
// install. handlers_client.go says so in a comment and the comment was right —
// what was missing was the pressure that makes someone reach for it, and a
// broken Send button is exactly that pressure. Closing the gap precisely removes
// the reason anyone would ever widen the prefix.
//
// The narrow additions are safe because each handler behind them already refuses
// a non-admin acting on another mailbox — mailReader forces a non-admin onto
// their own key, contactOwner and canManageAppPassword do the same, and
// handleVayuOSSend overwrites From with the caller's own address. Those are not
// assumed here: TestAClientReachingTheNewEndpointsStillCannotTouchAnother-
// Mailbox drives them.

// clientFunctionalRe extracts the endpoints a rendered page DEPENDS ON, as
// distinct from the ones it merely links to.
//
// hx-get/hx-post/action/src are machinery: if one of them is refused, a control
// on the page the client was given does not work. A bare href is navigation to
// another area, and a client bouncing off the operator's console is the
// confinement doing its job — so href is deliberately not matched here.
var clientFunctionalRe = regexp.MustCompile(`(?:action|src|hx-get|hx-post)="(/os/[^"?#]*)`)

// clientMailPages renders each page the surface grants a bound client and
// returns the functional endpoints it emits.
func clientMailPages(t *testing.T, a *App, cl *users.User) map[string][]string {
	t.Helper()
	out := map[string][]string{}
	for name, h := range map[string]http.HandlerFunc{
		"inbox":   a.handleVayuOSInbox,
		"compose": a.handleVayuOSCompose,
		"sent":    a.handleVayuOSSent,
		"connect": a.handleVayuOSConnect,
		"message": a.handleVayuOSMessage,
	} {
		req := withUser(httptest.NewRequest(http.MethodGet, "/os/vayumail/"+name, nil), cl)
		req = req.WithContext(context.WithValue(req.Context(), ctxAccessKey, accessMailOnly))
		rec := httptest.NewRecorder()
		h(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s rendered %d, so this test is not looking at the page a client sees", name, rec.Code)
		}
		seen := map[string]bool{}
		for _, m := range clientFunctionalRe.FindAllStringSubmatch(rec.Body.String(), -1) {
			if !seen[m[1]] {
				seen[m[1]] = true
				out[name] = append(out[name], m[1])
			}
		}
		sort.Strings(out[name])
	}
	return out
}

// boundClient is a client of the studio, bound to one domain, holding the
// mailbox resetSessionApp seeds.
func boundClient() *users.User {
	return &users.User{
		ID: "client-1", Email: "boss@example.com", Role: users.RoleClient,
		ClientDomainID: "a1b2c3d4e5f6a1b2c3d4e5f6", MailAddress: "boss@example.com",
	}
}

// THE FINDING, derived rather than restated: whatever the client's own pages
// emit, they must be able to reach. A widget added to the inbox next year that
// posts somewhere undeclared fails here rather than in a support ticket.
func TestEveryControlOnAClientsOwnPagesIsReachable(t *testing.T) {
	a := resetSessionApp(t)
	for page, endpoints := range clientMailPages(t, a, boundClient()) {
		for _, p := range endpoints {
			if !clientPathAllowed(p) {
				t.Errorf("the %s page a client is given emits %q, which the confinement refuses.\n\n"+
					"That control does not work for the customer it was built for. The cheap "+
					"repair is to widen the surface entry to /os/vayumail, which re-admits "+
					"every mailbox-administration route on the install — so the gap has to be "+
					"closed precisely instead.", page, p)
			}
		}
	}
}

// Three endpoints the renderer cannot surface on an empty mailbox, named here
// with the reason rather than derived, because a test that silently covers less
// than it appears to is worse than one that says what it covers:
//
//   - send and draft are issued by static/js/admin-os-mail.js, not by markup.
//     That file also drives the operator's Accounts page, so its endpoint list
//     cannot be asserted wholesale — these two are the composer's.
//   - attachment is rendered only on a message that HAS an attachment.
func TestTheComposerAndAttachmentEndpointsAreReachable(t *testing.T) {
	for path, consequence := range map[string]string{
		"/os/vayumail/send":       "the client writes a message, presses Send, and gets a 403",
		"/os/vayumail/draft":      "the composer's autosave fails silently and unsent work is lost on a reload",
		"/os/vayumail/attachment": "the client cannot download a file someone sent them",
	} {
		if !clientPathAllowed(path) {
			t.Errorf("%s is refused to a bound client: %s", path, consequence)
		}
	}
}

// The avatar chip is emitted by a pure function, so it is derived rather than
// named — mailAvatarImg only produces an <img> for a local mailbox that HAS a
// picture, which is why the empty-mailbox render above never reaches it.
func TestTheAvatarChipAClientsInboxRendersIsReachable(t *testing.T) {
	got := mailAvatarImg("Boss <boss@example.com>", map[string]bool{"boss@example.com": true})
	m := clientFunctionalRe.FindStringSubmatch(got)
	if m == nil {
		t.Fatalf("fixture wrong: mailAvatarImg emitted no functional URL (%s)", got)
	}
	if !clientPathAllowed(m[1]) {
		t.Errorf("every message in a client's inbox from a mailbox with a profile picture "+
			"renders %q, which the confinement refuses — a broken image on every row", m[1])
	}
}

// THE SECURITY HALF. Widening a surface is only safe if the handlers behind it
// refuse a client acting on someone else's mailbox. Asserted by driving them,
// not by reading their comments.
func TestAClientReachingTheNewEndpointsStillCannotTouchAnotherMailbox(t *testing.T) {
	a := resetSessionApp(t)
	cl := boundClient()
	const victim = "another-client@example.com"

	// The victim mailbox must REALLY EXIST. Pointing this at an absent address
	// conflates the authorization gate with the does-this-mailbox-exist check
	// that runs after it: a mutation removing the authorization was still stopped
	// by the second one, so the test scored a kill it had not earned.
	hash, err := auth.HashSecretArgon2id("the-other-clients-password")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if err := a.vayuMail.Accounts().Create(context.Background(), victim, hash, "Another", "mailbox"); err != nil {
		t.Fatalf("seed the victim mailbox: %v", err)
	}

	// App passwords: minting one for another mailbox is a silent IMAP read of the
	// whole thing, so this is the sharpest of the three.
	rec := postAppPwForm(a.handleVayuOSAppPasswordCreate, "/os/vayumail/accounts/apppassword",
		url.Values{"email": {victim}, "label": {"their phone"}}, cl)
	if rec.Code != http.StatusForbidden {
		t.Errorf("a client minted an app password for %s (status %d).\n\n"+
			"An app password reads the entire mailbox over IMAP and leaves no ledger "+
			"entry — this is the quietest possible takeover.\n%s", victim, rec.Code, rec.Body.String())
	}
	// Status is what the caller sees; the credential is what does the damage.
	// Assert on the row, so a handler that refuses loudly and writes anyway fails.
	if creds := a.vayuMail.Accounts().AppPasswordCredentials(context.Background(), victim); len(creds) != 0 {
		t.Errorf("%d app-password credential(s) exist on %s after a client asked for one. "+
			"Whatever the response said, that mailbox is now readable over IMAP by "+
			"someone who was never given it", len(creds), victim)
	}

	// Contacts: the panel threads ?user= through so an ADMIN can act on another
	// mailbox. A client naming one must be pinned to their own.
	rec = postAppPwForm(a.handleVayuOSContactAdd, "/os/vayumail/contacts/add",
		url.Values{"user": {victim}, "email": {"x@example.net"}, "name": {"X"}}, cl)
	if rec.Code == http.StatusOK && !strings.Contains(rec.Body.String(), "boss@example.com") {
		t.Errorf("a client posted a contact naming user=%s and the returned panel is not "+
			"their own mailbox's — the ?user= parameter is being honoured for a "+
			"non-admin.\n%s", victim, rec.Body.String())
	}
	if owner, ok := a.contactOwner(withUser(httptest.NewRequest(http.MethodGet, "/", nil), cl), victim); !ok || owner != "boss@example.com" {
		t.Errorf("contactOwner resolved %q for a client asking for %s; a non-admin has exactly "+
			"one address book", owner, victim)
	}

	// Reads: a client asking for another mailbox reads their own, never a refusal
	// that would confirm the other exists.
	rd := mailReaderFor(false, "boss@example.com", victim, "client-1")
	if rd.Key() != "boss@example.com" {
		t.Errorf("mailReaderFor gave a non-admin the key %q after they asked for %s",
			rd.Key(), victim)
	}
}

// The same finding, one layer up: the confinement was never checked from the
// client's side, so the sidebar was never given a client branch. A client is a
// console session (not a mail-only one), so it fell through to the operator
// sidebar, where every gated item closed against its floor access level and the
// two ungated product links pointed at pages the confinement refuses.
//
// A customer therefore had no link to their own site anywhere in the console.
// /os/mysite — the one page ADR-0152 exists to deliver — was reachable only by
// typing the URL, or by clicking something forbidden and being bounced there.
func TestAClientsSidebarLeadsToTheirOwnPagesAndNowhereElse(t *testing.T) {
	nav := osSidebarNav("mysite", &osSettings{UserRole: users.RoleClient, AccessLevel: accessMailOnly})

	for _, want := range []string{"/os/mysite", "/os/vayumail/inbox", "/os/profile"} {
		if !strings.Contains(nav, `href="`+want+`"`) {
			t.Errorf("a client's sidebar has no link to %q.\n\n"+
				"That is a page they were sold. A console whose only route to it is "+
				"typing the URL has not delivered it.\n%s", want, nav)
		}
	}
	// Derived, so a link added to this branch later cannot quietly point somewhere
	// the client is refused: every href it emits must be reachable.
	for _, m := range regexp.MustCompile(`href="(/os/[^"?#]*)`).FindAllStringSubmatch(nav, -1) {
		if !clientPathAllowed(m[1]) {
			t.Errorf("a client's sidebar offers %q, which the confinement refuses — "+
				"clicking it bounces them with no explanation", m[1])
		}
	}
}

// The last piece of the same finding, carried forward from the release that
// named it: the VayuMail tab strip offered a client an "Overview" tab pointing
// at /os/vayumail — the mailbox-administration dashboard the confinement
// refuses. It was the FIRST tab on every mailbox page they were given, so a
// paying customer's first click threw them back to /os/mysite with no
// explanation.
//
// The strip now asks clientPathAllowed rather than carrying a second copy of the
// rule, so it cannot drift from the confinement it reflects.
func TestTheMailTabStripOffersAClientNothingTheyCannotOpen(t *testing.T) {
	a := resetSessionApp(t)

	req := withUser(httptest.NewRequest(http.MethodGet, "/os/vayumail/inbox", nil), boundClient())
	tabs := a.vayuosNav(req, "mailbox")

	got := regexp.MustCompile(`href="(/os/[^"?#]*)`).FindAllStringSubmatch(tabs, -1)
	if len(got) == 0 {
		t.Fatal("a client's mail tab strip is empty — they can no longer move between " +
			"their own inbox, composer and connect page")
	}
	for _, m := range got {
		if !clientPathAllowed(m[1]) {
			t.Errorf("the mail tab strip offers a client %q, which the confinement refuses — "+
				"clicking it bounces them to /os/mysite with no explanation", m[1])
		}
	}
	// It must still be a usable strip, not an empty one: the pages they DO hold.
	for _, want := range []string{"/os/vayumail/inbox", "/os/vayumail/compose", "/os/vayumail/connect"} {
		if !strings.Contains(tabs, `href="`+want+`"`) {
			t.Errorf("the strip no longer offers %q, which a client is sold", want)
		}
	}
}

// THE CONTROL, and the reason the filter is keyed on the client role rather than
// applied to everyone: a mailbox-only holder is NOT a client and legitimately
// reaches the whole non-admin strip, Overview included. Filtering unconditionally
// would take that away to fix somebody else's problem.
func TestTheMailTabStripIsUnchangedForAMailboxHolder(t *testing.T) {
	a := resetSessionApp(t)

	holder := &users.User{ID: "m1", Email: "boss@example.com", Role: users.RoleAuthor, MailAddress: "boss@example.com"}
	req := withUser(httptest.NewRequest(http.MethodGet, "/os/vayumail/inbox", nil), holder)
	tabs := a.vayuosNav(req, "mailbox")

	for _, want := range []string{"/os/vayumail", "/os/vayumail/compose", "/os/vayumail/inbox",
		"/os/vayumail/connect", "/os/vayumail/sent"} {
		if !strings.Contains(tabs, `href="`+want+`"`) {
			t.Errorf("a mailbox holder lost the %q tab. The confinement filter is meant for "+
				"agency clients only; applying it to everyone removes a working page from "+
				"people who were never confined.", want)
		}
	}
	// And the admin-only tabs stay hidden from them, as before.
	for _, hidden := range []string{"/os/vayumail/accounts", "/os/vayumail/dns", "/os/vayumail/pgp"} {
		if strings.Contains(tabs, `href="`+hidden+`"`) {
			t.Errorf("a non-admin was offered the %q tab", hidden)
		}
	}
}

// The additions sit UNDER /os/vayumail/accounts. Exact-or-child matches
// downward only, so granting the app-password and avatar leaves must not grant
// the parent — where create, delete, update and TOTP live.
func TestGrantingALeafDoesNotGrantTheAccountsBranch(t *testing.T) {
	for _, p := range []string{
		"/os/vayumail/accounts",
		"/os/vayumail/accounts/create",
		"/os/vayumail/accounts/delete",
		"/os/vayumail/accounts/update",
		"/os/vayumail/accounts/totp",
		"/os/vayumail/accounts/action",
		"/os/vayumail/accounts/fragment",
		"/os/vayumail/accounts/pubkey",
	} {
		if clientPathAllowed(p) {
			t.Errorf("%q is reachable by a client. Adding a leaf under "+
				"/os/vayumail/accounts must not carry its parent — that branch creates, "+
				"deletes and re-passwords every mailbox on the install", p)
		}
	}
}
