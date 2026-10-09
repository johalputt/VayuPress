// SPDX-License-Identifier: Apache-2.0

package mail

// dav.go — a mailbox's contacts and calendar as phones and desktop apps keep
// them in step (CardDAV, RFC 6352; CalDAV, RFC 4791). davserver.go speaks the
// protocols; this file keeps what they store.
//
// A card or an event is kept as the app wrote it, so what an app holds that
// the console has no field for (a phone number, a photo, an alarm) comes back
// to it unchanged.
//
// The address book is the console's contacts (contacts.go), not a second
// one, kept in step both ways:
//   - a contact no card holds is offered to apps as a card made from it, under
//     a name that spells its address and when it was saved (madeCardName), so
//     a write or delete of that card finds the contact without a lookup;
//   - a card an app writes saves the addresses it holds as contacts, under its
//     name, and an address it no longer holds, which no other card holds,
//     stops being a contact; deleting the card does the same;
//   - the console renaming or deleting a contact changes the card that holds
//     it (cardsRenamed, cardsDropped).
//
// Groups stay the console's. A card's CATEGORIES are kept as written and do
// not change them: an app's categories are its own list, and taking them as
// groups would let a phone's "Favourites" become a group mail is sent to.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base32"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/emersion/go-vcard"
)

type davKind string

const (
	davCard  davKind = "card"
	davEvent davKind = "event"
)

const (
	// maxDAVObject is one card or event. A contact's photo is what makes a
	// card large, and a megabyte holds a phone's.
	maxDAVObject = 1 << 20
	// maxDAVObjects (per kind) and maxDAVBytes (in all) bound what one
	// mailbox keeps, so an app cannot fill the database.
	maxDAVObjects = 10000
	maxDAVBytes   = 100 << 20
	// maxCardEmails is how many of a card's addresses become contacts. A
	// person has a handful; a card with thousands is a list being smuggled
	// into the address book a megabyte at a time.
	maxCardEmails = 50
)

var (
	errDAVNotFound = errors.New("there is nothing at that address")
	errDAVChanged  = errors.New("it has changed since it was read")
	errDAVUIDTaken = errors.New("another entry already has this UID")
	errDAVTooLarge = errors.New("an entry is at most a megabyte")
	errDAVFull     = errors.New("this mailbox keeps no more contacts or events")
)

// davObject is one stored card or event.
type davObject struct {
	Name     string
	ETag     string
	Data     []byte
	Modified time.Time
}

func davETag(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:16])
}

// davCondition is a write's If-Match and If-None-Match: "" when not given,
// "*" for any entry, else an entity tag.
type davCondition struct{ ifMatch, ifNoneMatch string }

// holds reports whether the condition allows a write over current, the
// entry's entity tag ("" when there is none).
func (c davCondition) holds(current string) bool {
	if c.ifMatch != "" && (current == "" || (c.ifMatch != "*" && c.ifMatch != current)) {
		return false
	}
	if c.ifNoneMatch != "" && current != "" && (c.ifNoneMatch == "*" || c.ifNoneMatch == current) {
		return false
	}
	return true
}

// madeCardName is the name a contact's made card is offered under. It spells
// the address, so the card's contact is read from its name; and when the
// contact was saved, so a contact saved again after an app took its card's
// name for another person is not offered under that name a second time.
func madeCardName(email string, saved time.Time) string {
	return "vp-" + strings.ToLower(madeCardEnc.EncodeToString([]byte(email))) + "-" + strconv.FormatInt(saved.Unix(), 36) + ".vcf"
}

var madeCardEnc = base32.HexEncoding.WithPadding(base32.NoPadding)

// madeCardContact is the address a made card's name spells. The name is
// checked against the one madeCardName gives, so no other spelling of it
// (another case, a different time) reaches the contact.
func madeCardContact(name string) (string, bool) {
	stem, prefixed := strings.CutPrefix(name, "vp-")
	stem, suffixed := strings.CutSuffix(stem, ".vcf")
	enc, _, timed := strings.Cut(stem, "-")
	if !prefixed || !suffixed || !timed {
		return "", false
	}
	b, err := madeCardEnc.DecodeString(strings.ToUpper(enc))
	if err != nil {
		return "", false
	}
	return string(b), true
}

// madeCard is the card offered for a contact no card holds.
func madeCard(email, name string, saved time.Time) davObject {
	if name == "" {
		name = email
	}
	file := madeCardName(email, saved)
	c := vcard.Card{}
	c.SetValue(vcard.FieldVersion, "3.0")
	c.SetValue(vcard.FieldUID, strings.TrimSuffix(file, ".vcf"))
	c.SetValue(vcard.FieldFormattedName, name)
	c.SetName(&vcard.Name{GivenName: name})
	c.Add(vcard.FieldEmail, &vcard.Field{Value: email, Params: vcard.Params{vcard.ParamType: {"INTERNET"}}})
	var b bytes.Buffer
	_ = vcard.NewEncoder(&b).Encode(c)
	return davObject{Name: file, ETag: davETag(b.Bytes()), Data: b.Bytes(), Modified: saved}
}

