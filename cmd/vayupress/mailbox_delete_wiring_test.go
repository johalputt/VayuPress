// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/auth"
	"github.com/johalputt/vayupress/internal/users"
	vmail "github.com/johalputt/vayupress/internal/vayuos/mail"

	_ "github.com/mattn/go-sqlite3"
)

// The retirement itself is proven in internal/vayuos/mail. This file proves the
// CONSOLE REACHES IT, which is a separate question and the one this audit has
// got wrong four times: a correct guard nobody routes through is not a control.
//
// Engine.DeleteMailbox is the root; AccountStore.Delete still exists beside it
// and still leaves every message on disk. A handler calling the store directly
// compiles, passes its own tests, deletes the account exactly as asked — and
// leaves the next holder of the address reading the last one's mail.
//
// So this drives the real handler and then looks at the filesystem, rather than
// reading the handler for the name of the function it calls. A test that greps
// source fails an honest rename and passes a regression that moves the call into
// a comment.

// deleteWiringApp builds an App around a real mail engine whose Maildir path the
// test keeps, so the assertion can be made against the disk rather than a mock.
func deleteWiringApp(t *testing.T) (*App, string) {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })

	storage := t.TempDir()
	cfg := vmail.DefaultConfig()
	cfg.Enabled = true
	cfg.Domain = "example.com"
	cfg.Hostname = "mail.example.com"
	cfg.StorageDir = storage
	cfg.InboundEnabled = false
	e := vmail.NewEngine(&cfg, nil, db)
	if err := e.Start(context.Background()); err != nil {
		t.Fatalf("start engine: %v", err)
	}
	t.Cleanup(func() { _ = e.Stop(context.Background()) })
	applyMailHandoverSchema(t, db)
	return &App{vayuMail: e}, filepath.Join(storage, "maildir")
}

// countFiles returns how many regular files sit under dir (0 for a missing one).
func countFiles(t *testing.T, dir string) int {
	t.Helper()
	n := 0
	_ = filepath.Walk(dir, func(_ string, info os.FileInfo, err error) error {
		if err == nil && info != nil && !info.IsDir() {
			n++
		}
		return nil
	})
	return n
}

func TestTheConsoleDeleteTakesTheMailOutOfTheDeliveryTree(t *testing.T) {
	a, mailbase := deleteWiringApp(t)
	ctx := context.Background()
	const addr = "info@example.com"

	hash, err := auth.HashSecretArgon2id("leaving-soon")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if err := a.vayuMail.Accounts().Create(ctx, addr, hash, "Leaver", "mailbox"); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := a.vayuMail.DeliverInbound("lawyer@firm.example", addr,
		[]byte("From: lawyer@firm.example\r\nSubject: private\r\n\r\nthe settlement figure")); err != nil {
		t.Fatalf("deliver: %v", err)
	}
	live := filepath.Join(mailbase, "example.com", "info")
	if countFiles(t, live) == 0 {
		t.Fatal("fixture wrong: nothing was delivered, so this test cannot detect inheritance")
	}

	admin := &users.User{ID: "admin1", Email: "root@example.com", Role: users.RoleAdmin}
	req := withUser(httptest.NewRequest(http.MethodPost, "/os/vayumail/accounts/delete",
		strings.NewReader(`{"email":"`+addr+`"}`)), admin)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	a.handleVayuOSAccountDelete(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("the console delete failed: %d %s", rec.Code, rec.Body.String())
	}
	if n := countFiles(t, live); n != 0 {
		t.Errorf("%d message file(s) remain at the deleted mailbox's live Maildir path.\n\n"+
			"The account is gone from the database, so the console reports success and "+
			"every credential really is revoked — but the next person given this address "+
			"is handed the previous holder's mail by IMAP on their first sync. The handler "+
			"is calling the store's Delete instead of the engine's DeleteMailbox.", n)
	}
	// Kept, not destroyed: the operator was promised the messages survive.
	if n := countFiles(t, mailbase+"-retired"); n == 0 {
		t.Error("nothing was set aside. Retirement exists so a mailbox deleted by mistake, " +
			"or held for retention, is still recoverable — erasing it silently is the " +
			"outcome this fix was chosen to avoid.")
	}
	// And the response says which of the two happened, so the operator is not
	// left guessing whether their client's mail still exists. Decoded rather than
	// substring-matched: the encoder's whitespace is not the property under test.
	var out struct {
		Deleted  bool   `json:"deleted"`
		Retained bool   `json:"retained"`
		Detail   string `json:"detail"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode the delete response: %v (%s)", err, rec.Body.String())
	}
	if !out.Deleted || !out.Retained || out.Detail == "" {
		t.Errorf("the response does not tell the operator the mail was kept: %+v.\n\n"+
			"They have just deleted a client's mailbox. Whether the messages still "+
			"exist is the only question they have.", out)
	}
	// The panel must not name a path on the box — infrastructure detail does not
	// belong in product copy.
	if strings.Contains(out.Detail, "/") {
		t.Errorf("the operator-facing message carries a server path: %q", out.Detail)
	}
}

// The account really is gone — retirement must not become a soft delete that
// leaves the credential working.
func TestTheConsoleDeleteStillRevokesTheAccount(t *testing.T) {
	a, _ := deleteWiringApp(t)
	ctx := context.Background()
	const addr = "gone@example.com"

	hash, _ := auth.HashSecretArgon2id("still-works")
	if err := a.vayuMail.Accounts().Create(ctx, addr, hash, "Gone", "mailbox"); err != nil {
		t.Fatalf("create: %v", err)
	}
	admin := &users.User{ID: "admin1", Email: "root@example.com", Role: users.RoleAdmin}
	req := withUser(httptest.NewRequest(http.MethodPost, "/os/vayumail/accounts/delete",
		strings.NewReader(`{"email":"`+addr+`"}`)), admin)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	a.handleVayuOSAccountDelete(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("delete failed: %d %s", rec.Code, rec.Body.String())
	}
	if a.vayuMail.Accounts().HashFor(ctx, addr) != "" {
		t.Error("the deleted mailbox still authenticates — moving the mail aside must not " +
			"have replaced revoking the credential")
	}
}
