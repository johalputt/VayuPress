// SPDX-License-Identifier: Apache-2.0

package mail

// throwaway.go — addresses a holder makes in one click, one for each website
// that asks for an address: shop-x7k2qa@domain delivers into their mailbox
// like any alias, says how much mail it has taken, and is turned off when
// that website starts selling it on. An off address takes nothing, not even
// through the domain's catch-all (catchAllFor), so it can be left to bounce.

import (
	"context"
	"crypto/rand"
	"errors"
	"strings"
	"time"
)

// throwawayKind marks an alias as a holder's throwaway address.
const throwawayKind = "throwaway"

// MaxThrowaways is how many throwaway addresses one mailbox may hold.
const MaxThrowaways = 100

// ErrTooManyThrowaways refuses a throwaway address past MaxThrowaways.
var ErrTooManyThrowaways = errors.New("vayumail: this mailbox already has as many throwaway addresses as it may; remove one first")

// Throwaway is one throwaway address as its holder's list shows it.
type Throwaway struct {
	Address, Label string
	Received       int
	LastReceived   time.Time
	Off            bool
	Created        time.Time
}

// throwawayAlphabet is what the random part is drawn from: no letter that
// reads as a digit, so an address read aloud is typed right.
const throwawayAlphabet = "abcdefghjkmnpqrstuvwxyz23456789"

// throwawayLocal is the address's local part: the website's name, cut to
// what an address may hold, then a random part.
func throwawayLocal(label string) (string, error) {
	var name strings.Builder
	for _, r := range strings.ToLower(label) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			name.WriteRune(r)
		}
		if name.Len() == 16 {
			break
		}
	}
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	for i := range b {
		b[i] = throwawayAlphabet[int(b[i])%len(throwawayAlphabet)]
	}
	if name.Len() == 0 {
		return "x-" + string(b), nil
	}
	return name.String() + "-" + string(b), nil
}

// NewThrowaway makes a throwaway address for label (the website it is for)
// on the domain of the mailbox rd reads, delivering into it.
func (e *Engine) NewThrowaway(rd Reader, label string) (Throwaway, error) {
	if err := e.writeAuthorised(rd); err != nil {
		return Throwaway{}, err
	}
	if e.accounts == nil || e.accounts.db == nil {
		return Throwaway{}, errors.New("vayumail: not started")
	}
	mbox := e.mailboxAddr(rd)
	_, domain := splitAddress(mbox)
	label = strings.TrimSpace(label)
	if len(label) > 60 {
		label = label[:60]
	}
	ctx := context.Background()
	var n int
	_ = e.accounts.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM vayumail_aliases WHERE target=? AND kind=?`, mbox, throwawayKind).Scan(&n)
	if n >= MaxThrowaways {
		return Throwaway{}, ErrTooManyThrowaways
	}
	for range 3 { // a clash with an existing address is drawn again
		local, err := throwawayLocal(label)
		if err != nil {
			return Throwaway{}, err
		}
		addr := local + "@" + domain
		if err := e.accounts.CreateAlias(ctx, addr, mbox); err != nil {
			continue
		}
		now := time.Now().UTC()
		if _, err := e.accounts.db.ExecContext(ctx, `UPDATE vayumail_aliases SET kind=?, label=? WHERE alias=?`, throwawayKind, label, addr); err != nil {
			return Throwaway{}, err
		}
		return Throwaway{Address: addr, Label: label, Created: now}, nil
	}
	return Throwaway{}, errors.New("vayumail: no free address was found; try again")
}

// Throwaways is the mailbox's throwaway addresses, newest first.
func (e *Engine) Throwaways(rd Reader) ([]Throwaway, error) {
	if err := e.readAuthorised(rd); err != nil {
		return nil, err
	}
	if e.accounts == nil || e.accounts.db == nil {
		return nil, nil
	}
	rows, err := e.accounts.db.QueryContext(context.Background(),
		`SELECT alias, label, received, last_received, off, created_at FROM vayumail_aliases WHERE target=? AND kind=? ORDER BY created_at DESC`,
		e.mailboxAddr(rd), throwawayKind)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Throwaway
	for rows.Next() {
		var t Throwaway
		var last *time.Time
		if err := rows.Scan(&t.Address, &t.Label, &t.Received, &last, &t.Off, &t.Created); err != nil {
			return nil, err
		}
		if last != nil {
			t.LastReceived = *last
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// SetThrowawayOff turns the mailbox's throwaway address addr off or on. An
// address that is not one of this mailbox's throwaways is not touched.
func (e *Engine) SetThrowawayOff(rd Reader, addr string, off bool) error {
	return e.throwawayExec(rd, `UPDATE vayumail_aliases SET off=? WHERE alias=? AND target=? AND kind=?`, off, normEmail(addr))
}

// RemoveThrowaway deletes the mailbox's throwaway address addr; the address
// then takes no mail.
func (e *Engine) RemoveThrowaway(rd Reader, addr string) error {
	return e.throwawayExec(rd, `DELETE FROM vayumail_aliases WHERE alias=? AND target=? AND kind=?`, normEmail(addr))
}

// ErrAddressOff refuses mail for an address its holder turned off.
var ErrAddressOff = errors.New("vayumail: that address is turned off")

// ErrNotYourThrowaway refuses an address that is not one of the mailbox's
// throwaway addresses.
var ErrNotYourThrowaway = errors.New("vayumail: that is not one of this mailbox's throwaway addresses")

func (e *Engine) throwawayExec(rd Reader, query string, args ...any) error {
	if err := e.writeAuthorised(rd); err != nil {
		return err
	}
	if e.accounts == nil || e.accounts.db == nil {
		return errors.New("vayumail: not started")
	}
	res, err := e.accounts.db.ExecContext(context.Background(), query, append(args, e.mailboxAddr(rd), throwawayKind)...)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotYourThrowaway
	}
	return nil
}

// noteThrowawayMail counts a message delivered to addr when it is a
// throwaway address, so its holder sees which ones take mail.
func (s *AccountStore) noteThrowawayMail(addr string) {
	if s.db == nil {
		return
	}
	_, _ = s.db.Exec(`UPDATE vayumail_aliases SET received=received+1, last_received=? WHERE alias=? AND kind=? AND off=0`,
		time.Now().UTC(), normEmail(addr), throwawayKind)
}
