// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/ProtonMail/go-crypto/openpgp"

	"github.com/johalputt/vayupress/internal/auth"
	"github.com/johalputt/vayupress/internal/users"
	vpgp "github.com/johalputt/vayupress/internal/vayuos/pgp"
)

const danaPW = "main-mailbox-pass"

var recoveryCodeRe = regexp.MustCompile(`<code>([A-Z2-7-]+)</code>`)

// sealedMailApp is privateMailboxApp with VayuPGP running, so making the
// mailbox private seals it.
func sealedMailApp(t *testing.T) (a *App, admin, dana *users.User) {
	t.Helper()
	cfg := vpgp.DefaultConfig()
	cfg.StorageDir = t.TempDir()
	cfg.MasterSecret = []byte("test-master-secret")
	keys := vpgp.NewEngine(&cfg)
	if err := keys.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	a = appWithMailKeys(t, keys)
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	a.userStore = users.New(db)
	admin = &users.User{ID: "admin1", Email: "boss@example.com", Role: users.RoleAdmin}
	dana = &users.User{ID: "vmail:dana@example.com", Email: "dana@example.com", Role: users.RoleAuthor, MailAddress: "dana@example.com"}
	return a, admin, dana
}

func mailFiles(t *testing.T, a *App) []string {
	t.Helper()
	var out []string
	_ = filepath.WalkDir(a.vayuMail.Config().StorageDir+"/maildir", func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			out = append(out, p)
		}
		return nil
	})
	return out
}

func clearOnDisk(t *testing.T, a *App, word string) bool {
	t.Helper()
	for _, p := range mailFiles(t, a) {
		if b, _ := os.ReadFile(p); bytes.Contains(b, []byte(word)) {
			return true
		}
	}
	return false
}

// makeDanaPrivate makes dana's mailbox private and sealed, and returns the
// recovery code the page showed.
func makeDanaPrivate(t *testing.T, a *App, dana *users.User) string {
	t.Helper()
	setRecovery(t, a, "dana@example.com")
	body := postAppPwForm(a.handleVayuOSMakePrivate, "/", url.Values{"current": {danaPW}, "confirm": {"dana@example.com"}}, dana).Body.String()
	m := recoveryCodeRe.FindStringSubmatch(body)
	if m == nil || !strings.Contains(body, "Private and sealed") {
		t.Fatalf("making the mailbox private showed no recovery code: %s", body)
	}
	// What sealing does not cover is said, not only left out.
	if !strings.Contains(body, "Your calendar and contacts are not sealed") {
		t.Fatalf("the sealed card does not say what stays readable: %s", body)
	}
	return m[1]
}

// TestMakingAMailboxPrivateSealsItsMail — the mail already there and the mail
// that comes after are both unreadable on the disk.
func TestMakingAMailboxPrivateSealsItsMail(t *testing.T) {
	a, _, dana := sealedMailApp(t)
	if _, err := a.vayuMail.DeliverInbound("sam@example.org", "dana@example.com", []byte("From: sam@example.org\r\nTo: dana@example.com\r\nSubject: before\r\n\r\nstored before private\r\n")); err != nil {
		t.Fatal(err)
	}
	makeDanaPrivate(t, a, dana)
	if !a.vayuMail.IsHandedOver("dana@example.com") || !a.vayuPGP.HolderKeeps("dana@example.com") {
		t.Fatal("the mailbox is not private and sealed")
	}
	deadline := time.Now().Add(10 * time.Second)
	for clearOnDisk(t, a, "stored before private") && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if clearOnDisk(t, a, "stored before private") {
		t.Error("the mail stored before is still readable on the disk")
	}
	if _, err := a.vayuMail.DeliverInbound("sam@example.org", "dana@example.com", []byte("From: sam@example.org\r\nTo: dana@example.com\r\nSubject: after\r\n\r\narrived after private\r\n")); err != nil {
		t.Fatal(err)
	}
	if clearOnDisk(t, a, "arrived after private") {
		t.Error("mail that arrived after is readable on the disk")
	}
}

