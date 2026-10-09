// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/users"
	vmail "github.com/johalputt/vayupress/internal/vayuos/mail"
	vpgp "github.com/johalputt/vayupress/internal/vayuos/pgp"
)

// signingApp is an install whose mail reaches VayuPGP through the console's
// own bridge, as in production: dana and erin hold mailboxes, dana and the
// local bob@example.com have keys, erin has none.
func signingApp(t *testing.T) *App {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	udb, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = udb.Close() })
	a := &App{userStore: users.New(udb)}
	pcfg := vpgp.DefaultConfig()
	pcfg.Enabled = true
	pcfg.StorageDir = t.TempDir()
	pcfg.MasterSecret = []byte("test-master-secret")
	a.vayuPGP = vpgp.NewEngine(&pcfg)
	if err := a.vayuPGP.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.vayuPGP.Stop(context.Background()) })
	for _, who := range []string{"dana@example.com", "bob@example.com"} {
		if _, err := a.vayuPGP.EnsureKeypair(&vpgp.PGPUser{UserID: who, Name: who, Email: who}); err != nil {
			t.Fatal(err)
		}
	}
	cfg := vmail.DefaultConfig()
	cfg.Enabled = true
	cfg.Domain = "example.com"
	cfg.Hostname = "mail.example.com"
	cfg.StorageDir = t.TempDir()
	cfg.InboundEnabled = false
	a.vayuMail = vmail.NewEngine(&cfg, &vayuMailBridge{app: a}, db)
	if err := a.vayuMail.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.vayuMail.Stop(context.Background()) })
	for _, who := range []string{"dana@example.com", "erin@example.com", "bob@example.com"} {
		if err := a.vayuMail.Accounts().Create(context.Background(), who, "x", "", vmail.RoleMailbox); err != nil {
			t.Fatal(err)
		}
	}
	return a
}

