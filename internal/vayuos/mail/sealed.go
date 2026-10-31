// SPDX-License-Identifier: Apache-2.0

package mail

// sealed.go — mail to anyone, sealed. For a person outside who has no key, a
// message is not sent at all: it stays on this server, sealed with a key
// drawn from a passphrase the sender shares another way, and the person is
// sent only a link. They open it on the site with the passphrase, it is gone
// when the sender said, and they can answer it from the same page, the
// answer landing in the sender's mailbox.
//
// The server keeps no way to open it: only a salt, a nonce and the sealed
// box. The key is Argon2id over the passphrase, so a stolen database is
// worth a dictionary attack per message and no more; a wrong passphrase is
// counted, and the tenth seals the message for good.

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"mime"
	netmail "net/mail"
	"strings"
	"time"

	"golang.org/x/crypto/argon2"
)

// Sealed-message limits.
const (
	SealedMinPassphrase = 10                  // characters
	SealedMaxTTL        = 30 * 24 * time.Hour // the longest a sender may keep one open
	sealedMaxFailures   = 10                  // wrong passphrases before it is gone
	sealedMaxReplies    = 20                  // answers one message can carry back
	sealedMaxBody       = 200 << 10           // bytes of message or answer
)

var (
	// ErrSealedGone is a sealed message that is not here: never made, expired,
	// withdrawn, or closed after too many wrong passphrases. One error for all,
	// so the page says nothing about which.
	ErrSealedGone = errors.New("vayumail: this sealed message is not here, or the passphrase is not its own")
	// ErrSealedPassphrase is a passphrase too short to seal with.
	ErrSealedPassphrase = errors.New("vayumail: a passphrase is at least 10 characters")
	// ErrSealedTooManyReplies refuses an answer past sealedMaxReplies.
	ErrSealedTooManyReplies = errors.New("vayumail: this message has taken all the answers it can")
)

// SealedMessage is a sealed message as its sender's list shows it, or as
// its reader sees it once opened (Subject and Body are filled only then).
type SealedMessage struct {
	ID, Recipient, Subject, Body, From string
	Created, Expires                   time.Time
	Opened, Replies                    int
}

