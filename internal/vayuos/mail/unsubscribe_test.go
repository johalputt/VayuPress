// SPDX-License-Identifier: Apache-2.0

package mail

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"strings"
	"testing"

	"github.com/emersion/go-msgauth/dkim"

	"github.com/johalputt/vayupress/internal/safefetch"
)

const listMessage = "From: News <news@list.example>\r\nTo: alice@example.com\r\nSubject: Weekly\r\n" +
	"List-Unsubscribe: <https://list.example/u/123>, <mailto:leave@example.com?subject=stop>\r\n" +
	"List-Unsubscribe-Post: List-Unsubscribe=One-Click\r\n\r\nhello\r\n"

// signed signs raw for list.example over the headers named, and returns the
// signed message and a lookup that serves the key.
func signed(t *testing.T, raw string, headers []string) ([]byte, func(string) ([]string, error)) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := dkim.Sign(&out, strings.NewReader(raw), &dkim.SignOptions{Domain: "list.example", Selector: "s", Signer: priv, HeaderKeys: headers}); err != nil {
		t.Fatal(err)
	}
	txt := "v=DKIM1; k=ed25519; p=" + base64.StdEncoding.EncodeToString(pub)
	return out.Bytes(), func(name string) ([]string, error) {
		if name == "s._domainkey.list.example" {
			return []string{txt}, nil
		}
		return nil, errors.New("no such record")
	}
}

// unsubEngine is alice's engine with raw in her Inbox, a recorder for what
// it posts, and leave@example.com a local mailbox to receive a mailto.
func unsubEngine(t *testing.T, raw []byte, lookup func(string) ([]string, error), status int) (*Engine, string, *[]string) {
	t.Helper()
	e := newLoopbackEngine(t, loopbackBridge{localSet: map[string]bool{"leave@example.com": true}})
	id, err := e.maildir.DeliverTo("example.com", "alice", "Inbox", raw)
	if err != nil {
		t.Fatal(err)
	}
	var posted []string
	u := e.unsub()
	u.lookupTXT = lookup
	u.post = func(_ context.Context, url, ct string, body []byte) (*safefetch.Result, error) {
		posted = append(posted, url+" "+ct+" "+string(body))
		return &safefetch.Result{Status: status}, nil
	}
	return e, id, &posted
}

func TestTheOfferIsReadFromTheHeaders(t *testing.T) {
	o := ParseListUnsubscribe([]byte(listMessage))
	if o.OneClick != "https://list.example/u/123" || o.Mailto != "mailto:leave@example.com?subject=stop" || o.Link != o.OneClick {
		t.Fatalf("offer %+v", o)
	}
	without := strings.Replace(listMessage, "List-Unsubscribe-Post: List-Unsubscribe=One-Click\r\n", "", 1)
	if o := ParseListUnsubscribe([]byte(without)); o.OneClick != "" || o.Link == "" {
		t.Fatalf("without List-Unsubscribe-Post, offer %+v: a link to open, not one-click", o)
	}
}

// Signed over both headers, the request is posted, as RFC 8058 sets it out.
func TestOneClickIsPostedWhenSigned(t *testing.T) {
	raw, lookup := signed(t, listMessage, []string{"From", "Subject", "List-Unsubscribe", "List-Unsubscribe-Post"})
	e, id, posted := unsubEngine(t, raw, lookup, 200)
	said, err := e.Unsubscribe(context.Background(), ReadAsOwner("alice"), "Inbox", id)
	if err != nil || !strings.Contains(said, "list.example has been asked to stop") {
		t.Fatalf("unsubscribe: %q, %v", said, err)
	}
	if len(*posted) != 1 || (*posted)[0] != "https://list.example/u/123 application/x-www-form-urlencoded List-Unsubscribe=One-Click" {
		t.Fatalf("posted %v", *posted)
	}
}

// A signature that leaves List-Unsubscribe-Post out does not vouch for the
// request: nothing is posted, and the list's mailto is used instead.
func TestAnUncoveredRequestFallsBackToMail(t *testing.T) {
	raw, lookup := signed(t, listMessage, []string{"From", "Subject", "List-Unsubscribe"})
	e, id, posted := unsubEngine(t, raw, lookup, 200)
	said, err := e.Unsubscribe(context.Background(), ReadAsOwner("alice"), "Inbox", id)
	if err != nil || len(*posted) != 0 || !strings.Contains(said, "sent to leave@example.com") {
		t.Fatalf("uncovered: %q, %v, posted %v", said, err, *posted)
	}
	msgs, _ := e.maildir.ListFolder("example.com", "leave", "Inbox")
	if len(msgs) != 1 || msgs[0].Subject != "stop" {
		t.Fatalf("the mailto request: %+v", msgs)
	}
}