// sentCopy is the one message in who's Sent folder.
func sentCopy(t *testing.T, a *App, who string) []byte {
	t.Helper()
	rd := vmail.ReadAsOwner(who)
	msgs, err := a.vayuMail.ListFolder(rd, "Sent")
	if err != nil || len(msgs) != 1 {
		t.Fatalf("%s's Sent holds %d messages (%v)", who, len(msgs), err)
	}
	raw, err := a.vayuMail.ReadFolderMessageStored(rd, "Sent", msgs[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// A signed message, with text a relay could rewrite (accents, trailing
// spaces), carries a signature the reader's own seal checks good, and the
// text reads back as written; one changed byte turns the seal red.
func TestASignedMessageCarriesAGoodSeal(t *testing.T) {
	a := signingApp(t)
	body := "Grüße aus Köln.  \r\nThe figures are final."
	if _, err := a.vayuMail.ComposeRich(context.Background(), vmail.ComposeMessage{From: "dana@example.com", To: []string{"someone@else.test"}, Subject: "Figures", Body: body, Sign: true}); err != nil {
		t.Fatal(err)
	}
	raw := sentCopy(t, a, "dana@example.com")
	if !bytes.Contains(raw, []byte("multipart/signed; micalg=pgp-sha256")) {
		t.Fatalf("not multipart/signed:\n%s", raw)
	}
	// Seven-bit throughout: a relay that recodes 8-bit text would break the
	// signature on its way.
	for i, c := range raw {
		if c >= 0x80 {
			t.Fatalf("an 8-bit byte at %d of a signed message:\n%s", i, raw)
		}
	}
	checker := sealKeys{pgp: a.vayuPGP}
	if s := mailSealFor(nil, checker, "dana@example.com", raw, "dana@example.com"); s.Tone != "ok" {
		t.Fatalf("the seal on dana's own signed message: %+v", s)
	}
	pm := vmail.ParseMessage(raw)
	if !strings.Contains(pm.Text, "Grüße aus Köln.") || !strings.Contains(pm.Text, "The figures are final.") {
		t.Fatalf("the signed text reads back as %q", pm.Text)
	}
	tampered := bytes.Replace(raw, []byte("final"), []byte("draft"), 1)
	if s := mailSealFor(nil, checker, "dana@example.com", tampered, "dana@example.com"); s.Tone != "danger" {
		t.Fatalf("a changed signed message: %+v", s)
	}
}

// Asked to sign, a sender with no key sends nothing at all.
func TestASenderWithoutAKeySendsNothingSigned(t *testing.T) {
	a := signingApp(t)
	_, err := a.vayuMail.ComposeRich(context.Background(), vmail.ComposeMessage{From: "erin@example.com", To: []string{"someone@else.test"}, Subject: "x", Body: "x", Sign: true})
	if !errors.Is(err, vmail.ErrNoSigningKey) {
		t.Fatalf("erin, keyless, asked to sign: %v", err)
	}
	if msgs, _ := a.vayuMail.ListFolder(vmail.ReadAsOwner("erin@example.com"), "Sent"); len(msgs) != 0 {
		t.Fatal("a message asked to be signed went out unsigned")
	}
}

// Encrypted and signed, the signature is inside the encryption and checks
// good once opened.
func TestAnEncryptedMessageIsSignedInside(t *testing.T) {
	a := signingApp(t)
	if _, err := a.vayuMail.ComposeRich(context.Background(), vmail.ComposeMessage{From: "dana@example.com", To: []string{"bob@example.com"}, Subject: "s", Body: "for bob", Encrypt: true, Sign: true}); err != nil {
		t.Fatal(err)
	}
	raw := sentCopy(t, a, "dana@example.com")
	if s := mailSealFor(nil, sealKeys{pgp: a.vayuPGP}, "dana@example.com", raw, "dana@example.com"); s.Tone != "ok" || !strings.Contains(s.Text, "Signed and encrypted") {
		t.Fatalf("the encrypted, signed message: %+v", s)
	}
}

// outsideKey is a key made by another install, for an address outside this
// one: its public half, and the engine holding its private half.
func outsideKey(t *testing.T, email string) ([]byte, *vpgp.Engine) {
	t.Helper()
	cfg := vpgp.DefaultConfig()
	cfg.Enabled = true
	cfg.StorageDir = t.TempDir()
	cfg.MasterSecret = []byte("another-install")
	e := vpgp.NewEngine(&cfg)
	if err := e.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = e.Stop(context.Background()) })
	if _, err := e.EnsureKeypair(&vpgp.PGPUser{UserID: email, Name: "Outside", Email: email}); err != nil {
		t.Fatal(err)
	}
	pub, err := e.ExportPublicKey(email)
	if err != nil {
		t.Fatal(err)
	}
	return pub, e
}

func keysAction(a *App, vals url.Values) string {
	req := withUser(httptest.NewRequest(http.MethodPost, "/os/vayumail/keys/action", strings.NewReader(vals.Encode())), danaHolder())
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	a.handleVayuOSKeysAction(rec, req)
	return rec.Body.String()
}

// A key dana adds is dana's: a message from dana to its address can be
// encrypted, erin's cannot; its owner's signature reads good to dana and
// unknown to erin; and Remove takes it away.
func TestAnOutsideKeyIsTheMailboxsOwn(t *testing.T) {
	a := signingApp(t)
	pub, outside := outsideKey(t, "ana@outside.test")
	if out := keysAction(a, url.Values{"action": {"add"}, "armor": {string(pub)}}); !strings.Contains(out, "Added the key for ana@outside.test") {
		t.Fatalf("add:\n%s", out)
	}
	dana, erin := vmail.ReadAsOwner("dana@example.com"), vmail.ReadAsOwner("erin@example.com")
	if !a.vayuPGP.CanEncryptTo("ana@outside.test", a.vayuMail.KnownKeys(dana)) {
		t.Fatal("dana cannot encrypt to the key she added")
	}
	if a.vayuPGP.CanEncryptTo("ana@outside.test", a.vayuMail.KnownKeys(erin)) {
		t.Fatal("erin can encrypt to a key dana added")
	}
	data := []byte("Content-Type: text/plain\r\n\r\nhello")
	sig, err := outside.SignDetachedFromEmail(data, "ana@outside.test")
	if err != nil {
		t.Fatal(err)
	}
	if v := (sealKeys{pgp: a.vayuPGP, known: a.vayuMail.KnownKeys(dana)}).CheckDetached(data, sig, "ana@outside.test"); v != vpgp.SigVerified {
		t.Fatalf("ana's signature, to dana: %v", v)
	}
	if v := (sealKeys{pgp: a.vayuPGP, known: a.vayuMail.KnownKeys(erin)}).CheckDetached(data, sig, "ana@outside.test"); v != vpgp.SigUnknownKey {
		t.Fatalf("ana's signature, to erin: %v", v)
	}
	// From compose: dana's message to ana goes encrypted, erin's readable.
	for who, want := range map[string]bool{"dana@example.com": true, "erin@example.com": false} {
		if _, err := a.vayuMail.ComposeRich(context.Background(), vmail.ComposeMessage{From: who, To: []string{"ana@outside.test"}, Subject: "s", Body: "b", Encrypt: true}); err != nil {
			t.Fatal(err)
		}
		if got := bytes.Contains(sentCopy(t, a, who), []byte("multipart/encrypted")); got != want {
			t.Errorf("%s to ana encrypted = %v", who, got)
		}
	}
	// The reader's seal of a message ana signed, as dana and as erin read it.
	signed := signedBy(t, outside, "ana@outside.test", "Ana's words.")
	for who, want := range map[string]string{"dana@example.com": "mx-seal--ok", "erin@example.com": "mx-seal--plain"} {
		id, err := a.vayuMail.DeliverInbound("ana@outside.test", who, signed)
		if err != nil {
			t.Fatal(err)
		}
		if card, _ := a.vayuReaderCard(vmail.ReadAsOwner(who), "Inbox", id, readerView{Pane: true}); !strings.Contains(card, want) {
			t.Errorf("%s's reader shows no %s:\n%.1500s", who, want, card)
		}
	}
	// Compose's recipient check, for dana and for erin.
	for u, want := range map[string]bool{"dana": true, "erin": false} {
		rec := httptest.NewRecorder()
		holder := &users.User{ID: "u-" + u, Email: u + "@example.com", Role: users.RoleAuthor, MailAddress: u + "@example.com"}
		a.handleVayuOSComposeRecipient(rec, withUser(httptest.NewRequest(http.MethodGet, "/os/vayumail/compose/recipient?addr=ana@outside.test", nil), holder))
		var got struct{ Key bool }
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || got.Key != want {
			t.Errorf("%s's recipient check: %s", u, rec.Body.String())
		}
	}
	// Remove is dana's alone: erin's key for ana stays.
	if _, err := a.vayuMail.AddContactKey(erin, pub); err != nil {
		t.Fatal(err)
	}
	keysAction(a, url.Values{"action": {"remove"}, "email": {"ana@outside.test"}})
	if a.vayuPGP.CanEncryptTo("ana@outside.test", a.vayuMail.KnownKeys(dana)) {
		t.Fatal("the removed key is still used")
	}
	if !a.vayuPGP.CanEncryptTo("ana@outside.test", a.vayuMail.KnownKeys(erin)) {
		t.Fatal("dana's Remove took erin's key too")
	}
}

// signedBy is a multipart/signed message from email, signed by e, as another
// install's mail client would send it.
func signedBy(t *testing.T, e *vpgp.Engine, email, text string) []byte {
	t.Helper()
	part := "Content-Type: text/plain; charset=utf-8\r\n\r\n" + text
	sig, err := e.SignDetachedFromEmail([]byte(part), email)
	if err != nil {
		t.Fatal(err)
	}
	return []byte("From: " + email + "\r\nSubject: Signed\r\nMIME-Version: 1.0\r\n" +
		"Content-Type: multipart/signed; micalg=pgp-sha256; protocol=\"application/pgp-signature\"; boundary=\"s\"\r\n\r\n" +
		"--s\r\n" + part + "\r\n--s\r\nContent-Type: application/pgp-signature\r\n\r\n" + string(sig) + "\r\n--s--\r\n")
}

// A read-only mailbox keeps no keys; deleting a mailbox removes its keys.
func TestKeysAreKeptOnlyByAWritableMailboxForItsLife(t *testing.T) {
	a := signingApp(t)
	pub, _ := outsideKey(t, "ana@outside.test")
	ctx := context.Background()
	if err := a.vayuMail.Accounts().SetRole(ctx, "erin@example.com", vmail.RoleReviewer); err != nil {
		t.Fatal(err)
	}
	if _, err := a.vayuMail.AddContactKey(vmail.ReadAsOwner("erin@example.com"), pub); err == nil {
		t.Fatal("a read-only mailbox kept a key")
	}
	dana := vmail.ReadAsOwner("dana@example.com")
	if _, err := a.vayuMail.AddContactKey(dana, pub); err != nil {
		t.Fatal(err)
	}
	if err := a.vayuMail.Accounts().Delete(ctx, "dana@example.com"); err != nil {
		t.Fatal(err)
	}
	if keys, _ := a.vayuMail.ContactKeys(dana); len(keys) != 0 {
		t.Fatalf("a deleted mailbox's keys outlived it: %+v", keys)
	}
}

// Sign every message is the holder's own to set, and compose follows it; the
// send handler signs when asked.
func TestTheSigningDefaultAndTheSendHandler(t *testing.T) {
	a := signingApp(t)
	req := withUser(httptest.NewRequest(http.MethodPost, "/os/vayumail/accounts/update", strings.NewReader(`{"email":"dana@example.com","sign_by_default":true}`)), danaHolder())
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	a.handleVayuOSAccountUpdate(rec, req)
	if rec.Code != http.StatusOK || !a.vayuMail.Accounts().SignsByDefault(context.Background(), "dana@example.com") {
		t.Fatalf("the holder's own default: %d %s", rec.Code, rec.Body.String())
	}
	crec := httptest.NewRecorder()
	a.handleVayuOSCompose(crec, withUser(httptest.NewRequest(http.MethodGet, "/os/vayumail/compose?user=dana", nil), danaHolder()))
	if !strings.Contains(crec.Body.String(), `data-can-sign="1" data-sign="1"`) {
		t.Fatalf("compose does not carry dana's default:\n%.3000s", crec.Body.String())
	}
	if rec := postSend(a, `{"to":"someone@else.test","subject":"s","body":"b","sign":true}`, danaHolder()); rec.Code != http.StatusOK {
		t.Fatalf("send: %d %s", rec.Code, rec.Body.String())
	}
	if !bytes.Contains(sentCopy(t, a, "dana@example.com"), []byte("multipart/signed")) {
		t.Fatal("the send handler's sign was not followed")
	}
	erinHolder := &users.User{ID: "u-erin", Email: "erin@example.com", Role: users.RoleAuthor, MailAddress: "erin@example.com"}
	if rec := postSend(a, `{"to":"someone@else.test","subject":"s","body":"b","sign":true}`, erinHolder); rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "no key to sign with") {
		t.Fatalf("keyless erin asked to sign: %d %s", rec.Code, rec.Body.String())
	}
}