// davQuerier is what both the store and a transaction answer.
type davQuerier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// madeCardFor is the made card offered under name, if one is: its contact
// exists, was saved when the name says, and no card holds its address.
func madeCardFor(ctx context.Context, q davQuerier, mailbox, name string) (davObject, string, bool) {
	email, ok := madeCardContact(name)
	if !ok {
		return davObject{}, "", false
	}
	var cname string
	var at time.Time
	err := q.QueryRowContext(ctx, `SELECT name,created_at FROM vayumail_contacts c WHERE owner=? AND email=?
		AND NOT EXISTS (SELECT 1 FROM vayumail_dav_emails d WHERE d.mailbox=c.owner AND d.email=c.email)`, mailbox, email).Scan(&cname, &at)
	if err != nil || madeCardName(email, at) != name {
		return davObject{}, "", false
	}
	return madeCard(email, cname, at), email, true
}

// storedDAV is the stored entry name, if there is one.
func storedDAV(ctx context.Context, q davQuerier, mailbox string, kind davKind, name string) (davObject, bool, error) {
	o := davObject{Name: name}
	err := q.QueryRowContext(ctx, `SELECT etag,data,modified FROM vayumail_dav WHERE mailbox=? AND kind=? AND name=?`, mailbox, kind, name).
		Scan(&o.ETag, &o.Data, &o.Modified)
	if errors.Is(err, sql.ErrNoRows) {
		return o, false, nil
	}
	return o, err == nil, err
}

