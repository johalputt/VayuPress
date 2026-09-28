// SPDX-License-Identifier: Apache-2.0

package mail

import (
	"context"
	"database/sql"
	"errors"
	"net"
	"net/textproto"
	"testing"
	"time"
)

// TestOnlyARefusedRecipientIsPermanent plays a relay that refuses at one stage
// each, through the real deliverer, so the error judged is the one net/smtp
// actually returns rather than one built for the test.
func TestOnlyARefusedRecipientIsPermanent(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name      string
		reply     map[string]string
		permanent bool
	}{
		{"recipient does not exist", map[string]string{"RCPT": "550 5.1.1 No such user here"}, true},
		{"recipient mailbox busy", map[string]string{"RCPT": "450 4.2.1 Try again later"}, false},
		{"sender refused", map[string]string{"MAIL": "550 5.7.1 Sender not allowed"}, false},
		{"relay password wrong", map[string]string{"AUTH": "535 5.7.8 Authentication failed"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fr := startFakeRelayReplying(t, tc.reply)
			host, port, _ := net.SplitHostPort(fr.addr)
			cfg := DefaultConfig()
			cfg.RelayHost = host
			cfg.RelayPort = atoiOrZero(port)
			cfg.RelayUsername = "apikey"
			cfg.RelayPassword = "s3cret"
			cfg.RelayRequireTLS = false
			err := NewRelayDeliverer(cfg, "mail.example.com", 5*time.Second)(
				context.Background(), "a@example.com", []string{"nobody@dest.net"}, []byte("Subject: x\r\n\r\nx\r\n"))
			if err == nil {
				t.Fatal("the relay refused and the delivery reported success")
			}
			if got := PermanentFailure(err); got != tc.permanent {
				t.Errorf("PermanentFailure(%v) = %v, want %v", err, got, tc.permanent)
			}
		})
	}
}

// TestARefusedRecipientIsNotRetried — a message to an address the receiving
// server says does not exist fails on its first attempt instead of repeating
// the refusal for days; anything else keeps its retries.
func TestARefusedRecipientIsNotRetried(t *testing.T) {
	t.Parallel()
	refused := &RecipientRefusedError{Rcpt: "x", Err: &textproto.Error{Code: 550, Msg: "5.1.1 No such user here"}}
	for _, tc := range []struct {
		name  string
		to    []string
		err   error
		state string
	}{
		{"one refused recipient", []string{"nobody@d.net"}, refused, "failed"},
		{"a refusal among several recipients", []string{"nobody@d.net", "real@d.net"}, refused, "pending"},
		{"a transient refusal", []string{"nobody@d.net"}, &RecipientRefusedError{Rcpt: "x", Err: &textproto.Error{Code: 450}}, "pending"},
		{"a connection failure", []string{"nobody@d.net"}, errors.New("dial tcp: i/o timeout"), "pending"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			db, err := sql.Open("sqlite3", ":memory:")
			if err != nil {
				t.Fatal(err)
			}
			db.SetMaxOpenConns(1)
			defer db.Close()
			cfg := DefaultConfig()
			cfg.QueueMaxAttempts = 12
			q, err := NewQueue(db, cfg, func(context.Context, string, []string, []byte) error { return tc.err })
			if err != nil {
				t.Fatal(err)
			}
			id, err := q.Enqueue(context.Background(), "a@b.com", tc.to, []byte("x"))
			if err != nil {
				t.Fatal(err)
			}
			if _, _, err := q.ProcessDue(context.Background(), time.Now()); err != nil {
				t.Fatal(err)
			}
			var state string
			var attempts int
			if err := db.QueryRow(`SELECT state, attempts FROM vayumail_queue WHERE id=?`, id).Scan(&state, &attempts); err != nil {
				t.Fatal(err)
			}
			if state != tc.state || attempts != 1 {
				t.Errorf("after one attempt: state %q, %d attempts recorded; want %q, 1", state, attempts, tc.state)
			}
		})
	}
}
