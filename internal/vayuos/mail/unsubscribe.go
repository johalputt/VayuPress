// SPDX-License-Identifier: Apache-2.0

package mail

import (
	"bytes"
	"context"
	"errors"
	"net/mail"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/emersion/go-msgauth/dkim"

	"github.com/johalputt/vayupress/internal/safefetch"
)

// Unsubscribe: leaving a mailing list from the message, as RFC 2369 and
// RFC 8058 describe it.
//
// A one-click request (an https List-Unsubscribe with List-Unsubscribe-Post)
// is posted by this server, but only when a DKIM signature that verifies
// covers both headers, which RFC 8058 section 4 requires: otherwise anyone
// could forge a message that makes this server post wherever they like. A
// mailto: request is sent as mail from the mailbox. Anything else is a page
// for the person to open themselves.

// UnsubscribeOffer is what a message offers for leaving its list.
type UnsubscribeOffer struct {
	OneClick string // https URL to POST to (List-Unsubscribe-Post present)
	Mailto   string // mailto: URL
	Link     string // https URL to open
}

// Any reports whether the message offers a way off its list.
func (o UnsubscribeOffer) Any() bool { return o.OneClick != "" || o.Mailto != "" || o.Link != "" }

// ParseListUnsubscribe reads a message's List-Unsubscribe offer.
func ParseListUnsubscribe(raw []byte) UnsubscribeOffer {
	var o UnsubscribeOffer
	m, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		return o
	}
	oneClick := strings.EqualFold(strings.TrimSpace(m.Header.Get("List-Unsubscribe-Post")), "List-Unsubscribe=One-Click")
	for _, part := range strings.Split(m.Header.Get("List-Unsubscribe"), ",") {
		part = strings.TrimSpace(part)
		if !strings.HasPrefix(part, "<") || !strings.HasSuffix(part, ">") {
			continue
		}
		u, err := url.Parse(strings.TrimSpace(part[1 : len(part)-1]))
		if err != nil {
			continue
		}
		switch strings.ToLower(u.Scheme) {
		case "https":
			if o.Link == "" {
				o.Link = u.String()
			}
			if oneClick && o.OneClick == "" {
				o.OneClick = u.String()
			}
		case "mailto":
			if o.Mailto == "" && u.Opaque != "" {
				o.Mailto = u.String()
			}
		}
	}
	return o
}

// unsubscriber holds what Unsubscribe reaches out with, which a test points
// elsewhere.
type unsubscriber struct {
	post      func(ctx context.Context, url, contentType string, body []byte) (*safefetch.Result, error)
	lookupTXT func(string) ([]string, error)
}

func (e *Engine) unsub() *unsubscriber {
	e.unsubOnce.Do(func() {
		e.unsubscriber = &unsubscriber{
			post: safefetch.New(safefetch.Options{MaxBytes: 64 << 10, Timeout: 15 * time.Second}).Post,
		}
	})
	return e.unsubscriber
}

// ErrUnsubscribeUnverified is a one-click request no DKIM signature vouches
// for, with no mailto: to fall back on.
var ErrUnsubscribeUnverified = errors.New("the sender's request to leave could not be verified, so it was not sent from here; open their page to unsubscribe")

// Unsubscribe leaves the list of message id in rd's mailbox, and says how.
func (e *Engine) Unsubscribe(ctx context.Context, rd Reader, folder, id string) (string, error) {
	if err := e.writeAuthorised(rd); err != nil {
		return "", err
	}
	raw, err := e.ReadFolderMessageStored(rd, folder, id)
	if err != nil {
		return "", err
	}
	o := ParseListUnsubscribe(raw)
	if o.OneClick != "" && e.listUnsubscribeSigned(raw) {
		res, err := e.unsub().post(ctx, o.OneClick, "application/x-www-form-urlencoded", []byte("List-Unsubscribe=One-Click"))
		if err != nil {
			return "", err
		}
		if res.Status < 200 || res.Status > 299 {
			return "", errors.New("the sender's server refused it (" + strconv.Itoa(res.Status) + ")")
		}
		u, _ := url.Parse(o.OneClick)
		return "Unsubscribed: " + u.Hostname() + " has been asked to stop.", nil
	}
	if o.Mailto != "" {
		return e.unsubscribeByMail(ctx, rd, o.Mailto)
	}
	if o.OneClick != "" {
		return "", ErrUnsubscribeUnverified
	}
	return "", errors.New("this message offers no way to leave its list from here")
}

// listUnsubscribeSigned reports whether a DKIM signature that verifies covers
// List-Unsubscribe and List-Unsubscribe-Post (RFC 8058 section 4).
func (e *Engine) listUnsubscribeSigned(raw []byte) bool {
	verifs, err := dkim.VerifyWithOptions(bytes.NewReader(raw), &dkim.VerifyOptions{
		LookupTXT:        e.unsub().lookupTXT,
		MaxVerifications: maxDKIMVerifications,
	})
	if err != nil {
		return false
	}
	for _, v := range verifs {
		if v.Err != nil {
			continue
		}
		covered := map[string]bool{}
		for _, h := range v.HeaderKeys {
			covered[strings.ToLower(h)] = true
		}
		if covered["list-unsubscribe"] && covered["list-unsubscribe-post"] {
			return true
		}
	}
	return false
}

// unsubscribeByMail sends the list's mailto: request from the mailbox.
func (e *Engine) unsubscribeByMail(ctx context.Context, rd Reader, mailto string) (string, error) {
	u, err := url.Parse(mailto)
	if err != nil {
		return "", err
	}
	to, err := mail.ParseAddress(u.Opaque)
	if err != nil {
		return "", errors.New("the list's unsubscribe address cannot be read")
	}
	subject, body := u.Query().Get("subject"), u.Query().Get("body")
	if strings.TrimSpace(subject) == "" {
		subject = "unsubscribe"
	}
	if strings.TrimSpace(body) == "" {
		body = "unsubscribe"
	}
	from := e.mailboxAddr(rd)
	if e.MailboxOverQuota(from) {
		return "", errors.New("this mailbox is full, so the request cannot be sent")
	}
	if _, err := e.ComposeRich(ctx, ComposeMessage{From: from, To: []string{to.Address}, Subject: subject, Body: body}); err != nil {
		return "", err
	}
	return "Unsubscribed: a request to leave was sent to " + to.Address + ".", nil
}
