// SPDX-License-Identifier: Apache-2.0

package main

// vayuos_mail_templates.go — compose's Templates menu: a mailbox's saved
// subject and body (internal/vayuos/mail/templates.go), listed, saved from
// the message being written, and deleted. Each request names its mailbox
// through mailReader (ADR-0152), as every Mail request does.

import (
	"net/http"
	"strconv"
	"strings"

	vmail "github.com/johalputt/vayupress/internal/vayuos/mail"
)

// templatesAnswer is the menu's state after a request: the list, and what
// went wrong if something did.
type templatesAnswer struct {
	Templates []vmail.Template `json:"templates"`
	Error     string           `json:"error,omitempty"`
}

func (a *App) templatesReply(w http.ResponseWriter, r *http.Request, rd vmail.Reader, failed error) {
	list, err := a.vayuMail.Templates(rd)
	out := templatesAnswer{Templates: list}
	if failed != nil {
		out.Error = failed.Error()
	} else if err != nil {
		out.Error = err.Error()
	}
	if out.Templates == nil {
		out.Templates = []vmail.Template{}
	}
	code := http.StatusOK
	if out.Error != "" {
		code = http.StatusBadRequest
	}
	writeJSON(w, r, code, out)
}

// handleVayuOSTemplates lists the templates of the mailbox compose writes from.
func (a *App) handleVayuOSTemplates(w http.ResponseWriter, r *http.Request) {
	if !a.mailRunning() {
		writeAPIError(w, r, http.StatusServiceUnavailable, "mail-disabled", "VayuMail is not active", "")
		return
	}
	rd := a.mailReader(r, mailUserParam(r))
	if rd.Key() == "" {
		writeAPIError(w, r, http.StatusForbidden, "forbidden", "No mailbox here to keep templates in.", "")
		return
	}
	a.templatesReply(w, r, rd, nil)
}

// handleVayuOSTemplatesAction saves the message being written as a template,
// or deletes one, and answers with the list.
func (a *App) handleVayuOSTemplatesAction(w http.ResponseWriter, r *http.Request) {
	if !a.mailRunning() {
		writeAPIError(w, r, http.StatusServiceUnavailable, "mail-disabled", "VayuMail is not active", "")
		return
	}
	rd := a.mailReader(r, sanitizeMailUser(strings.TrimSpace(r.PostFormValue("user"))))
	if rd.Key() == "" {
		writeAPIError(w, r, http.StatusForbidden, "forbidden", "No mailbox here to keep templates in.", "")
		return
	}
	var err error
	switch r.PostFormValue("action") {
	case "save":
		err = a.vayuMail.SaveTemplate(rd, r.PostFormValue("name"), r.PostFormValue("subject"), r.PostFormValue("body"))
	case "delete":
		id, perr := strconv.ParseInt(r.PostFormValue("id"), 10, 64)
		if perr != nil {
			writeAPIError(w, r, http.StatusBadRequest, "bad-request", "no template was named", "")
			return
		}
		err = a.vayuMail.DeleteTemplate(rd, id)
	default:
		writeAPIError(w, r, http.StatusBadRequest, "bad-request", "unknown action", "")
		return
	}
	a.templatesReply(w, r, rd, err)
}