// TestSealedMailOpensWithThePasswordOnly — typed into the inbox's notice, the
// password holds it open; a wrong one does not; Close shuts it.
func TestSealedMailOpensWithThePasswordOnly(t *testing.T) {
	a, _, dana := sealedMailApp(t)
	makeDanaPrivate(t, a, dana)
	a.vayuPGP.CloseHeld("dana@example.com")
	if !strings.Contains(a.sealedNotice("dana@example.com", ""), "Your mail is sealed") {
		t.Fatal("no sealed notice while the key is shut")
	}
	if body := postAppPwForm(a.handleVayuOSMailUnlock, "/", url.Values{"current": {"not it"}}, dana).Body.String(); !strings.Contains(body, "not your mailbox password") || a.vayuPGP.HeldOpen("dana@example.com") {
		t.Fatalf("a wrong password: %s", body)
	}
	rec := postAppPwForm(a.handleVayuOSMailUnlock, "/", url.Values{"current": {danaPW}}, dana)
	if rec.Header().Get("HX-Redirect") != "/os/vayumail/inbox" || !a.vayuPGP.HeldOpen("dana@example.com") {
		t.Fatalf("the password did not open the mail: %d %s", rec.Code, rec.Body.String())
	}
	if a.sealedNotice("dana@example.com", "") != "" {
		t.Error("the notice still shows with the mail open")
	}
	postAppPwForm(a.handleVayuOSMailLock, "/", nil, dana)
	if a.vayuPGP.HeldOpen("dana@example.com") {
		t.Error("Close my mail now left it open")
	}
}

// TestASharedMailboxIsNotOneHoldersToSeal — sales@ held by two people is
// refused, whoever asks.
func TestASharedMailboxIsNotOneHoldersToSeal(t *testing.T) {
	a, _, dana := sealedMailApp(t)
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(`CREATE TABLE users(id TEXT PRIMARY KEY,email TEXT NOT NULL UNIQUE,name TEXT NOT NULL DEFAULT '',password_hash TEXT NOT NULL,role TEXT NOT NULL DEFAULT 'author',created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,last_login DATETIME,avatar_url TEXT NOT NULL DEFAULT '',bio TEXT NOT NULL DEFAULT '',socials TEXT NOT NULL DEFAULT '{}',mail_address TEXT NOT NULL DEFAULT '',must_change_password INTEGER NOT NULL DEFAULT 0,username TEXT NOT NULL DEFAULT '',client_domain_id TEXT NOT NULL DEFAULT '');
		CREATE VIEW user_profiles AS SELECT id, email, name, role, avatar_url, bio, socials, mail_address, created_at, last_login, must_change_password AS must_change_signin, username, client_domain_id FROM users;
		INSERT INTO users(id,email,password_hash,mail_address) VALUES('u1','dana@home.test','x','dana@example.com'),('u2','sam@home.test','x','dana@example.com');`); err != nil {
		t.Fatal(err)
	}
	a.userStore = users.New(db)
	setRecovery(t, a, "dana@example.com")
	body := postAppPwForm(a.handleVayuOSMakePrivate, "/", url.Values{"current": {danaPW}, "confirm": {"dana@example.com"}}, dana).Body.String()
	if !strings.Contains(body, "shared by 2 people") || a.vayuMail.IsHandedOver("dana@example.com") || a.vayuPGP.HolderKeeps("dana@example.com") {
		t.Fatalf("one holder of a shared mailbox made it private: %s", body)
	}
}

// TestADeletedPrivateMailboxsAddressCanBeGivenAgain — deleting it takes its
// key with its mail, so the next holder's mail is not sealed to a key only
// the last holder's password opens.
func TestADeletedPrivateMailboxsAddressCanBeGivenAgain(t *testing.T) {
	a, _, dana := sealedMailApp(t)
	makeDanaPrivate(t, a, dana)
	if _, err := a.vayuMail.DeleteMailbox(context.Background(), "dana@example.com"); err != nil {
		t.Fatal(err)
	}
	if a.vayuPGP.HolderKeeps("dana@example.com") {
		t.Fatal("the deleted mailbox's locked key is still the address's key")
	}
}

// TestTheInboxSaysTheMailIsSealed — the holder's own inbox carries the notice
// and its form while the key is shut.
func TestTheInboxSaysTheMailIsSealed(t *testing.T) {
	a, _, dana := sealedMailApp(t)
	makeDanaPrivate(t, a, dana)
	a.vayuPGP.CloseHeld("dana@example.com")
	req := withUser(httptest.NewRequest(http.MethodGet, "/os/vayumail/inbox", nil), dana)
	rec := httptest.NewRecorder()
	a.handleVayuOSInbox(rec, req)
	if !strings.Contains(rec.Body.String(), "Your mail is sealed") || !strings.Contains(rec.Body.String(), `hx-post="/os/vayumail/unlock"`) {
		t.Fatalf("the inbox of shut sealed mail: %d, no notice", rec.Code)
	}
}