// Unsigned, with no mailto to fall back on, nothing is sent at all.
func TestAnUnsignedOneClickIsNotSent(t *testing.T) {
	only := strings.Replace(listMessage, ", <mailto:leave@example.com?subject=stop>", "", 1)
	e, id, posted := unsubEngine(t, []byte(only), func(string) ([]string, error) { return nil, errors.New("none") }, 200)
	if _, err := e.Unsubscribe(context.Background(), ReadAsOwner("alice"), "Inbox", id); !errors.Is(err, ErrUnsubscribeUnverified) || len(*posted) != 0 {
		t.Fatalf("unsigned: %v, posted %v", err, *posted)
	}
}

// A refusal from the list's server is said, not taken for success.
func TestARefusedOneClickIsSaid(t *testing.T) {
	raw, lookup := signed(t, listMessage, []string{"From", "List-Unsubscribe", "List-Unsubscribe-Post"})
	e, id, _ := unsubEngine(t, raw, lookup, 500)
	if _, err := e.Unsubscribe(context.Background(), ReadAsOwner("alice"), "Inbox", id); err == nil || !strings.Contains(err.Error(), "(500)") {
		t.Fatalf("a 500 answer: %v", err)
	}
}

// A read-only mailbox may not unsubscribe: it acts for the mailbox.
func TestAReadOnlyMailboxCannotUnsubscribe(t *testing.T) {
	raw, lookup := signed(t, listMessage, []string{"From", "List-Unsubscribe", "List-Unsubscribe-Post"})
	e, id, posted := unsubEngine(t, raw, lookup, 200)
	if err := e.accounts.Create(context.Background(), "alice@example.com", "x", "A", RoleReviewer); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Unsubscribe(context.Background(), ReadAsOwner("alice"), "Inbox", id); err == nil || len(*posted) != 0 {
		t.Fatalf("read-only: %v, posted %v", err, *posted)
	}
}

// A signature that no longer verifies vouches for nothing: a forwarder that
// rewrote the one-click address to its own does not get it posted.
func TestATamperedSignatureIsNotTrusted(t *testing.T) {
	raw, lookup := signed(t, listMessage, []string{"From", "Subject", "List-Unsubscribe", "List-Unsubscribe-Post"})
	raw = bytes.Replace(raw, []byte("https://list.example/u/123"), []byte("https://evil.example/u/123"), 1)
	e, id, posted := unsubEngine(t, raw, lookup, 200)
	if said, err := e.Unsubscribe(context.Background(), ReadAsOwner("alice"), "Inbox", id); err != nil || len(*posted) != 0 || !strings.Contains(said, "sent to leave@example.com") {
		t.Fatalf("tampered: %q, %v, posted %v", said, err, *posted)
	}
}

// A full mailbox sends no request by mail: its Sent copy would not fit.
func TestAFullMailboxSendsNoRequest(t *testing.T) {
	without := strings.Replace(listMessage, "List-Unsubscribe-Post: List-Unsubscribe=One-Click\r\n", "", 1)
	e, id, _ := unsubEngine(t, []byte(without), nil, 200)
	ctx := context.Background()
	if err := e.accounts.Create(ctx, "alice@example.com", "x", "A", RoleMailbox); err != nil {
		t.Fatal(err)
	}
	if _, err := e.maildir.DeliverTo("example.com", "alice", "Inbox", []byte("Subject: big\r\n\r\n"+strings.Repeat("x", 4096))); err != nil {
		t.Fatal(err)
	}
	if err := e.accounts.SetQuota(ctx, "alice@example.com", 1024); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Unsubscribe(ctx, ReadAsOwner("alice"), "Inbox", id); err == nil || !strings.Contains(err.Error(), "mailbox is full") || count(t, e, "leave", "Inbox") != 0 {
		t.Fatalf("full: %v", err)
	}
}
