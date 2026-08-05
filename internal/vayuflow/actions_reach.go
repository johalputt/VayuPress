// SPDX-License-Identifier: Apache-2.0

package vayuflow

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"sync"
)

// The two actions that reach outside this install: one fetches, one sends.
//
// They are the reason several of the contract's fields exist at all. Egress is
// the field OnionInert was written for; mail is the field Irreversible was
// written for. Until this file they were answers to questions nothing asked.

// Fetcher performs one guarded outbound request and returns the body.
//
// The implementation MUST route through safefetch, which is what makes the Tor
// kill-switch, the SSRF guards and the size limits apply. VayuFlow does not
// build its own HTTP client for the same reason it does not read VAYUOS_MODE
// itself: a second path is a second thing that can disagree with the first.
type Fetcher interface {
	Fetch(ctx context.Context, rawURL string) (string, error)
}

// MailSender delivers one message through the built-in sender.
type MailSender interface {
	Send(ctx context.Context, to, subject, body string) error
}

var (
	reachMu sync.RWMutex
	fetcher Fetcher
	mailer  MailSender
)

// SetFetcher wires guarded outbound fetching. nil detaches it.
func SetFetcher(f Fetcher) {
	reachMu.Lock()
	defer reachMu.Unlock()
	fetcher = f
}

// SetMailSender wires mail delivery. nil detaches it.
func SetMailSender(m MailSender) {
	reachMu.Lock()
	defer reachMu.Unlock()
	mailer = m
}

func currentFetcher() (Fetcher, error) {
	reachMu.RLock()
	defer reachMu.RUnlock()
	if fetcher == nil {
		return nil, fmt.Errorf("vayuflow: no guarded fetcher is wired into this build")
	}
	return fetcher, nil
}

func currentMailer() (MailSender, error) {
	reachMu.RLock()
	defer reachMu.RUnlock()
	if mailer == nil {
		return nil, fmt.Errorf("vayuflow: no mail sender is wired into this build")
	}
	return mailer, nil
}

// MaxFetchBody bounds what a fetch may hand to the next step, for the same
// reason MaxModelOutput bounds a generation: an unbounded value from outside
// this install is the most obviously attacker-controlled thing in the engine.
const MaxFetchBody = 40000

func init() {
	RegisterAction("egress.fetch", func(ctx context.Context, p map[string]string, e *Effects) (string, error) {
		raw := strings.TrimSpace(p["url"])
		if raw == "" {
			return "", fmt.Errorf("vayuflow: a fetch step needs a url")
		}
		// Parsed before anything else so a malformed value fails here rather
		// than inside a client, and so http(s) is the only scheme that reaches
		// one at all.
		u, err := url.Parse(raw)
		if err != nil {
			return "", fmt.Errorf("vayuflow: %q is not a usable url", raw)
		}
		switch strings.ToLower(u.Scheme) {
		case "http", "https":
		default:
			return "", fmt.Errorf("vayuflow: refusing scheme %q; a fetch step speaks http(s) only", u.Scheme)
		}
		// Permission before the request. Fetch is what refuses in a Tor Space
		// and charges the egress ceiling.
		if err := e.Fetch(raw); err != nil {
			return "", err
		}
		f, err := currentFetcher()
		if err != nil {
			return "", err
		}
		body, err := f.Fetch(ctx, raw)
		if err != nil {
			return "", err
		}
		if len(body) > MaxFetchBody {
			return "", fmt.Errorf("vayuflow: the response was %d bytes, above the %d limit",
				len(body), MaxFetchBody)
		}
		return body, nil
	})

	RegisterAction("mail.send", func(ctx context.Context, p map[string]string, e *Effects) (string, error) {
		to := strings.TrimSpace(p["to"])
		subject := strings.TrimSpace(p["subject"])
		body := p["body"]
		if to == "" || subject == "" {
			return "", fmt.Errorf("vayuflow: a mail step needs a recipient and a subject")
		}
		if strings.TrimSpace(body) == "" {
			return "", fmt.Errorf("vayuflow: refusing to send an empty message")
		}
		// A single recipient per step, deliberately. A comma-separated list is
		// how "send the digest to one person" becomes "mail the whole list
		// twice" without the budget noticing: one step, one write charged, many
		// deliveries. Fan-out belongs to a future action that declares it.
		if strings.ContainsAny(to, ",;") {
			return "", fmt.Errorf("vayuflow: a mail step sends to ONE recipient; " +
				"a list would spend one write and deliver many")
		}
		// WriteLive, and the ceiling is charged here — before delivery, which is
		// the only moment at which refusing still means anything.
		if err := e.Write(WriteLive, "send mail to "+to+" — "+subject); err != nil {
			return "", err
		}
		m, err := currentMailer()
		if err != nil {
			return "", err
		}
		if err := m.Send(ctx, to, subject, body); err != nil {
			return "", err
		}
		return "sent to " + to, nil
	})
}
