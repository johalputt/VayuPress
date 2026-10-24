// SPDX-License-Identifier: Apache-2.0

package main

// account_lockout.go — what the console does around the account lockout
// (auth.CheckAccountLockout): tell the owner, and let the operator lift one.

import (
	"context"
	"html"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/johalputt/vayupress/internal/auth"
	"github.com/johalputt/vayupress/internal/config"
	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/email"
)

// accountExistsHere reports whether account is a console user or a mailbox
// on this install. Only such an account is told of its lockout: a lockout of
// any other address would otherwise make the install mail a stranger.
func (a *App) accountExistsHere(ctx context.Context, account string) bool {
	if a.userStore != nil {
		if u, err := a.userStore.GetByEmail(ctx, account); err == nil && u != nil {
			return true
		}
	}
	return a.vayuMail != nil && a.vayuMail.Accounts() != nil && a.vayuMail.Accounts().Exists(ctx, account)
}

// notifyAccountLocked tells an account's owner it was locked: at the
// account's own address and its confirmed backup address, with until when
// and where the attempts came from. It runs off the sign-in that locked it,
// so the attacker's request is not kept waiting on mail.
func (a *App) notifyAccountLocked(account string, until time.Time, sources []string) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		dbpkg.AuditLog("account.locked", "system", account, "until="+until.UTC().Format(time.RFC3339)+" from="+strings.Join(sources, ", "))
		if a.mailer == nil || !a.accountExistsHere(ctx, account) {
			return
		}
		to := []string{account}
		if a.vayuMail != nil && a.vayuMail.Accounts() != nil {
			if c := a.vayuMail.Accounts().RecoveryContact(ctx, account); c != "" && !strings.EqualFold(c, account) {
				to = append(to, c)
			}
		}
		from := "an address we could not see"
		if len(sources) > 0 {
			from = strings.Join(sources, ", ")
		}
		text := "There were too many failed attempts to sign in to " + account + ", so new sign-ins to it are paused until " +
			config.FormatSite(until, "15:04 MST on 2 January") + ".\n\n" +
			"The attempts came from: " + from + ".\n\n" +
			"If this was you, wait until then and sign in as usual. If it was not, the pause has stopped the guessing; " +
			"you are still signed in wherever you already were, and nothing else has changed. Changing your password is a good idea.\n\n" +
			"https://" + config.Cfg.Domain + "/os/login\n"
		for _, t := range to {
			_ = a.mailer.Send(email.Message{To: t, Subject: "Sign-ins to your account are paused for a while", Text: text})
		}
	}()
}

// handleOSAccountUnlock lifts one account's lockout, for the operator, and
// records it.
func (a *App) handleOSAccountUnlock(w http.ResponseWriter, r *http.Request) {
	if r.PostFormValue("all") == "1" {
		auth.LiftAllAccountLockouts()
		dbpkg.AuditLog("account.unlock", dbpkg.AuditActor(r), "*", "every account")
		w.Header().Set("HX-Refresh", "true")
		writeJSON(w, r, http.StatusOK, map[string]string{"status": "ok"})
		return
	}
	account := auth.AccountKey(r.PostFormValue("account"))
	if account == "" {
		writeAPIError(w, r, http.StatusBadRequest, "bad-request", "Name the account to unlock.", "")
		return
	}
	auth.LiftAccountLockout(account)
	dbpkg.AuditLog("account.unlock", dbpkg.AuditActor(r), account, "")
	w.Header().Set("HX-Refresh", "true")
	writeJSON(w, r, http.StatusOK, map[string]string{"status": "ok"})
}

// lockedAccountsRows lists the accounts locked now, each with until when and
// a control to lift it, or "" when none is.
func lockedAccountsRows() string {
	locked := auth.LockedAccounts()
	if len(locked) == 0 {
		return ""
	}
	names := make([]string, 0, len(locked))
	for k := range locked {
		names = append(names, k)
	}
	sort.Strings(names)
	var b strings.Builder
	b.WriteString(`<ul class="vs-locked">`)
	for _, k := range names {
		b.WriteString(`<li><code>` + html.EscapeString(k) + `</code> until ` + html.EscapeString(config.FormatSite(locked[k], "15:04 MST")) +
			` <button type="button" class="btn btn--ghost btn--sm" hx-post="/os/api/accounts/unlock" hx-swap="none"` +
			` hx-vals='{"account":"` + html.EscapeString(strings.ReplaceAll(k, `'`, ``)) + `"}'` +
			` hx-confirm="Let sign-ins to this account through now?">Lift</button></li>`)
	}
	b.WriteString(`</ul>`)
	if len(names) > 1 {
		b.WriteString(`<button type="button" class="btn btn--ghost btn--sm" hx-post="/os/api/accounts/unlock" hx-swap="none" hx-vals='{"all":"1"}'` +
			` hx-confirm="Let sign-ins to every paused account through now?">Lift all</button>`)
	}
	return b.String()
}