func (s *AccountStore) ensureSealedTable() error {
	if s.db == nil {
		return errors.New("vayumail: no storage")
	}
	_, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS vayumail_sealed(
		id TEXT PRIMARY KEY,
		mailbox TEXT NOT NULL,
		sender TEXT NOT NULL,
		recipient TEXT NOT NULL,
		salt BLOB NOT NULL,
		nonce BLOB NOT NULL,
		box BLOB NOT NULL,
		created INTEGER NOT NULL,
		expires INTEGER NOT NULL,
		failures INTEGER NOT NULL DEFAULT 0,
		opened INTEGER NOT NULL DEFAULT 0,
		replies INTEGER NOT NULL DEFAULT 0);`)
	return err
}

// sealedKey is the message key drawn from a passphrase: Argon2id at the
// cost the install's own secrets use for a stored passphrase.
func sealedKey(passphrase string, salt []byte) []byte {
	return argon2.IDKey([]byte(passphrase), salt, 3, 64*1024, 2, 32)
}

func sealedAEAD(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// sealedContent is what the box holds.
type sealedContent struct {
	Subject string `json:"s"`
	Body    string `json:"b"`
}

// Seal keeps a message for recipient, sealed with passphrase, open for ttl,
// and gives its id; from is how the sender is named to the reader. The id
// is bound into the seal, so a box moved to another id does not open.
func (e *Engine) Seal(rd Reader, from, recipient, subject, body, passphrase string, ttl time.Duration) (string, error) {
	if err := e.writeAuthorised(rd); err != nil {
		return "", err
	}
	if e.accounts == nil || e.accounts.ensureSealedTable() != nil {
		return "", errors.New("vayumail: not started")
	}
	if len([]rune(passphrase)) < SealedMinPassphrase {
		return "", ErrSealedPassphrase
	}
	if ttl <= 0 || ttl > SealedMaxTTL {
		return "", errors.New("vayumail: a sealed message stays open for a day to thirty days")
	}
	if len(subject)+len(body) > sealedMaxBody {
		return "", errors.New("vayumail: the message is too long to seal")
	}
	a, perr := netmail.ParseAddress(recipient)
	if perr != nil || !strings.Contains(a.Address, "@") {
		return "", errors.New("vayumail: a sealed message needs the address of the person it is for")
	}
	recipient = normEmail(a.Address)
	idb, salt, nonce := make([]byte, 16), make([]byte, 16), make([]byte, 12)
	for _, b := range [][]byte{idb, salt, nonce} {
		if _, err := rand.Read(b); err != nil {
			return "", err
		}
	}
	id := base64.RawURLEncoding.EncodeToString(idb)
	plain, _ := json.Marshal(sealedContent{Subject: subject, Body: body})
	aead, err := sealedAEAD(sealedKey(passphrase, salt))
	if err != nil {
		return "", err
	}
	box := aead.Seal(nil, nonce, plain, []byte(id))
	now := time.Now()
	ctx := context.Background()
	e.sweepSealed(ctx, now)
	_, err = e.accounts.db.ExecContext(ctx,
		`INSERT INTO vayumail_sealed(id,mailbox,sender,recipient,salt,nonce,box,created,expires) VALUES(?,?,?,?,?,?,?,?,?)`,
		id, e.mailboxAddr(rd), from, recipient, salt, nonce, box, now.Unix(), now.Add(ttl).Unix())
	return id, err
}

// sweepSealed forgets every sealed message whose time is up.
func (e *Engine) sweepSealed(ctx context.Context, now time.Time) {
	_, _ = e.accounts.db.ExecContext(ctx, `DELETE FROM vayumail_sealed WHERE expires <= ?`, now.Unix())
}

// OpenSealed opens message id with passphrase for the person it was sent
// to. A wrong passphrase is counted, and at sealedMaxFailures the message
// is gone; every refusal is ErrSealedGone.
func (e *Engine) OpenSealed(id, passphrase string) (SealedMessage, error) {
	if e.accounts == nil || e.accounts.ensureSealedTable() != nil {
		return SealedMessage{}, ErrSealedGone
	}
	ctx := context.Background()
	now := time.Now()
	e.sweepSealed(ctx, now)
	var m SealedMessage
	var salt, nonce, box []byte
	var created, expires int64
	err := e.accounts.db.QueryRowContext(ctx,
		`SELECT sender, recipient, salt, nonce, box, created, expires, opened, replies FROM vayumail_sealed WHERE id=?`, id).
		Scan(&m.From, &m.Recipient, &salt, &nonce, &box, &created, &expires, &m.Opened, &m.Replies)
	if err != nil {
		return SealedMessage{}, ErrSealedGone
	}
	aead, err := sealedAEAD(sealedKey(passphrase, salt))
	if err != nil {
		return SealedMessage{}, ErrSealedGone
	}
	plain, err := aead.Open(nil, nonce, box, []byte(id))
	if err != nil {
		_, _ = e.accounts.db.ExecContext(ctx, `UPDATE vayumail_sealed SET failures=failures+1 WHERE id=?`, id)
		_, _ = e.accounts.db.ExecContext(ctx, `DELETE FROM vayumail_sealed WHERE id=? AND failures >= ?`, id, sealedMaxFailures)
		return SealedMessage{}, ErrSealedGone
	}
	var c sealedContent
	if json.Unmarshal(plain, &c) != nil {
		return SealedMessage{}, ErrSealedGone
	}
	_, _ = e.accounts.db.ExecContext(ctx, `UPDATE vayumail_sealed SET opened=opened+1 WHERE id=?`, id)
	m.ID, m.Subject, m.Body = id, c.Subject, c.Body
	m.Created, m.Expires = time.Unix(created, 0), time.Unix(expires, 0)
	m.Opened++
	return m, nil
}

// ReplySealed delivers an answer to message id, written by the person who
// holds its passphrase, into its sender's mailbox. It is marked as such, and
// its From is the address the message was sent to, never one the form names.
func (e *Engine) ReplySealed(id, passphrase, text string) error {
	m, err := e.OpenSealed(id, passphrase)
	if err != nil {
		return err
	}
	if m.Replies >= sealedMaxReplies {
		return ErrSealedTooManyReplies
	}
	text = strings.TrimSpace(text)
	if text == "" || len(text) > sealedMaxBody {
		return errors.New("vayumail: an answer is between one character and 200 KB")
	}
	ctx := context.Background()
	var mailbox string
	if err := e.accounts.db.QueryRowContext(ctx, `SELECT mailbox FROM vayumail_sealed WHERE id=?`, id).Scan(&mailbox); err != nil {
		return ErrSealedGone
	}
	subject := m.Subject
	if !strings.HasPrefix(strings.ToLower(subject), "re:") {
		subject = "Re: " + subject
	}
	subject = strings.NewReplacer("\r", " ", "\n", " ").Replace(subject)
	raw := "From: <" + m.Recipient + ">\r\nTo: <" + mailbox + ">\r\nSubject: " + mime.QEncoding.Encode("utf-8", subject) +
		"\r\nDate: " + time.Now().Format(time.RFC1123Z) + "\r\nMessage-ID: " + e.messageID("") +
		"\r\nX-VayuMail-Sealed-Reply: " + id + "\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=utf-8\r\nContent-Transfer-Encoding: 8bit\r\n\r\n" +
		"Answered on the sealed message's page by whoever holds its passphrase.\r\n\r\n" + strings.ReplaceAll(text, "\n", "\r\n") + "\r\n"
	if _, err := e.deliver(m.Recipient, mailbox, []byte(raw), false); err != nil {
		return err
	}
	_, err = e.accounts.db.ExecContext(ctx, `UPDATE vayumail_sealed SET replies=replies+1 WHERE id=?`, id)
	return err
}

// SealedSent is the sealed messages the mailbox rd reads has sent and that
// are still open, newest first, without their contents.
func (e *Engine) SealedSent(rd Reader) ([]SealedMessage, error) {
	if err := e.readAuthorised(rd); err != nil {
		return nil, err
	}
	if e.accounts == nil || e.accounts.ensureSealedTable() != nil {
		return nil, nil
	}
	ctx := context.Background()
	e.sweepSealed(ctx, time.Now())
	rows, err := e.accounts.db.QueryContext(ctx,
		`SELECT id, recipient, created, expires, opened, replies FROM vayumail_sealed WHERE mailbox=? ORDER BY created DESC`, e.mailboxAddr(rd))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SealedMessage
	for rows.Next() {
		var m SealedMessage
		var created, expires int64
		if err := rows.Scan(&m.ID, &m.Recipient, &created, &expires, &m.Opened, &m.Replies); err != nil {
			return nil, err
		}
		m.Created, m.Expires = time.Unix(created, 0), time.Unix(expires, 0)
		out = append(out, m)
	}
	return out, rows.Err()
}

// WithdrawSealed closes one of the mailbox's own sealed messages now.
func (e *Engine) WithdrawSealed(rd Reader, id string) error {
	if err := e.writeAuthorised(rd); err != nil {
		return err
	}
	if e.accounts == nil || e.accounts.ensureSealedTable() != nil {
		return errors.New("vayumail: not started")
	}
	res, err := e.accounts.db.ExecContext(context.Background(), `DELETE FROM vayumail_sealed WHERE id=? AND mailbox=?`, id, e.mailboxAddr(rd))
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	return nil
}