// TestAProtocolSignInWithThePasswordOpensSealedMail — an IMAP or POP3 sign-in
// with the mailbox password opens it; an app password does not.
func TestAProtocolSignInWithThePasswordOpensSealedMail(t *testing.T) {
	a, _, dana := sealedMailApp(t)
	makeDanaPrivate(t, a, dana)
	ctx := context.Background()
	if err := a.vayuMail.Accounts().SetRequireDeviceApproval(ctx, "dana@example.com", false); err != nil {
		t.Fatal(err)
	}
	hash, _ := auth.HashSecretArgon2id("devicesecret1234")
	if _, err := a.vayuMail.Accounts().CreateAppPassword(ctx, "dana@example.com", "Phone", hash); err != nil {
		t.Fatal(err)
	}
	b := &vayuMailBridge{app: a}
	a.vayuPGP.CloseHeld("dana@example.com")
	if !b.verifyCredential(ctx, "dana@example.com", "devicesecret1234") || a.vayuPGP.HeldOpen("dana@example.com") {
		t.Fatal("an app password opened sealed mail, or was refused")
	}
	if !b.verifyCredential(ctx, "dana@example.com", danaPW) || !a.vayuPGP.HeldOpen("dana@example.com") {
		t.Fatal("a sign-in with the mailbox password did not open the mail")
	}
	// And the console's sign-in for a mail-only account.
	a.vayuPGP.CloseHeld("dana@example.com")
	if _, ok, _ := a.authMailAccount(ctx, "dana@example.com", danaPW, ""); !ok || !a.vayuPGP.HeldOpen("dana@example.com") {
		t.Fatal("a console sign-in with the mailbox password did not open the mail")
	}
}

// TestAPasswordChangeTakesTheKeyAlong — and a refused change leaves it where
// it was.
func TestAPasswordChangeTakesTheKeyAlong(t *testing.T) {
	a, _, dana := sealedMailApp(t)
	makeDanaPrivate(t, a, dana)
	change := func(cur, next string) string {
		return postAppPwForm(a.handleVayuOSMyPassword, "/", url.Values{"current": {cur}, "new": {next}, "again": {next}}, dana).Body.String()
	}
	if body := change(danaPW, "short"); !strings.Contains(body, "Not changed") {
		t.Fatalf("a too-short password was taken: %s", body)
	}
	a.vayuPGP.CloseHeld("dana@example.com")
	if err := a.vayuPGP.HoldOpen("dana@example.com", danaPW); err != nil {
		t.Fatalf("after a refused change the key no longer opens with the password: %v", err)
	}
	if body := change(danaPW, "a much longer new password"); !strings.Contains(body, "Password changed") {
		t.Fatalf("the change: %s", body)
	}
	if a.vayuPGP.HeldOpen("dana@example.com") {
		t.Error("the change signs everything out, but left the mail open")
	}
	if err := a.vayuPGP.HoldOpen("dana@example.com", danaPW); err == nil {
		t.Error("the old password still opens the key")
	}
	if err := a.vayuPGP.HoldOpen("dana@example.com", "a much longer new password"); err != nil {
		t.Errorf("the new password does not open the key: %v", err)
	}
}

// TestAfterAResetTheRecoveryCodeOpensTheMail — a password set some other way
// is told so, and the code moves the key under it and is replaced.
func TestAfterAResetTheRecoveryCodeOpensTheMail(t *testing.T) {
	a, _, dana := sealedMailApp(t)
	code := makeDanaPrivate(t, a, dana)
	hash, _ := auth.HashSecretArgon2id("a password set by recovery")
	if err := a.vayuMail.Accounts().SetPasswordHash(context.Background(), "dana@example.com", hash); err != nil {
		t.Fatal(err)
	}
	a.vayuPGP.CloseHeld("dana@example.com")
	if body := postAppPwForm(a.handleVayuOSMailUnlock, "/", url.Values{"current": {"a password set by recovery"}}, dana).Body.String(); !strings.Contains(body, "earlier one") {
		t.Fatalf("the new password against the old lock: %s", body)
	}
	recover := func(c string) string {
		return postAppPwForm(a.handleVayuOSMailRecover, "/", url.Values{"code": {c}, "current": {"a password set by recovery"}}, dana).Body.String()
	}
	if body := recover("AAAA-BBBB"); !strings.Contains(body, "does not open") {
		t.Fatalf("a wrong code: %s", body)
	}
	body := recover(code)
	m := recoveryCodeRe.FindStringSubmatch(body)
	if m == nil || m[1] == code || !a.vayuPGP.HeldOpen("dana@example.com") {
		t.Fatalf("the code did not open the mail with a new code: %s", body)
	}
	if body := recover(code); !strings.Contains(body, "does not open") {
		t.Fatalf("the used code worked twice: %s", body)
	}
}

