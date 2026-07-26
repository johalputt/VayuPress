// SPDX-License-Identifier: Apache-2.0

package main

import (
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/render"
)

// The recovery flow shipped complete and unreachable: /mail/recover existed, its
// pages cross-linked to each other, and not one sign-in page pointed at it. A
// locked-out holder had no way to discover the feature built for exactly them.
//
// These tests assert the link direction that was missing. They render the pages
// where that is cheap, and read the source where the page needs a live App.

const recoverHref = "/mail/recover"

// TestOSLoginPageLinksToRecovery covers the console sign-in page. A mailbox-role
// holder lands here, because handleOSLoginSubmit falls back to authMailAccount
// when the console user store rejects them, and every unauthenticated
// /os/vayumail/* request is bounced here.
func TestOSLoginPageLinksToRecovery(t *testing.T) {
	page := osLoginPage("", "", "")
	if !strings.Contains(page, `href="`+recoverHref+`"`) {
		t.Errorf("the /os sign-in page does not link to %s — a locked-out mailbox holder cannot find recovery", recoverHref)
	}
	// The link needs a rule to render as a link; .login-footer has no anchor
	// styling, which is why it got its own class rather than being tacked on.
	css := repoFile(t, "static/css/admin-os.css")
	if !strings.Contains(css, ".login-recover a") {
		t.Error("static/css/admin-os.css has no .login-recover a rule; the recovery link renders unstyled")
	}
}

// TestPortalWidgetLinksToRecovery covers the floating VayuPortal widget, whose
// VayuMail view is a bare address+password form.
func TestPortalWidgetLinksToRecovery(t *testing.T) {
	if !strings.Contains(render.PortalJS, recoverHref) {
		t.Errorf("the portal widget's VayuMail sign-in view does not link to %s", recoverHref)
	}
}

// TestMemberSigninPageLinksToRecovery covers the member sign-in page. It is
// checked as source text rather than rendered output because the page needs a
// live App with VayuMail enabled; the assertion is still specific enough to fail
// if the link is removed.
func TestMemberSigninPageLinksToRecovery(t *testing.T) {
	src := repoFile(t, "cmd/vayupress/handlers_member_portal.go")
	if !strings.Contains(src, `href="`+recoverHref+`"`) {
		t.Errorf("the member sign-in page does not link to %s — this is the page where a mailbox password is typed", recoverHref)
	}
}

// TestPortalInboxLinkMatchesTheRegisteredRoute is the bug this sweep turned up:
// the widget's "Open VayuMail" button pointed at /os/vayuos/mail/inbox, which
// has never been a route. Every other reference in the tree uses
// /os/vayumail/inbox.
func TestPortalInboxLinkMatchesTheRegisteredRoute(t *testing.T) {
	if strings.Contains(render.PortalJS, "/os/vayuos/mail/inbox") {
		t.Error(`portal widget links to /os/vayuos/mail/inbox, which is not a registered route; the route is /os/vayumail/inbox`)
	}
	if !strings.Contains(render.PortalJS, "/os/vayumail/inbox") {
		t.Error("portal widget no longer links to the mailbox at all")
	}
}

// TestRecoveryIsDocumented guards the docs, because a feature nobody can find
// out about is only marginally better than one nobody can reach.
func TestRecoveryIsDocumented(t *testing.T) {
	doc := repoFile(t, "docs/MAIL-RECOVERY.md")
	// Each of the five shipped paths must be named, or the doc silently drifts
	// out of date as paths are added.
	for _, want := range []string{
		recoverHref,
		"/mail/recover/code",
		"/mail/recover/ask",
		"vayupress mail passwd",
		"vayupress mail unrecoverable",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("docs/MAIL-RECOVERY.md does not mention %q", want)
		}
	}
	readme := repoFile(t, "README.md")
	if !strings.Contains(readme, "docs/MAIL-RECOVERY.md") {
		t.Error("README.md does not link to docs/MAIL-RECOVERY.md")
	}
}
