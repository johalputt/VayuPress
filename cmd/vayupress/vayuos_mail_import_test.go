// SPDX-License-Identifier: Apache-2.0

package main

import (
	"archive/zip"
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/users"
	vmail "github.com/johalputt/vayupress/internal/vayuos/mail"
)

func operatorUser() *users.User {
	return &users.User{ID: "u-op", Email: "op@example.com", Role: users.RoleAdmin}
}

func download(a *App, u *users.User, user string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	a.handleVayuOSMailDownload(rec, withUser(httptest.NewRequest(http.MethodGet, "/os/vayumail/download?user="+url.QueryEscape(user), nil), u))
	return rec
}

func ledgerActions(t *testing.T, a *App) []string {
	t.Helper()
	ents, err := a.vayuMail.Ledger(context.Background(), "dana@example.com", 50)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range ents {
		out = append(out, e.Action)
	}
	return out
}

// The holder downloads their own mailbox as a zip of mbox files, and that is
// not an access anyone is told of.
func TestAHolderDownloadsTheirMailbox(t *testing.T) {
	a := scheduledApp(t)
	rec := download(a, danaHolder(), "")
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "application/zip" || !strings.Contains(rec.Header().Get("Content-Disposition"), "dana-at-example.com-mail-") {
		t.Fatalf("download: %d %q %q", rec.Code, rec.Header().Get("Content-Type"), rec.Header().Get("Content-Disposition"))
	}
	z, err := zip.NewReader(bytes.NewReader(rec.Body.Bytes()), int64(rec.Body.Len()))
	if err != nil || len(z.File) != 1 || z.File[0].Name != "Inbox.mbox" {
		t.Fatalf("the zip holds %v (%v), want Inbox.mbox", z, err)
	}
	if acts := ledgerActions(t, a); len(acts) != 0 {
		t.Fatalf("the holder's own download was recorded: %v", acts)
	}
}

// An operator's download is recorded where the holder sees it, and a
// handed-over mailbox is not theirs to download at all.
func TestAnOperatorsDownloadIsRecordedAndHandoverRefusesIt(t *testing.T) {
	a := scheduledApp(t)
	if rec := download(a, operatorUser(), "dana@example.com"); rec.Code != http.StatusOK {
		t.Fatalf("operator download: %d", rec.Code)
	}
	if acts := ledgerActions(t, a); len(acts) != 1 || acts[0] != "download" {
		t.Fatalf("the operator's download recorded as %v", acts)
	}
	if err := a.vayuMail.HandOver(context.Background(), "dana@example.com", "op", ""); err != nil {
		t.Fatal(err)
	}
	if rec := download(a, operatorUser(), "dana@example.com"); rec.Code != http.StatusForbidden || rec.Header().Get("Content-Type") == "application/zip" {
		t.Fatalf("a handed-over mailbox downloaded by an operator: %d", rec.Code)
	}
}

func importAction(a *App, u *users.User, vals url.Values) *httptest.ResponseRecorder {
	req := withUser(httptest.NewRequest(http.MethodPost, "/os/vayumail/import/action", strings.NewReader(vals.Encode())), u)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	a.handleVayuOSMailImportAction(rec, req)
	return rec
}

// A refused start says why at once, and starts nothing.
func TestBringMailInRefusesAtOnce(t *testing.T) {
	a := scheduledApp(t)
	rec := importAction(a, danaHolder(), url.Values{"action": {"start"}, "host": {"imap.example.net"}, "port": {"25"}, "username": {"d"}, "password": {"p"}})
	if !strings.Contains(rec.Body.String(), "Not started: the port must be 993") {
		t.Fatalf("port 25:\n%s", rec.Body.String())
	}
	rd := a.mailReader(withUser(httptest.NewRequest(http.MethodGet, "/", nil), danaHolder()), "")
	if st, _ := a.vayuMail.ImportStatusFor(rd); st != nil {
		t.Fatalf("a refused start left an import: %+v", st)
	}
}

// An operator's import into someone's mailbox is recorded where they see it.
func TestAnOperatorsImportIsRecorded(t *testing.T) {
	a := scheduledApp(t)
	importAction(a, operatorUser(), url.Values{"action": {"start"}, "user": {"dana@example.com"}, "host": {"imap.example.net"}, "port": {"993"}, "username": {"d"}, "password": {"p"}})
	rd := vmail.ReadAsOwner("dana@example.com")
	t.Cleanup(func() { _ = a.vayuMail.StopImport(rd) })
	if acts := ledgerActions(t, a); len(acts) != 1 || acts[0] != "import" {
		t.Fatalf("the operator's import recorded as %v", acts)
	}
}

// The page offers the form to a holder and not to a read-only mailbox, and
// the sidebar offers Bring mail in on the same terms.
func TestBringMailInIsOfferedOnlyWhereItCanRun(t *testing.T) {
	for role, readOnly := range map[string]bool{vmail.RoleMailbox: false, vmail.RoleReviewer: true} {
		a, holder, _ := reviewerApp(t, role)
		rd := a.mailReader(withUser(httptest.NewRequest(http.MethodGet, "/", nil), holder), "")
		body := a.mailImportBody(context.Background(), rd, "")
		if strings.Contains(body, `name="password"`) == readOnly {
			t.Errorf("%s: the form is offered = %v", role, !readOnly)
		}
		side := saMailSide(&osMailSide{User: "dana", Address: "dana@example.com", Writable: !a.vayuMail.ReaderReadOnly(rd)}, "")
		if strings.Contains(side, "Bring mail in") == readOnly {
			t.Errorf("%s: the sidebar offers Bring mail in = %v", role, !readOnly)
		}
		if !strings.Contains(side, "/os/vayumail/download?user=dana") {
			t.Errorf("%s: the sidebar has no Download all mail", role)
		}
	}
}
