// SPDX-License-Identifier: Apache-2.0

package main

// vayuos_mail_folders.go — the mailbox's own folders in Mail: making,
// renaming and deleting them, and offering them wherever mail can be moved.

import (
	"net/http"
	"strings"

	vmail "github.com/johalputt/vayupress/internal/vayuos/mail"
)

// mailFolderIcon is a folder's icon in menus and the sidebar.
func mailFolderIcon(f string) string {
	if icon, ok := mailFolderIcons[f]; ok {
		return icon
	}
	return "folder"
}

// moveTargets are the folders a message in folder can be moved to: every
// folder of rd's mailbox but its own and Snoozed (only the snooze action
// files there; a manual move would sleep forever with no wake row).
func (a *App) moveTargets(rd vmail.Reader, folder string) []string {
	var out []string
	for _, f := range append(append([]string{}, vmail.StandardFolders...), a.ownFolders(rd)...) {
		if !strings.EqualFold(f, folder) && !strings.EqualFold(f, "Snoozed") {
			out = append(out, f)
		}
	}
	return out
}

// ownFolderMenu is the header's menu on one of the mailbox's own folders:
// rename it, or delete it (its mail goes to Trash, which the confirm says).
func ownFolderMenu(user, folder string) string {
	post := ` hx-post="/os/vayumail/folders/action" hx-target="#vm-inbox-list" hx-swap="innerHTML" hx-indicator="#vm-inbox-spin"`
	return `<details class="sa-pop mx-head__folder"><summary class="btn btn--ghost btn--sm btn--icon" aria-label="Folder options" title="Folder options">` + saIcon("more") + `</summary><div class="sa-pop__panel">` +
		`<form class="mx-folderform"` + post + `><input type="hidden" name="user" value="` + esc(user) + `"><input type="hidden" name="folder" value="` + esc(folder) + `"><input type="hidden" name="action" value="rename">` +
		`<label class="mx-folderform__label">Rename<input class="mx-newfolder__name" name="name" value="` + esc(folder) + `" required maxlength="40" autocomplete="off"></label>` +
		`<button type="submit" class="btn btn--sm">Rename</button></form>` +
		`<button type="button" class="sa-menu__item mx-folderform__delete"` + post + ` hx-vals='{"action":"delete","user":"` + esc(user) + `","folder":"` + esc(folder) + `"}' hx-confirm="Delete the folder ` + esc(folder) + `? Its mail moves to Trash.">` + saIcon("trash") + `<span class="sa-menu__text">Delete folder</span></button>` +
		`</div></details>`
}

// handleVayuOSFolderAction makes, renames or deletes one of the mailbox's own
// folders, for the mailbox the request may change (mailReader, ADR-0152; the
// engine refuses a read-only holder), and redraws the list with the folder
// open: the new or renamed one, Inbox after a delete. A refusal is said above
// the list it leaves open.
func (a *App) handleVayuOSFolderAction(w http.ResponseWriter, r *http.Request) {
	if !a.mailRunning() {
		writeAPIError(w, r, http.StatusServiceUnavailable, "mail-disabled", "VayuMail is not active", "")
		return
	}
	rd := a.mailReader(r, strings.TrimSpace(r.PostFormValue("user")))
	if rd.Key() == "" {
		writeAPIError(w, r, http.StatusForbidden, "forbidden", "No mailbox you can change was named.", "")
		return
	}
	folder := sanitizeMailFolder(strings.TrimSpace(r.PostFormValue("folder")))
	name := strings.TrimSpace(r.PostFormValue("name"))
	var err error
	var open string
	switch r.PostFormValue("action") {
	case "create":
		err, open = a.vayuMail.CreateFolder(rd, name), name
	case "rename":
		err, open = a.vayuMail.RenameFolder(rd, folder, name), name
	case "delete":
		err, open = a.vayuMail.DeleteFolder(rd, folder), "Inbox"
	default:
		writeAPIError(w, r, http.StatusBadRequest, "bad-request", "unknown action", "")
		return
	}
	note := ""
	if err != nil {
		open, note = folder, "Not done: "+err.Error()+"."
	} else {
		open = sanitizeMailFolder(open)
		w.Header().Set("HX-Push-Url", "/os/vayumail/inbox?user="+qparam(rd.Key())+"&folder="+qparam(open))
	}
	banner := ""
	if note != "" {
		banner = `<div class="mx-banner" role="alert">` + saIcon("warn") + ` <span>` + esc(note) + `</span></div>`
	}
	writeOSFragment(w, banner+a.vayuInboxSwap(rd, open, "", 0))
}
