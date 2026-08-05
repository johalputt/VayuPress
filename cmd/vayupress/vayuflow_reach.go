// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	vmail "github.com/johalputt/vayupress/internal/vayuos/mail"

	"github.com/johalputt/vayupress/internal/safefetch"
)

// flowFetcher performs a flow's outbound request through the GUARDED client.
//
// It builds a safefetch client rather than an http.Client, and that is the
// whole point: safefetch carries the SSRF defences, the byte cap, the scheme
// allow-list and the Tor egress kill-switch. An automation reaching the network
// through anything else would be a hole in every one of those at once.
type flowFetcher struct{}

func (flowFetcher) Fetch(ctx context.Context, rawURL string) (string, error) {
	res, err := safefetch.New(safefetch.Options{
		MaxBytes: 64 << 10,
		Timeout:  15 * time.Second,
		// http is permitted alongside https because a flow may legitimately
		// fetch from a loopback service; safefetch still refuses the private
		// ranges it is configured to refuse.
		AllowedSchemes: []string{"http", "https"},
		UserAgent:      "VayuPress-VayuFlow/1.0",
	}).Get(ctx, rawURL)
	if err != nil {
		return "", err
	}
	if res.Status < 200 || res.Status > 299 {
		// A non-2xx is a failure rather than a body to hand on. Passing an
		// error page into the next step as though it were content is how an
		// automation writes a draft containing someone's 404.
		return "", fmt.Errorf("vayuflow: fetch returned status %d", res.Status)
	}
	return string(res.Body), nil
}

// flowMailer delivers a flow's message through the built-in sender.
//
// The from-address comes from the app's existing transactionalFrom rather than
// a second lookup of its own. Two rules for "who is this from" is two things
// that can disagree, and the visible symptom would be automated mail arriving
// with a different identity from every other message the install sends.
type flowMailer struct {
	eng  *vmail.Engine
	from func() string
}

func (m flowMailer) Send(ctx context.Context, to, subject, body string) error {
	if m.eng == nil {
		return fmt.Errorf("vayuflow: the mail engine is not running on this install")
	}
	if m.from == nil {
		return fmt.Errorf("vayuflow: no sender address is available")
	}
	from := strings.TrimSpace(m.from())
	if from == "" {
		// Refusing beats guessing. A message sent from an address the operator
		// did not configure is one their recipients will not recognise and
		// their domain has not authorised.
		return fmt.Errorf("vayuflow: no sender address is configured for this install")
	}
	_, err := m.eng.SendSystemMail(ctx, from, []string{to}, subject, "", body)
	return err
}
