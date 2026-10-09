// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/users"
)

func templatesAction(t *testing.T, a *App, u *users.User, vals url.Values) (int, templatesAnswer) {
	t.Helper()
	req := withUser(httptest.NewRequest(http.MethodPost, "/os/vayumail/templates/action", strings.NewReader(vals.Encode())), u)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	a.handleVayuOSTemplatesAction(rec, req)
	var ans templatesAnswer
	_ = json.Unmarshal(rec.Body.Bytes(), &ans)
	return rec.Code, ans
}

func templatesList(t *testing.T, a *App, u *users.User, user string) templatesAnswer {
	t.Helper()
	rec := httptest.NewRecorder()
	a.handleVayuOSTemplates(rec, withUser(httptest.NewRequest(http.MethodGet, "/os/vayumail/templates?user="+url.QueryEscape(user), nil), u))
	var ans templatesAnswer
	if err := json.Unmarshal(rec.Body.Bytes(), &ans); err != nil {
		t.Fatalf("list: %d %s", rec.Code, rec.Body.String())
	}
	return ans
}

// Save answers with the list; a refusal answers 400 in words, with the list
// still there; delete takes one off.
func TestTemplatesFromCompose(t *testing.T) {
	a := scheduledApp(t)
	code, ans := templatesAction(t, a, danaHolder(), url.Values{"action": {"save"}, "name": {"Thanks"}, "subject": {"Thank you"}, "body": {"Thanks for this."}})
	if code != http.StatusOK || len(ans.Templates) != 1 || ans.Templates[0].Body != "Thanks for this." {
		t.Fatalf("save: %d %+v", code, ans)
	}
	code, ans = templatesAction(t, a, danaHolder(), url.Values{"action": {"save"}, "name": {""}, "body": {"x"}})
	if code != http.StatusBadRequest || !strings.Contains(ans.Error, "needs a name") || len(ans.Templates) != 1 {
		t.Fatalf("a refused save: %d %+v", code, ans)
	}
	id := ans.Templates[0].ID
	code, ans = templatesAction(t, a, danaHolder(), url.Values{"action": {"delete"}, "id": {itoaSafe(int(id))}})
	if code != http.StatusOK || len(ans.Templates) != 0 {
		t.Fatalf("delete: %d %+v", code, ans)
	}
}

// A holder naming another mailbox reaches their own (mailReader): erin's
// templates are neither listed to dana nor written by her.
func TestTemplatesAreTheHoldersOwn(t *testing.T) {
	a := scheduledApp(t)
	templatesAction(t, a, danaHolder(), url.Values{"action": {"save"}, "user": {"erin@example.com"}, "name": {"Mine"}, "body": {"x"}})
	if ans := templatesList(t, a, operatorUser(), "erin@example.com"); len(ans.Templates) != 0 {
		t.Fatalf("dana wrote into erin's templates: %+v", ans.Templates)
	}
	if ans := templatesList(t, a, danaHolder(), "erin@example.com"); len(ans.Templates) != 1 || ans.Templates[0].Name != "Mine" {
		t.Fatalf("dana's own list: %+v", ans.Templates)
	}
}

// Compose carries the menu, and the mailbox it writes from for the menu to
// name.
func TestComposeOffersTemplates(t *testing.T) {
	a := scheduledApp(t)
	rec := httptest.NewRecorder()
	a.handleVayuOSCompose(rec, withUser(httptest.NewRequest(http.MethodGet, "/os/vayumail/compose?user=dana", nil), danaHolder()))
	body := rec.Body.String()
	if !strings.Contains(body, `data-c-tpl>`) || !strings.Contains(body, `data-c-user="dana"`) {
		t.Fatalf("compose has no Templates menu, or no mailbox for it:\n%.2000s", body)
	}
}