// TestTheAppIsHandedTheSameKeyLocked — the phone gets the holder's key,
// locked with the password, never a new one made because it would not open.
func TestTheAppIsHandedTheSameKeyLocked(t *testing.T) {
	a, _, dana := sealedMailApp(t)
	makeDanaPrivate(t, a, dana)
	before, _ := a.vayuPGP.GetPublicKey("dana@example.com")
	hash, _ := auth.HashSecretArgon2id("devicesecret1234")
	if _, err := a.vayuMail.Accounts().CreateAppPassword(context.Background(), "dana@example.com", "Phone", hash); err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(map[string]string{"email": "dana@example.com", "password": "devicesecret1234"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/members/vayumail-privkey", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	a.handleMemberVayuMailPrivKey(rec, req)
	var got struct {
		Armored string `json:"armored_private_key"`
	}
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &got) != nil {
		t.Fatalf("the key endpoint: %d %s", rec.Code, rec.Body.String())
	}
	ring, err := openpgp.ReadArmoredKeyRing(strings.NewReader(got.Armored))
	if err != nil || len(ring) == 0 {
		t.Fatal(err)
	}
	if !ring[0].PrivateKey.Encrypted {
		t.Error("the app was handed the key unlocked")
	}
	if fp := strings.ToUpper(strings.TrimSpace(before.Fingerprint)); !strings.EqualFold(fp, strings.ToUpper(hexFP(ring[0]))) {
		t.Errorf("the app was handed another key: %s, want %s", hexFP(ring[0]), fp)
	}
}

func hexFP(e *openpgp.Entity) string {
	const digits = "0123456789ABCDEF"
	out := make([]byte, 0, len(e.PrimaryKey.Fingerprint)*2)
	for _, b := range e.PrimaryKey.Fingerprint {
		out = append(out, digits[b>>4], digits[b&15])
	}
	return string(out)
}

// TestSigningOutClosesSealedMail — the console sign-out closes the key the
// sign-in opened.
func TestSigningOutClosesSealedMail(t *testing.T) {
	a, _, dana := sealedMailApp(t)
	makeDanaPrivate(t, a, dana)
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`CREATE TABLE sessions(token_hash TEXT PRIMARY KEY, user_id TEXT, expires_at DATETIME)`); err != nil {
		t.Fatal(err)
	}
	a.sessions = auth.NewSessionStore(db)
	token, err := a.sessions.Create(context.Background(), "vmail:dana@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if !a.vayuPGP.HeldOpen("dana@example.com") {
		t.Fatal("not open to begin with")
	}
	req := httptest.NewRequest(http.MethodPost, "/os/logout", nil)
	req.AddCookie(&http.Cookie{Name: auth.SessionCookie, Value: token})
	a.handleOSLogout(httptest.NewRecorder(), req)
	if a.vayuPGP.HeldOpen("dana@example.com") {
		t.Error("signing out left the sealed mail open")
	}
}

// TestAResetClosesSealedMail — every reset path ends the open key with the
// sessions it signs out.
func TestAResetClosesSealedMail(t *testing.T) {
	closed := ""
	d := mailResetDeps{
		SetPasswordHash: func(context.Context, string, string) error { return nil },
		CloseHeldKey:    func(email string) { closed = email },
	}
	if _, err := applyMailPasswordReset(context.Background(), d, "dana@example.com", "a long new password", mailResetByAdmin, "admin"); err != nil {
		t.Fatal(err)
	}
	if closed != "dana@example.com" {
		t.Errorf("the reset closed %q", closed)
	}
}
