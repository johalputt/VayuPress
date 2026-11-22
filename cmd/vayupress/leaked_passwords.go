// SPDX-License-Identifier: Apache-2.0

package main

// leaked_passwords.go — refusing a new password that is already in Have I
// Been Pwned's corpus of breached passwords, when the operator asks for it
// (Shield › Settings).
//
// A password from a breach is the first one a credential-stuffing list
// tries, whatever its length. Only the first five characters of its SHA-1
// leave the server (internal/pwned). The check fails open: an unreachable
// API, a timeout, an onion-only install (safefetch refuses the clearnet
// there) lets the password through, because a sign-up that waits on a
// third party's uptime is a sign-up the operator cannot rely on; the
// length rule each caller already has still holds.

import (
	"context"
	"fmt"
	"time"

	"github.com/johalputt/vayupress/internal/i18n"
	"github.com/johalputt/vayupress/internal/logging"
	"github.com/johalputt/vayupress/internal/pwned"
	"github.com/johalputt/vayupress/internal/safefetch"
	"github.com/johalputt/vayupress/internal/settings"
)

// pwnedRange fetches the range answer for a prefix. A variable so tests
// answer it: the real API is not reachable from a test, and safefetch
// refuses the loopback a test server listens on.
var pwnedRange = func(ctx context.Context, prefix string) ([]byte, error) {
	res, err := pwnedFetcher.Get(ctx, pwned.RangeURL+prefix)
	if err != nil {
		return nil, err
	}
	if res.Status != 200 {
		return nil, fmt.Errorf("the range API answered %d", res.Status)
	}
	return res.Body, nil
}

// An answer is some hundreds of 40-byte lines (about 40 KB, padded).
var pwnedFetcher = safefetch.New(safefetch.Options{
	MaxBytes:       1 << 20,
	Timeout:        3 * time.Second,
	AllowedSchemes: []string{"https"},
})

// leakedPasswordsOn reports whether the operator switched the check on.
func (a *App) leakedPasswordsOn(ctx context.Context) bool {
	return a.siteSettings != nil && a.siteSettings.Get(ctx, settings.ForPrimary(), settings.KeyShieldLeakedPasswords) == "on"
}

// leakedPassword is the refusal for a new password found in a breach, in
// the reader's language, or "" when it may be used.
func (a *App) leakedPassword(ctx context.Context, password string) string {
	if password == "" || !a.leakedPasswordsOn(ctx) {
		return ""
	}
	prefix, suffix := pwned.Split(password)
	answer, err := pwnedRange(ctx, prefix)
	if err != nil {
		logging.LogWarn("vayushield", "the breached-password check could not be made, so the password was allowed: "+err.Error())
		return ""
	}
	if !pwned.Found(answer, suffix) {
		return ""
	}
	return i18n.T(i18n.Lang(ctx), "This password is in known data breaches, so it is among the first an attacker tries. Choose another.")
}
