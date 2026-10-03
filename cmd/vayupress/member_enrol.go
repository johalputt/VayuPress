// SPDX-License-Identifier: Apache-2.0

package main

// member_enrol.go — everyone who signs in with a mailbox is a member.
//
// The member portal's mailbox sign-in made the member row; the console's login
// button, which takes the same mailbox password, and the VayuMail app's device
// sign-in did not, so a mailbox holder who came in that way was signed in and
// never listed in Members (operator, 2026-10-02). Every web sign-in with a
// mailbox credential now passes through enrolMember. Mail protocols (IMAP,
// POP3, submission) are not sign-ins to the site and are left out: they
// authenticate on every connection and carry no site to attribute to.

import (
	"context"
	"database/sql"
	"net/http"
	"strings"
	"time"

	"github.com/johalputt/vayupress/internal/logging"
	"github.com/johalputt/vayupress/internal/members"
)

// enrolMember ensures the member row for a mailbox that has just signed in,
// attributed to the site it signed in on, with its coarse join location set
// once (country, region, city; never the address).
func (a *App) enrolMember(r *http.Request, email string) (*members.Member, error) {
	m, err := a.members.UpsertScoped(r.Context(), a.memberScope(r), email)
	if err != nil {
		return nil, err
	}
	geo := geoFromHeaders(r)
	a.members.SetGeoIfEmpty(r.Context(), m.ID, geo.Country, geo.Region, geo.City)
	return m, nil
}

// enrolOnSignIn is enrolMember for the sign-ins whose job is not the member
// record: a failure is logged and the sign-in goes on, since refusing a
// correct password over a listing would lock a person out of their own mail.
func (a *App) enrolOnSignIn(r *http.Request, email string) {
	if a.members == nil {
		return
	}
	if _, err := a.enrolMember(r, email); err != nil {
		logging.LogError("members", "a mailbox signed in but could not be listed in Members: "+email, err.Error())
	}
}

// enrolSignedInMailboxes lists, at each start, every mailbox with a web
// session still open, for the people who signed in before every sign-in
// enrolled them. Mailboxes keep no record of past sign-ins, and an expired
// session is deleted, so this finds the ones still signed in; anyone else is
// listed at their next sign-in. With no request to attribute them by, they
// are listed under the primary site. A deleted or disabled mailbox is skipped:
// its session no longer resolves, so it is not anyone signed in.
func (a *App) enrolSignedInMailboxes(ctx context.Context, db *sql.DB) int {
	if a.members == nil || a.vayuMail == nil || a.vayuMail.Accounts() == nil {
		return 0
	}
	rows, err := db.QueryContext(ctx, `SELECT DISTINCT user_id FROM sessions WHERE user_id LIKE 'vmail:%' AND expires_at > ?`,
		time.Now().UTC().Format("2006-01-02 15:04:05"))
	if err != nil {
		logging.LogError("members", "listing mailboxes signed in before this version", err.Error())
		return 0
	}
	var addrs []string
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			addrs = append(addrs, strings.TrimPrefix(id, "vmail:"))
		}
	}
	// A read cut short lists whom it reached; the rest are listed at their
	// next sign-in, so it is logged rather than abandoning those already read.
	if err := rows.Err(); err != nil {
		logging.LogError("members", "listing mailboxes signed in before this version", err.Error())
	}
	_ = rows.Close()
	n := 0
	for _, addr := range addrs {
		if a.vayuMail.Accounts().HashFor(ctx, addr) == "" {
			continue
		}
		if _, err := a.members.UpsertScoped(ctx, "", addr); err == nil {
			n++
		}
	}
	return n
}
