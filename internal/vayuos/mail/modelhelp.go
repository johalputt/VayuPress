// SPDX-License-Identifier: Apache-2.0

package mail

// modelhelp.go — what a mailbox allows of help from a model: the holder's
// own switch (off until they turn it on), the operator's leave for it to
// use a provider outside the server (not given until they give it), and how
// many requests went where, which the operator sees and never their text.

import (
	"context"
	"errors"
)

// ModelHelp is a mailbox's model-help state.
type ModelHelp struct {
	On, Outside bool
	Sent        int
	SentTo      string
}

// ModelHelpFor is email's model-help state.
func (s *AccountStore) ModelHelpFor(ctx context.Context, email string) ModelHelp {
	var m ModelHelp
	if s == nil || s.db == nil {
		return m
	}
	_ = s.db.QueryRowContext(ctx, `SELECT model_help, model_outside, model_sent, model_sent_to FROM vayumail_accounts WHERE email=?`, normEmail(email)).
		Scan(&m.On, &m.Outside, &m.Sent, &m.SentTo)
	return m
}

// SetModelHelp is the holder's switch.
func (s *AccountStore) SetModelHelp(ctx context.Context, email string, on bool) error {
	return s.setModelFlag(ctx, `UPDATE vayumail_accounts SET model_help=? WHERE email=?`, on, email)
}

// SetModelOutside is the operator's leave to use a provider outside the
// server for this mailbox.
func (s *AccountStore) SetModelOutside(ctx context.Context, email string, allowed bool) error {
	return s.setModelFlag(ctx, `UPDATE vayumail_accounts SET model_outside=? WHERE email=?`, allowed, email)
}

func (s *AccountStore) setModelFlag(ctx context.Context, q string, v bool, email string) error {
	if s == nil || s.db == nil {
		return errors.New("vayumail: no storage")
	}
	res, err := s.db.ExecContext(ctx, q, v, normEmail(email))
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errors.New("vayumail: no such mailbox")
	}
	return nil
}

// NoteModelSent counts one request of email's sent to where.
func (s *AccountStore) NoteModelSent(ctx context.Context, email, where string) {
	if s == nil || s.db == nil {
		return
	}
	_, _ = s.db.ExecContext(ctx, `UPDATE vayumail_accounts SET model_sent=model_sent+1, model_sent_to=? WHERE email=?`, where, normEmail(email))
}