// davList is every entry of kind in mailbox: for cards, the stored ones and
// a made card for each contact none of them holds.
func (s *AccountStore) davList(ctx context.Context, mailbox string, kind davKind) ([]davObject, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT name,etag,data,modified FROM vayumail_dav WHERE mailbox=? AND kind=? ORDER BY name`, mailbox, kind)
	if err != nil {
		return nil, err
	}
	var out []davObject
	taken := map[string]bool{}
	for rows.Next() {
		var o davObject
		if err := rows.Scan(&o.Name, &o.ETag, &o.Data, &o.Modified); err != nil {
			_ = rows.Close()
			return nil, err
		}
		taken[o.Name] = true
		out = append(out, o)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil || kind != davCard {
		return out, err
	}
	rows, err = s.db.QueryContext(ctx, `SELECT email,name,created_at FROM vayumail_contacts c WHERE owner=?
		AND NOT EXISTS (SELECT 1 FROM vayumail_dav_emails d WHERE d.mailbox=c.owner AND d.email=c.email) ORDER BY email`, mailbox)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var email, name string
		var at time.Time
		if err := rows.Scan(&email, &name, &at); err != nil {
			return nil, err
		}
		if o := madeCard(email, name, at); !taken[o.Name] {
			out = append(out, o)
		}
	}
	return out, rows.Err()
}

// davGet is the entry name of kind in mailbox.
func (s *AccountStore) davGet(ctx context.Context, mailbox string, kind davKind, name string) (davObject, error) {
	o, ok, err := storedDAV(ctx, s.db, mailbox, kind, name)
	if err != nil || ok {
		return o, err
	}
	if kind == davCard {
		if o, _, ok := madeCardFor(ctx, s.db, mailbox, name); ok {
			return o, nil
		}
	}
	return davObject{}, errDAVNotFound
}

// davPut keeps data as the entry name of kind in mailbox, if cond holds over
// what is there. uid is the entry's UID, which no other entry of the kind
// may have.
func (s *AccountStore) davPut(ctx context.Context, mailbox string, kind davKind, name, uid string, data []byte, cond davCondition) (davObject, error) {
	if len(data) > maxDAVObject {
		return davObject{}, errDAVTooLarge
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return davObject{}, err
	}
	defer func() { _ = tx.Rollback() }()
	cur, stored, err := storedDAV(ctx, tx, mailbox, kind, name)
	if err != nil {
		return davObject{}, err
	}
	var madeFor string
	if !stored && kind == davCard {
		cur, madeFor, _ = madeCardFor(ctx, tx, mailbox, name)
	}
	if !cond.holds(cur.ETag) {
		return davObject{}, errDAVChanged
	}
	var other string
	if err := tx.QueryRowContext(ctx, `SELECT name FROM vayumail_dav WHERE mailbox=? AND kind=? AND uid=? AND name<>?`, mailbox, kind, uid, name).Scan(&other); err == nil {
		return davObject{}, errDAVUIDTaken
	} else if !errors.Is(err, sql.ErrNoRows) {
		return davObject{}, err
	}
	var count, kindBytes, allBytes int64
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(1), COALESCE(SUM(LENGTH(data)),0) FROM vayumail_dav WHERE mailbox=? AND kind=? AND name<>?`, mailbox, kind, name).Scan(&count, &kindBytes); err != nil {
		return davObject{}, err
	}
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(SUM(LENGTH(data)),0) FROM vayumail_dav WHERE mailbox=? AND kind<>?`, mailbox, kind).Scan(&allBytes); err != nil {
		return davObject{}, err
	}
	if count >= maxDAVObjects || allBytes+kindBytes+int64(len(data)) > maxDAVBytes {
		return davObject{}, errDAVFull
	}
	o := davObject{Name: name, ETag: davETag(data), Data: data, Modified: time.Now().UTC().Truncate(time.Second)}
	if _, err := tx.ExecContext(ctx, `INSERT INTO vayumail_dav(mailbox,kind,name,uid,etag,data,modified) VALUES(?,?,?,?,?,?,?)
		ON CONFLICT(mailbox,kind,name) DO UPDATE SET uid=excluded.uid, etag=excluded.etag, data=excluded.data, modified=excluded.modified`,
		mailbox, kind, name, uid, o.ETag, data, o.Modified); err != nil {
		return davObject{}, err
	}
	if kind == davCard {
		if err := cardWritten(ctx, tx, mailbox, name, madeFor, data); err != nil {
			return davObject{}, err
		}
	}
	return o, tx.Commit()
}

// davDelete removes the entry name of kind in mailbox, if cond holds over it.
func (s *AccountStore) davDelete(ctx context.Context, mailbox string, kind davKind, name string, cond davCondition) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	cur, stored, err := storedDAV(ctx, tx, mailbox, kind, name)
	if err != nil {
		return err
	}
	var madeFor string
	if !stored && kind == davCard {
		cur, madeFor, _ = madeCardFor(ctx, tx, mailbox, name)
	}
	switch {
	case cur.ETag == "":
		return errDAVNotFound
	case !cond.holds(cur.ETag):
		return errDAVChanged
	case madeFor != "":
		if err := forgetContact(ctx, tx, mailbox, madeFor); err != nil {
			return err
		}
	default:
		if _, err := tx.ExecContext(ctx, `DELETE FROM vayumail_dav WHERE mailbox=? AND kind=? AND name=?`, mailbox, kind, name); err != nil {
			return err
		}
		if kind == davCard {
			if err := cardWritten(ctx, tx, mailbox, name, "", nil); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

// cardWritten brings the contacts into step with the card name now holding
// data (nil when it was deleted). madeFor is the contact whose made card an
// app has just written over, which the card held until now.
func cardWritten(ctx context.Context, tx *sql.Tx, mailbox, name, madeFor string, data []byte) error {
	held := map[string]bool{}
	if madeFor != "" {
		held[madeFor] = true
	}
	rows, err := tx.QueryContext(ctx, `SELECT email FROM vayumail_dav_emails WHERE mailbox=? AND name=?`, mailbox, name)
	if err != nil {
		return err
	}
	for rows.Next() {
		var e string
		if err := rows.Scan(&e); err != nil {
			_ = rows.Close()
			return err
		}
		held[e] = true
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM vayumail_dav_emails WHERE mailbox=? AND name=?`, mailbox, name); err != nil {
		return err
	}
	person, emails := cardPerson(mailbox, data)
	for _, e := range emails {
		delete(held, e)
		if _, err := tx.ExecContext(ctx, `INSERT INTO vayumail_dav_emails(mailbox,name,email) VALUES(?,?,?)`, mailbox, name, e); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO vayumail_contacts(owner,email,name) VALUES(?,?,?)
			ON CONFLICT(owner,email) DO UPDATE SET name=CASE WHEN excluded.name<>'' THEN excluded.name ELSE vayumail_contacts.name END`, mailbox, e, person); err != nil {
			return err
		}
	}
	for e := range held {
		var one int
		err := tx.QueryRowContext(ctx, `SELECT 1 FROM vayumail_dav_emails WHERE mailbox=? AND email=?`, mailbox, e).Scan(&one)
		if errors.Is(err, sql.ErrNoRows) {
			err = forgetContact(ctx, tx, mailbox, e)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// cardPerson is the name and the addresses a card gives, read as an import
// reads one (vcard.go): at most maxCardEmails addresses, never the mailbox's
// own.
func cardPerson(mailbox string, data []byte) (string, []string) {
	cards, err := ParseVCards(data)
	if err != nil || len(cards) == 0 {
		return "", nil
	}
	name := strings.TrimSpace(cards[0].Name)
	if r := []rune(name); len(r) > 200 {
		name = string(r[:200])
	}
	seen := map[string]bool{mailbox: true}
	var emails []string
	for _, e := range cards[0].Emails {
		if e = normEmail(e); strings.Contains(e, "@") && !seen[e] && len(emails) < maxCardEmails {
			seen[e] = true
			emails = append(emails, e)
		}
	}
	return name, emails
}

// forgetContact removes email from mailbox's contacts and their groups, as
// DeleteContact does.
func forgetContact(ctx context.Context, tx *sql.Tx, mailbox, email string) error {
	for _, q := range []string{`DELETE FROM vayumail_contacts WHERE owner=? AND email=?`, `DELETE FROM vayumail_contact_groups WHERE owner=? AND email=?`} {
		if _, err := tx.ExecContext(ctx, q, mailbox, email); err != nil {
			return err
		}
	}
	return nil
}

// cardsHolding is the stored cards that hold email.
func cardsHolding(ctx context.Context, tx *sql.Tx, owner, email string) ([]davObject, error) {
	rows, err := tx.QueryContext(ctx, `SELECT d.name,d.data FROM vayumail_dav d JOIN vayumail_dav_emails e ON e.mailbox=d.mailbox AND e.name=d.name
		WHERE d.mailbox=? AND d.kind=? AND e.email=?`, owner, davCard, email)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []davObject
	for rows.Next() {
		var o davObject
		if err := rows.Scan(&o.Name, &o.Data); err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// rewriteCard keeps c as the stored card name.
func rewriteCard(ctx context.Context, tx *sql.Tx, owner, name string, c vcard.Card) error {
	var b bytes.Buffer
	if err := vcard.NewEncoder(&b).Encode(c); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `UPDATE vayumail_dav SET data=?, etag=?, modified=? WHERE mailbox=? AND kind=? AND name=?`,
		b.Bytes(), davETag(b.Bytes()), time.Now().UTC().Truncate(time.Second), owner, davCard, name)
	return err
}

// cardsRenamed gives the console's new name for email to the cards that
// hold it, and so to the card's other addresses too, since a card is one
// person. An empty name leaves them as they are.
func cardsRenamed(ctx context.Context, tx *sql.Tx, owner, email, name string) error {
	if name == "" {
		return nil
	}
	cards, err := cardsHolding(ctx, tx, owner, email)
	if err != nil {
		return err
	}
	for _, o := range cards {
		c, err := vcard.NewDecoder(bytes.NewReader(o.Data)).Decode()
		if err != nil || c.PreferredValue(vcard.FieldFormattedName) == name {
			continue
		}
		c.SetValue(vcard.FieldFormattedName, name)
		// Apps show N before FN when a card has one, so a rename that left
		// N would not be seen on the phone.
		if c.Name() != nil {
			c.SetName(&vcard.Name{GivenName: name})
		}
		if err := rewriteCard(ctx, tx, owner, o.Name, c); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE vayumail_contacts SET name=? WHERE owner=? AND email IN
			(SELECT email FROM vayumail_dav_emails WHERE mailbox=? AND name=?)`, name, owner, owner, o.Name); err != nil {
			return err
		}
	}
	return nil
}

