// SPDX-License-Identifier: Apache-2.0

package mail

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"
)

func sealedEngine(t *testing.T) *Engine {
	t.Helper()
	s := aliasTestStore(t)
	if err := s.Create(context.Background(), "erin@example.com", "hash", "", "mailbox"); err != nil {
		t.Fatal(err)
	}
	cfg := DefaultConfig()
	cfg.Domain = "example.com"
	return &Engine{cfg: cfg, maildir: NewMaildir(t.TempDir()), accounts: s}
}

const pass = "correct horse battery"

// A sealed message opens with its passphrase alone, for as long as its
// sender said, and is bound to its own id.
func TestASealedMessageOpensWithItsPassphrase(t *testing.T) {
	e := sealedEngine(t)
	me := ReadAsOwner("ankush")
	if _, err := e.Seal(me, "Ankush", "rohan@outside.example", "s", "b", "short", 24*time.Hour); !errors.Is(err, ErrSealedPassphrase) {
		t.Errorf("a short passphrase: %v", err)
	}
	if _, err := e.Seal(me, "Ankush", "rohan@outside.example", "s", "b", pass, 31*24*time.Hour); err == nil {
		t.Error("a message sealed for longer than thirty days")
	}
	if _, err := e.Seal(me, "Ankush", "Rohan <rohan@outside.example>\r\nBcc: x@y", "s", "b", pass, time.Hour); err == nil {
		t.Error("a recipient carrying a header was sealed for")
	}
	id, err := e.Seal(me, "Ankush", "Rohan <Rohan@Outside.example>", "Keys", "The bucket key is in the safe.", pass, 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	m, err := e.OpenSealed(id, pass)
	if err != nil || m.Subject != "Keys" || m.Body != "The bucket key is in the safe." || m.From != "Ankush" || m.Recipient != "rohan@outside.example" {
		t.Fatalf("opened: %+v %v", m, err)
	}
	if _, err := e.OpenSealed(id, pass+"!"); !errors.Is(err, ErrSealedGone) {
		t.Errorf("a wrong passphrase: %v", err)
	}
	// The box moved under another id does not open.
	other, _ := e.Seal(me, "Ankush", "rohan@outside.example", "x", "y", pass, time.Hour)
	db := e.accounts.db
	if _, err := db.Exec(`UPDATE vayumail_sealed SET box=(SELECT box FROM vayumail_sealed WHERE id=?), salt=(SELECT salt FROM vayumail_sealed WHERE id=?), nonce=(SELECT nonce FROM vayumail_sealed WHERE id=?) WHERE id=?`, id, id, id, other); err != nil {
		t.Fatal(err)
	}
	if _, err := e.OpenSealed(other, pass); !errors.Is(err, ErrSealedGone) {
		t.Errorf("a box moved to another id opened: %v", err)
	}
	// Its time up, it is gone.
	if _, err := db.Exec(`UPDATE vayumail_sealed SET expires=? WHERE id=?`, time.Now().Add(-time.Minute).Unix(), id); err != nil {
		t.Fatal(err)
	}
	if _, err := e.OpenSealed(id, pass); !errors.Is(err, ErrSealedGone) {
		t.Errorf("an expired message opened: %v", err)
	}
	var n int
	_ = db.QueryRow(`SELECT COUNT(1) FROM vayumail_sealed WHERE id=?`, id).Scan(&n)
	if n != 0 {
		t.Error("an expired message is still kept")
	}
}

// The tenth wrong passphrase closes it for good; the right one after that
// opens nothing.
func TestWrongPassphrasesCloseASealedMessage(t *testing.T) {
	e := sealedEngine(t)
	id, err := e.Seal(ReadAsOwner("ankush"), "Ankush", "rohan@outside.example", "s", "b", pass, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < sealedMaxFailures-1; i++ {
		_, _ = e.OpenSealed(id, "guess number "+string(rune('a'+i)))
	}
	if _, err := e.OpenSealed(id, pass); err != nil {
		t.Fatalf("closed before the tenth wrong passphrase: %v", err)
	}
	_, _ = e.OpenSealed(id, "the tenth wrong guess")
	if _, err := e.OpenSealed(id, pass); !errors.Is(err, ErrSealedGone) {
		t.Errorf("opened after ten wrong passphrases: %v", err)
	}
}

// An answer lands in the sender's mailbox from the address the message was
// for, marked as a sealed answer; there is a cap; and only the sender's
// mailbox lists or withdraws it.
func TestASealedMessageIsAnsweredAndWithdrawn(t *testing.T) {
	e := sealedEngine(t)
	me, erin := ReadAsOwner("ankush"), ReadAsOwner("erin")
	id, err := e.Seal(me, "Ankush", "rohan@outside.example", "Keys", "b", pass, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.ReplySealed(id, "wrong passphrase!", "hi"); !errors.Is(err, ErrSealedGone) {
		t.Errorf("answered with a wrong passphrase: %v", err)
	}
	if err := e.ReplySealed(id, pass, "Got it,\nthanks."); err != nil {
		t.Fatal(err)
	}
	ms, _ := e.ListFolder(me, "Inbox")
	if len(ms) != 1 || ms[0].Subject != "Re: Keys" || !strings.Contains(ms[0].From, "rohan@outside.example") {
		t.Fatalf("the answer: %+v", ms)
	}
	raw, _ := e.ReadFolderMessageStored(me, "Inbox", ms[0].ID)
	if !strings.Contains(string(raw), "X-VayuMail-Sealed-Reply: "+id) {
		t.Error("the answer is not marked as one")
	}
	if _, err := e.accounts.db.Exec(`UPDATE vayumail_sealed SET replies=? WHERE id=?`, sealedMaxReplies, id); err != nil {
		t.Fatal(err)
	}
	if err := e.ReplySealed(id, pass, "more"); !errors.Is(err, ErrSealedTooManyReplies) {
		t.Errorf("past the cap: %v", err)
	}
	if l, _ := e.SealedSent(erin); len(l) != 0 {
		t.Error("another mailbox lists this one's sealed messages")
	}
	if err := e.WithdrawSealed(erin, id); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("another mailbox withdrew it: %v", err)
	}
	if l, _ := e.SealedSent(me); len(l) != 1 || l[0].Opened != 2 || l[0].Replies != sealedMaxReplies {
		t.Errorf("the sender's list: %+v", l)
	}
	if err := e.WithdrawSealed(me, id); err != nil {
		t.Fatal(err)
	}
	if _, err := e.OpenSealed(id, pass); !errors.Is(err, ErrSealedGone) {
		t.Error("a withdrawn message opened")
	}
}
