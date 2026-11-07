// SPDX-License-Identifier: Apache-2.0

package main

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/auth"
	vmail "github.com/johalputt/vayupress/internal/vayuos/mail"
)

// exportApp is a mail install with dana's mailbox holding one message, and an
// approved device credential for dana, as the app signs in.
func exportApp(t *testing.T) (*App, string) {
	t.Helper()
	a := appWithMailAccounts(t)
	md := vmail.NewMaildir(a.vayuMail.Config().StorageDir + "/maildir")
	if err := md.CreateAll("example.com", "dana"); err != nil {
		t.Fatal(err)
	}
	if _, err := md.Deliver("example.com", "dana", []byte("From: sam@example.org\r\nSubject: quarterly figures\r\n\r\nthe figures")); err != nil {
		t.Fatal(err)
	}
	const secret = "devicesecret1234"
	hash, err := auth.HashSecretArgon2id(secret)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.vayuMail.Accounts().CreateAppPassword(context.Background(), "dana@example.com", "Phone", hash); err != nil {
		t.Fatal(err)
	}
	return a, secret
}

func postExport(a *App, email, password string) *httptest.ResponseRecorder {
	payload, _ := json.Marshal(map[string]string{"email": email, "password": password})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/members/vayumail-export", strings.NewReader(string(payload)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	a.handleMemberVayuMailExport(rec, req)
	return rec
}

// TestTheAppDownloadsTheWholeMailbox — the app's export is the console's
// Download all mail: a zip of mbox files holding the holder's messages.
func TestTheAppDownloadsTheWholeMailbox(t *testing.T) {
	a, secret := exportApp(t)
	rec := postExport(a, "dana@example.com", secret)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/zip" {
		t.Errorf("Content-Type %q, want application/zip", ct)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control %q, want no-store", cc)
	}
	z, err := zip.NewReader(bytes.NewReader(rec.Body.Bytes()), int64(rec.Body.Len()))
	if err != nil {
		t.Fatalf("the export is not a zip: %v", err)
	}
	var all strings.Builder
	for _, f := range z.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(rc)
		rc.Close()
		all.Write(b)
	}
	if !strings.Contains(all.String(), "Subject: quarterly figures") {
		t.Errorf("the export does not hold the mailbox's message; files: %d", len(z.File))
	}
}

// TestAWrongCredentialExportsNothing — no mail without the app's credential,
// and the refusal carries none.
func TestAWrongCredentialExportsNothing(t *testing.T) {
	a, _ := exportApp(t)
	rec := postExport(a, "dana@example.com", "not-the-secret")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status %d, want 401", rec.Code)
	}
	if bytes.Contains(rec.Body.Bytes(), []byte("PK\x03\x04")) {
		t.Error("a refused export carried a zip of the mailbox")
	}
}

// TestOneExportPerMailboxAtATime — a second export of a mailbox while one is
// streaming is refused, so whole-mailbox reads cannot be stacked.
func TestOneExportPerMailboxAtATime(t *testing.T) {
	a, secret := exportApp(t)
	mailExportsRunning.Store("dana@example.com", struct{}{})
	t.Cleanup(func() { mailExportsRunning.Delete("dana@example.com") })
	rec := postExport(a, "dana@example.com", secret)
	if rec.Code != http.StatusTooManyRequests || !strings.Contains(rec.Body.String(), "already running") {
		t.Fatalf("a second export got %d %s, want 429, already running", rec.Code, rec.Body.String())
	}
	mailExportsRunning.Delete("dana@example.com")
	if rec := postExport(a, "dana@example.com", secret); rec.Code != http.StatusOK {
		t.Errorf("after the first finished, an export got %d, want 200", rec.Code)
	}
}

// TestMailKeyOfIsMailAddrOfsInverse — the export reads the mailbox the IMAP
// server would open for the same address.
func TestMailKeyOfIsMailAddrOfsInverse(t *testing.T) {
	for addr, want := range map[string]string{
		"dana@example.com":   "dana",
		"dana@Example.COM":   "dana",
		"dana@other.example": "dana@other.example",
	} {
		if got := mailKeyOf(addr, "example.com"); got != want {
			t.Errorf("mailKeyOf(%q) = %q, want %q", addr, got, want)
		}
	}
	if got := mailAddrOf(mailKeyOf("dana@example.com", "example.com"), "example.com"); got != "dana@example.com" {
		t.Errorf("round trip gave %q", got)
	}
}