// cardsDropped takes email off the cards that hold it, and deletes a card
// left holding no contact's address: deleting a contact deletes the person,
// here and on the phone, as an address book shared by both should. A card
// that holds only the mailbox's own address (an app's "my card") is never a
// contact's, so it is never reached here.
func cardsDropped(ctx context.Context, tx *sql.Tx, owner, email string) error {
	cards, err := cardsHolding(ctx, tx, owner, email)
	if err != nil {
		return err
	}
	for _, o := range cards {
		if _, err := tx.ExecContext(ctx, `DELETE FROM vayumail_dav_emails WHERE mailbox=? AND name=? AND email=?`, owner, o.Name, email); err != nil {
			return err
		}
		var left int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(1) FROM vayumail_dav_emails WHERE mailbox=? AND name=?`, owner, o.Name).Scan(&left); err != nil {
			return err
		}
		if left == 0 {
			if _, err := tx.ExecContext(ctx, `DELETE FROM vayumail_dav WHERE mailbox=? AND kind=? AND name=?`, owner, davCard, o.Name); err != nil {
				return err
			}
			continue
		}
		c, err := vcard.NewDecoder(bytes.NewReader(o.Data)).Decode()
		if err != nil {
			return err
		}
		var kept []*vcard.Field
		for _, f := range c[vcard.FieldEmail] {
			if normEmail(vcardAddress(f.Value)) != email {
				kept = append(kept, f)
			}
		}
		c[vcard.FieldEmail] = kept
		if err := rewriteCard(ctx, tx, owner, o.Name, c); err != nil {
			return err
		}
	}
	return nil
}
