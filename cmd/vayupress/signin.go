// SPDX-License-Identifier: Apache-2.0

package main

// signin.go — the record of sign-in attempts (internal/signin) at the app's
// edges: opening it with its key, writing what it holds, telling a holder
// whose password was right but whose code was not, and the two calls every
// door makes: is this source refused, and what happened.

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/johalputt/vayupress/internal/auth"
	"github.com/johalputt/vayupress/internal/config"
	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/email"
	"github.com/johalputt/vayupress/internal/geoip"
	"github.com/johalputt/vayupress/internal/logging"
	"github.com/johalputt/vayupress/internal/signin"
)

// signinFlushEvery is how often the attempts held are written. A failure
// waits at most this long to show on the Sign-ins page; the gate and the
// alarm do not wait for it.
const signinFlushEvery = 10 * time.Second

// openSignins opens the record with its fingerprint key, which is derived
// from the secrets store's key and so is never in the database, and starts
// writing it. Without the key nothing is recorded or blocked: a fingerprint
// made with a key in the database could be reversed by trying every address.
func (a *App) openSignins() {
	if a.secrets == nil {
		return
	}
	key, err := a.secrets.Derive("signin-fingerprint-v1")
	if err != nil {
		logging.LogError("signin", "no fingerprint key; sign-in attempts are not recorded", err.Error())
		return
	}
	st, err := signin.Open(context.Background(), dbpkg.DB, key)
	if err != nil {
		logging.LogError("signin", "could not open the record of sign-in attempts", err.Error())
		return
	}
	signin.Country = geoip.Country
	st.OnAlarm = a.notifySigninAlarm
	a.signin = st
	go func() {
		t := time.NewTicker(signinFlushEvery)
		defer t.Stop()
		for range t.C {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			if err := st.Flush(ctx); err != nil {
				logging.LogError("signin", "attempts not written; kept for the next try", err.Error())
			}
			cancel()
		}
	}()
}

// flushSignins writes what the record holds, for the shutdown.
func (a *App) flushSignins(limit time.Duration) {
	if a.signin == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), limit)
	defer cancel()
	if err := a.signin.Flush(ctx); err != nil {
		logging.LogError("main", "sign-in attempts on shutdown", err.Error())
	}
}

// signinRefused reports whether a sign-in to account from ip is refused by a
// block or the account's fence, before its password is checked.
func (a *App) signinRefused(account, ip string) bool {
	if a.signin == nil {
		return false
	}
	_, refused := a.signin.Refuse(auth.AccountKey(account), ip)
	return refused
}

// signinRefusedMessage is what a refused sign-in is told. It names no rule,
// so it tells a guesser nothing about the account, and it tells a holder
// caught by their own fence where to turn.
const signinRefusedMessage = "Sign-in from here is not allowed. If this is your account, ask the site's administrator."

// signinRecord records one attempt at door.
func (a *App) signinRecord(account, door, stage, ip string) {
	if a.signin != nil {
		a.signin.Record(signin.Attempt{Account: auth.AccountKey(account), Door: door, Stage: stage, IP: ip})
	}
}

// signinIPKey carries the client's address to a door that is handed only a
// context (VayuTalk's verify).
type signinIPKey struct{}

func withSigninIP(r *http.Request) context.Context {
	return context.WithValue(r.Context(), signinIPKey{}, auth.ClientIP(r))
}

func signinIPFrom(ctx context.Context) string {
	ip, _ := ctx.Value(signinIPKey{}).(string)
	return ip
}

// signinDoorNames are the doors as the pages and the alarm name them.
var signinDoorNames = map[string]string{
	signin.DoorWeb: "the site's sign-in", signin.DoorConsole: "the console", signin.DoorIMAP: "IMAP",
	signin.DoorPOP3: "POP3", signin.DoorSMTP: "SMTP", signin.DoorDAV: "contacts and calendars",
	signin.DoorTalk: "VayuTalk", signin.DoorRecovery: "a recovery code",
}

// notifySigninAlarm tells a holder at once that their password was right and
// their second factor was not: whoever it was knows the password. It goes to
// the account's address and its confirmed recovery address, and to the
// audit log; only an account on this install is told, so a guess at a
// stranger's address never mails the stranger.
func (a *App) notifySigninAlarm(al signin.Alarm) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	where := al.Masked
	if al.Country != "" {
		where += " (" + al.Country + ")"
	}
	dbpkg.AuditLog("account.signin_alarm", "system", al.Account, "from="+where+" door="+al.Door)
	if a.mailer == nil || !a.accountExistsHere(ctx, al.Account) {
		return
	}
	to := []string{al.Account}
	if a.vayuMail != nil && a.vayuMail.Accounts() != nil {
		if c := a.vayuMail.Accounts().RecoveryContact(ctx, al.Account); c != "" && !strings.EqualFold(c, al.Account) {
			to = append(to, c)
		}
	}
	door := signinDoorNames[al.Door]
	if door == "" {
		door = al.Door
	}
	text := "Someone signed in to " + al.Account + " with the right password at " + config.FormatSite(al.At, "15:04 MST on 2 January") +
		", from " + where + " through " + door + ", and was stopped by your second factor.\n\n" +
		"If this was not you, someone knows your password. Change it now; your sessions stay signed in and nothing else has changed.\n\n" +
		"If it was you, there is nothing to do. You can say so on your Security page, and this place will not raise the alarm again.\n\n" +
		"https://" + config.Cfg.Domain + "/os/login\n"
	for _, t := range to {
		_ = a.mailer.Send(email.Message{To: t, Subject: "Your password was used, and your second factor stopped it", Text: text})
	}
}
