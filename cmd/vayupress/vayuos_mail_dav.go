// SPDX-License-Identifier: Apache-2.0

package main

// vayuos_mail_dav.go — contacts and calendar sync for a mailbox's apps
// (internal/vayuos/mail/davserver.go), signed in as the mail protocols are.

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	vmail "github.com/johalputt/vayupress/internal/vayuos/mail"
)

// The router answers only the methods it knows; CardDAV's and CalDAV's
// must be known before /dav is routed, or an app's PROPFIND is a 405.
func init() {
	for _, m := range vmail.DAVMethods() {
		chi.RegisterMethod(m)
	}
}

// handleMailDAV answers /dav and the two /.well-known addresses that lead
// to it.
func (a *App) handleMailDAV(w http.ResponseWriter, r *http.Request) {
	if !a.mailRunning() {
		http.NotFound(w, r)
		return
	}
	a.vayuMail.DAVHandler(a.davSignIn).ServeHTTP(w, r)
}

// davSignIn is the mail protocols' sign-in: AuthUser, the throttle and then
// the credential check in its mail-sync scope, which wants an approved
// device's password wherever the mailbox asks for one, as it does by
// default and always for an address with no mailbox here. A contact or an
// event can so only be kept by a credential minted for a mailbox.
func (a *App) davSignIn(user, password string) (string, bool) {
	addr := strings.ToLower(mailAddrOf(strings.TrimSpace(user), a.vayuMail.Config().Domain))
	if ok, _ := (&vayuMailBridge{app: a}).AuthUser(addr, password); !ok {
		return "", false
	}
	return addr, true
}