// What cannot be added, one seed per refusal.
func TestWhatCannotBeAddedAsAKey(t *testing.T) {
	a := signingApp(t)
	local, _ := a.vayuPGP.ExportPublicKey("bob@example.com")
	ana, _ := outsideKey(t, "ana@outside.test")
	ben, _ := outsideKey(t, "ben@outside.test")
	priv, _ := a.vayuPGP.ArmoredPrivateKey("dana@example.com")
	for name, c := range map[string]struct{ armor, want string }{
		"not a key":     {"hello", "not an OpenPGP public key"},
		"two keys":      {string(ana) + "\n" + string(ben), "one key at a time"},
		"a private key": {priv, "private key"},
		"a local key":   {string(local), "an address this server serves"},
	} {
		if out := keysAction(a, url.Values{"action": {"add"}, "armor": {c.armor}}); !strings.Contains(out, c.want) {
			t.Errorf("%s: the panel says\n%s", name, out)
		}
	}
	if keys, _ := a.vayuMail.ContactKeys(vmail.ReadAsOwner("dana@example.com")); len(keys) != 0 {
		t.Fatalf("refused keys were kept: %+v", keys)
	}
}

// A key the mailbox adds never stands in for a key this install holds.
func TestTheInstallsOwnKeyComesFirst(t *testing.T) {
	a := signingApp(t)
	pub, _ := outsideKey(t, "bob@example.com")
	known := vpgp.KnownKeys{"bob@example.com": string(pub)}
	ct, _, err := a.vayuPGP.EncryptToRecipients([]byte("x"), []string{"bob@example.com"}, known, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.vayuPGP.DecryptForEmail(ct, "bob@example.com"); err != nil {
		t.Fatalf("encrypted to the planted key instead of bob's own: %v", err)
	}
}
