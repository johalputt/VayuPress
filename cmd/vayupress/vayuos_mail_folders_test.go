// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	vmail "github.com/johalputt/vayupress/internal/vayuos/mail"
)

func postFolderAction(a *App, vals url.Values) *httptest.ResponseRecorder {
	req := withUser(httptest.NewRequest(http.MethodPost, "/os/vayumail/folders/action", strings.NewReader(vals.Encode())), danaHolder())
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	a.handleVayuOSFolderAction(rec, req)
	return rec
}

func danaReader(a *App) vmail.Reader {
	return a.mailReader(withUser(httptest.NewRequest(http.MethodGet, "/", nil), danaHolder()), "")
}

// A folder made here is in the sidebar and in every Move menu, and opens.
func TestAFolderOfYourOwnIsOfferedEverywhereMailMoves(t *testing.T) {
	a := scheduledApp(t)
	rec := postFolderAction(a, url.Values{"action": {"create"}, "name": {" Projects "}})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Header().Get("HX-Push-Url"), "folder=Projects") {
		t.Fatalf("create: %d, pushed %q", rec.Code, rec.Header().Get("HX-Push-Url"))
	}
	rd := danaReader(a)
	if nav := a.mailNavFor(rd, "Inbox", "", nil, false); !strings.Contains(nav, "folder=Projects") || !strings.Contains(nav, ">Projects<") {
		t.Fatalf("the sidebar does not list Projects:\n%s", nav)
	}
	inbox, _ := a.vayuInboxBody(rd, "Inbox", "", 0, "")
	if !strings.Contains(inbox, `{"action":"move","to":"Projects"}`) {
		t.Fatal("the selection's Move menu does not offer Projects")
	}
	if got := a.moveTargets(rd, "Projects"); strings.Contains(strings.Join(got, ","), "Projects") || strings.Contains(strings.Join(got, ","), "Snoozed") {
		t.Fatalf("moving out of Projects offers %v", got)
	}
	// Its own header carries Rename and Delete; a standard folder's does not.
	if body, _ := a.vayuInboxBody(rd, "Projects", "", 0, ""); !strings.Contains(body, `aria-label="Folder options"`) {
		t.Fatal("Projects has no folder menu")
	}
	if strings.Contains(inbox, `aria-label="Folder options"`) {
		t.Fatal("Inbox offers a folder menu")
	}
}

// A refused name is said, and nothing is made.
func TestARefusedFolderNameIsSaid(t *testing.T) {
	a := scheduledApp(t)
	rec := postFolderAction(a, url.Values{"action": {"create"}, "name": {"a.b"}, "folder": {"Sent"}})
	if !strings.Contains(rec.Body.String(), "Not done: a folder name is") || rec.Header().Get("HX-Push-Url") != "" {
		t.Fatalf("a refused name: pushed %q\n%s", rec.Header().Get("HX-Push-Url"), rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `<h2 class="mx-head__title">Sent</h2>`) {
		t.Fatal("a refused create left the folder that was open")
	}
	if own := a.ownFolders(danaReader(a)); len(own) != 0 {
		t.Fatalf("a refused name made %v", own)
	}
}

// Rename keeps the folder's mail and opens it; Delete files the mail in Trash
// and opens Inbox.
func TestRenameAndDeleteAFolder(t *testing.T) {
	a := scheduledApp(t)
	rd := danaReader(a)
	if err := a.vayuMail.CreateFolder(rd, "Old"); err != nil {
		t.Fatal(err)
	}
	inbox, _ := a.vayuMail.ListFolder(rd, "Inbox")
	if err := a.vayuMail.MoveMessage(rd, inbox[0].ID, "Inbox", "Old"); err != nil {
		t.Fatal(err)
	}
	rec := postFolderAction(a, url.Values{"action": {"rename"}, "folder": {"Old"}, "name": {"Kept"}})
	if !strings.Contains(rec.Header().Get("HX-Push-Url"), "folder=Kept") {
		t.Fatalf("rename pushed %q:\n%s", rec.Header().Get("HX-Push-Url"), rec.Body.String())
	}
	if kept, _ := a.vayuMail.ListFolder(rd, "Kept"); len(kept) != 1 {
		t.Fatalf("Kept holds %d after the rename", len(kept))
	}
	rec = postFolderAction(a, url.Values{"action": {"delete"}, "folder": {"Kept"}})
	if !strings.Contains(rec.Header().Get("HX-Push-Url"), "folder=Inbox") {
		t.Fatalf("delete pushed %q", rec.Header().Get("HX-Push-Url"))
	}
	if trash, _ := a.vayuMail.ListFolder(rd, "Trash"); len(trash) != 1 {
		t.Fatalf("Trash holds %d after deleting Kept", len(trash))
	}
}

// A holder's folder actions reach their own mailbox, whatever mailbox the
// request names.
func TestFolderActionsReachTheHoldersOwnMailbox(t *testing.T) {
	a := scheduledApp(t)
	postFolderAction(a, url.Values{"action": {"create"}, "name": {"Mine"}, "user": {"erin@example.com"}})
	if own := a.ownFolders(danaReader(a)); len(own) != 1 || own[0] != "Mine" {
		t.Fatalf("dana's own folders: %v, want Mine", own)
	}
}

// A read-only mailbox is offered no New folder and no folder menu, and the
// engine refuses the action.
func TestAReadOnlyMailboxIsNotOfferedFolders(t *testing.T) {
	a, holder, _ := reviewerApp(t, vmail.RoleReviewer)
	rd := a.mailReader(withUser(httptest.NewRequest(http.MethodGet, "/", nil), holder), "")
	if nav := a.mailNavFor(rd, "Inbox", "", nil, false); strings.Contains(nav, "New folder") {
		t.Fatal("a read-only mailbox is offered New folder")
	}
	if err := a.vayuMail.CreateFolder(rd, "Nope"); err == nil {
		t.Fatal("a read-only mailbox made a folder")
	}
}

// A studio client's Mail has no sections, so the shell drew no sidebar and
// their Sent, Drafts, Scheduled and own folders could be reached by link
// alone. An open mailbox now has its folders whoever holds it.
func TestAClientsOpenMailboxHasItsFolders(t *testing.T) {
	a := resetSessionApp(t)
	req := withUser(httptest.NewRequest(http.MethodGet, "/os/vayumail/inbox", nil), boundClient())
	req = req.WithContext(context.WithValue(req.Context(), ctxAccessKey, accessMailOnly))
	rec := httptest.NewRecorder()
	a.handleVayuOSInbox(rec, req)
	if body := rec.Body.String(); !strings.Contains(body, `id="vm-folders"`) || !strings.Contains(body, "folder=Sent") {
		t.Fatal("a client's open mailbox draws no folders")
	}
}
