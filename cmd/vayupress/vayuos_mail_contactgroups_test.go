// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"context"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/users"
)

func groupsAction(a *App, u *users.User, vals url.Values) *httptest.ResponseRecorder {
	req := withUser(httptest.NewRequest(http.MethodPost, "/os/vayumail/contacts/groups", strings.NewReader(vals.Encode())), u)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	a.handleVayuOSContactGroups(rec, req)
	return rec
}

func importVCard(a *App, field, name, body string) *httptest.ResponseRecorder {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("user", "dana")
	if field != "" {
		fw, _ := mw.CreateFormFile(field, name)
		_, _ = fw.Write([]byte(body))
	}
	_ = mw.Close()
	req := withUser(httptest.NewRequest(http.MethodPost, "/os/vayumail/contacts/import", &buf), danaHolder())
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rec := httptest.NewRecorder()
	a.handleVayuOSContactsImport(rec, req)
	return rec
}

func groupNames(t *testing.T, a *App) []string {
	t.Helper()
	groups, err := a.vayuMail.Accounts().ContactGroups(context.Background(), "dana@example.com")
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, g := range groups {
		out = append(out, g.Name+"="+strings.Join(g.Members, ","))
	}
	return out
}

// Save makes a group and says so beside it; saving it under another name
// renames it; Delete takes it off; a refusal is said in words.
func TestContactGroupsFromThePanel(t *testing.T) {
	a := scheduledApp(t)
	body := groupsAction(a, danaHolder(), url.Values{"action": {"save"}, "name": {" Team "}, "members": {"a@x.com, b@x.com\nc@x.com;"}}).Body.String()
	if !strings.Contains(body, `<p class="vm-keys-said" role="status">Saved Team.</p>`) || !strings.Contains(body, `<span class="vm-contact-name">Team</span><span class="vm-contact-mail">3 people</span>`) {
		t.Fatalf("save:\n%s", body)
	}
	if got := strings.Join(groupNames(t, a), " "); got != "Team=a@x.com,b@x.com,c@x.com" {
		t.Fatalf("kept %q", got)
	}
	groupsAction(a, danaHolder(), url.Values{"action": {"save"}, "was": {"Team"}, "name": {"Crew"}, "members": {"a@x.com"}})
	if got := strings.Join(groupNames(t, a), " "); got != "Crew=a@x.com" {
		t.Fatalf("after renaming: %q", got)
	}
	body = groupsAction(a, danaHolder(), url.Values{"action": {"delete"}, "was": {"Crew"}}).Body.String()
	if len(groupNames(t, a)) != 0 || !strings.Contains(body, "Deleted Crew; its people are still in your contacts.") {
		t.Fatalf("delete: %v\n%s", groupNames(t, a), body)
	}
	body = groupsAction(a, danaHolder(), url.Values{"action": {"save"}, "name": {"Team"}, "members": {"bob"}}).Body.String()
	if !strings.Contains(body, "Not saved: &#34;bob&#34; is not an email address.") {
		t.Fatalf("a refused save:\n%s", body)
	}
	if rec := groupsAction(a, danaHolder(), url.Values{"action": {"keep"}}); rec.Code != http.StatusBadRequest {
		t.Fatalf("an unknown action: %d", rec.Code)
	}
	if rec := groupsAction(a, &users.User{ID: "u-x", Email: "x@example.com", Role: users.RoleAuthor}, url.Values{"action": {"save"}, "name": {"T"}, "members": {"a@x.com"}}); rec.Code != http.StatusForbidden {
		t.Fatalf("someone with no mailbox: %d", rec.Code)
	}
}

// Compose offers a group by its name, carrying its people for the field to
// put in.
func TestComposeOffersAGroup(t *testing.T) {
	a := scheduledApp(t)
	groupsAction(a, danaHolder(), url.Values{"action": {"save"}, "name": {"Team"}, "members": {"a@x.com b@x.com"}})
	list := a.composeContactsDatalistFor(context.Background(), "dana@example.com")
	if !strings.Contains(list, `<option value="Team" label="Group · 2 people" data-group="a@x.com,b@x.com">`) {
		t.Fatalf("no group offered:\n%s", list)
	}
}

// An import says what it did under the panel's heading, one seed per
// outcome.
func TestAVCardImport(t *testing.T) {
	a := scheduledApp(t)
	card := "BEGIN:VCARD\nFN:Priya\nEMAIL:p@h.test\nEMAIL:nope\nCATEGORIES:Team,bad@name\nEND:VCARD\nBEGIN:VCARD\nFN:Ops\nEMAIL:o@h.test\nEND:VCARD\n"
	for name, c := range map[string]struct{ field, body, want string }{
		"imported":   {"file", card, "Imported 2 addresses. 1 passed over: not an address, or none on the card. Groups not made, for a name other than letters, digits, spaces, &#39;-&#39; and &#39;_&#39;, or past 50 groups or 500 people: bad@name."},
		"not vCards": {"file", "hello", "Not imported: there is no vCard in that file."},
		"no file":    {"", "", "Not imported: no file was chosen."},
	} {
		if body := importVCard(a, c.field, "c.vcf", c.body).Body.String(); !strings.Contains(body, `<p class="vm-keys-said" role="status">`+c.want+`</p>`) {
			t.Errorf("%s:\n%s", name, body)
		}
	}
	if got := strings.Join(groupNames(t, a), " "); got != "Team=p@h.test" {
		t.Fatalf("groups after the import: %q", got)
	}
	if rec := importVCard(a, "file", "big.vcf", strings.Repeat("x", maxVCardFile+(64<<10))); rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("a file past the limit: %d", rec.Code)
	}
}

// Export downloads the address book as vCards, with its groups.
func TestAVCardExport(t *testing.T) {
	a := scheduledApp(t)
	groupsAction(a, danaHolder(), url.Values{"action": {"save"}, "name": {"Team"}, "members": {"a@x.com"}})
	rec := httptest.NewRecorder()
	a.handleVayuOSContactsExport(rec, withUser(httptest.NewRequest(http.MethodGet, "/os/vayumail/contacts/export?user=dana", nil), danaHolder()))
	want := "BEGIN:VCARD\r\nVERSION:3.0\r\nFN:a@x.com\r\nN:;a@x.com;;;\r\nEMAIL;TYPE=INTERNET:a@x.com\r\nCATEGORIES:Team\r\nEND:VCARD\r\n"
	if rec.Code != http.StatusOK || rec.Body.String() != want || rec.Header().Get("Content-Type") != "text/vcard; charset=utf-8" ||
		rec.Header().Get("Content-Disposition") != `attachment; filename="contacts.vcf"` {
		t.Fatalf("%d %v\n%q", rec.Code, rec.Header(), rec.Body.String())
	}
	panel := a.vayuContactsPanel(withUser(httptest.NewRequest(http.MethodGet, "/", nil), danaHolder()), "dana@example.com", "dana")
	if !strings.Contains(panel, `href="/os/vayumail/contacts/export?user=dana" download>Export as vCard</a>`) || !strings.Contains(panel, `hx-post="/os/vayumail/contacts/import"`) {
		t.Fatalf("the panel has no Export or Import:\n%s", panel)
	}
}
