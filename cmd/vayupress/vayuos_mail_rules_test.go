// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/users"
)

func rulesAdmin() *users.User {
	return &users.User{ID: "admin1", Email: "boss@example.com", Role: users.RoleAdmin}
}

func postRule(a *App, vals url.Values) *httptest.ResponseRecorder {
	vals.Set("email", "dana@example.com")
	return postForm(a.handleVayuOSFilterAction, "/os/vayumail/filters/action", vals.Encode(), rulesAdmin())
}

func deliverToDana(t *testing.T, a *App, subject string) {
	t.Helper()
	if _, err := a.vayuMail.DeliverInbound("sam@shop.example", "dana@example.com",
		[]byte("From: sam@shop.example\r\nTo: dana@example.com\r\nSubject: "+subject+"\r\n\r\nx\r\n")); err != nil {
		t.Fatal(err)
	}
}

// TestARuleIsSavedFromTheEditor — two conditions on any, two actions, and
// keep going, as the editor posts them; the list says it in words.
func TestARuleIsSavedFromTheEditor(t *testing.T) {
	a := appWithMailAccounts(t)
	rec := postRule(a, url.Values{"op": {"save"}, "match": {"any"}, "keep": {"1"},
		"c_field_0": {"domain"}, "c_op_0": {"is"}, "c_value_0": {"shop.example"},
		"c_field_1": {"subject"}, "c_op_1": {"contains"}, "c_value_1": {"receipt"},
		"a_kind_0": {"label"}, "a_arg_0": {"Shop"}, "a_kind_1": {"markread"}})
	if rec.Code != http.StatusOK {
		t.Fatalf("save: %d %s", rec.Code, rec.Body.String())
	}
	rules, _ := a.vayuMail.Accounts().FiltersFor(context.Background(), "dana@example.com")
	if len(rules) != 1 || !rules[0].Any || !rules[0].KeepGoing || len(rules[0].Conditions) != 2 || len(rules[0].Actions) != 2 {
		t.Fatalf("stored %+v", rules)
	}
	sec, _ := a.mailboxSettingsSections(context.Background(), "dana@example.com", true)
	if body := sec; !strings.Contains(body, "<b>sender&#39;s domain</b> is <b>shop.example</b> or <b>subject</b> contains <b>receipt</b>") ||
		!strings.Contains(body, "label <b>Shop</b>, mark read, and keep going") {
		t.Errorf("the list does not say the rule: %s", body)
	}
}

// TestTheEditorShowsWhatARuleWouldCatch — before it is saved, against the
// mailbox's recent mail.
func TestTheEditorShowsWhatARuleWouldCatch(t *testing.T) {
	a := appWithMailAccounts(t)
	deliverToDana(t, a, "your receipt 42")
	deliverToDana(t, a, "hello")
	rec := postRule(a, url.Values{"op": {"test"}, "c_field_0": {"subject"}, "c_op_0": {"contains"}, "c_value_0": {"receipt"}, "a_kind_0": {"markread"}})
	if !strings.Contains(rec.Body.String(), "Would have matched 1</strong> of your last 2 messages") || !strings.Contains(rec.Body.String(), "your receipt 42") {
		t.Fatalf("test: %s", rec.Body.String())
	}
	if rules, _ := a.vayuMail.Accounts().FiltersFor(context.Background(), "dana@example.com"); len(rules) != 0 {
		t.Error("testing a rule saved it")
	}
}

// TestSaveAndRunMovesTheMailAlreadyThere — the rule applies to the Inbox now,
// not only to mail still to come.
func TestSaveAndRunMovesTheMailAlreadyThere(t *testing.T) {
	a := appWithMailAccounts(t)
	deliverToDana(t, a, "weekly digest")
	deliverToDana(t, a, "hello")
	rec := postRule(a, url.Values{"op": {"save"}, "run": {"1"}, "c_field_0": {"subject"}, "c_op_0": {"contains"}, "c_value_0": {"digest"},
		"a_kind_0": {"move"}, "a_arg_0": {"Archive"}})
	if !strings.Contains(rec.Body.String(), "1 message changed") {
		t.Fatalf("run: %s", rec.Body.String())
	}
	rd := a.mailReader(withUser(httptest.NewRequest(http.MethodGet, "/", nil), rulesAdmin()), "dana")
	inbox, _ := a.vayuMail.ListFolder(rd, "Inbox")
	archive, _ := a.vayuMail.ListFolder(rd, "Archive")
	if len(inbox) != 1 || len(archive) != 1 || !strings.Contains(archive[0].Subject, "digest") {
		t.Fatalf("Inbox %d, Archive %d", len(inbox), len(archive))
	}
}

// TestRulesAreOrderedAndSwitchedFromTheList — Up, Down and the switch.
func TestRulesAreOrderedAndSwitchedFromTheList(t *testing.T) {
	a := appWithMailAccounts(t)
	ctx := context.Background()
	for _, w := range []string{"first", "second"} {
		postRule(a, url.Values{"op": {"save"}, "c_field_0": {"subject"}, "c_op_0": {"contains"}, "c_value_0": {w}, "a_kind_0": {"pin"}})
	}
	rules, _ := a.vayuMail.Accounts().FiltersFor(ctx, "dana@example.com")
	second := strconv.FormatInt(rules[1].ID, 10)
	postRule(a, url.Values{"op": {"up"}, "id": {second}})
	rules, _ = a.vayuMail.Accounts().FiltersFor(ctx, "dana@example.com")
	if rules[0].Conditions[0].Value != "second" {
		t.Fatalf("after Up the order is %q, %q", rules[0].Conditions[0].Value, rules[1].Conditions[0].Value)
	}
	postRule(a, url.Values{"op": {"toggle"}, "id": {second}})
	rules, _ = a.vayuMail.Accounts().FiltersFor(ctx, "dana@example.com")
	if rules[0].Enabled {
		t.Error("Switch off left the rule on")
	}
}

// TestAForwardIsConfirmedOnlyByTheButton — opening the link asks; only the
// form's POST confirms, and only once.
func TestAForwardIsConfirmedOnlyByTheButton(t *testing.T) {
	a := appWithMailAccounts(t)
	ctx := context.Background()
	accts := a.vayuMail.Accounts()
	tok, err := accts.RequestRuleForward(ctx, "dana@example.com", "accounts@ledger.example")
	if err != nil || tok == "" {
		t.Fatal(tok, err)
	}
	get := httptest.NewRecorder()
	a.handleRuleForwardConfirm(get, httptest.NewRequest(http.MethodGet, "/vayumail/forward/confirm?t="+url.QueryEscape(tok), nil))
	if !strings.Contains(get.Body.String(), "Confirm") || accts.RuleForwardConfirmed(ctx, "dana@example.com", "accounts@ledger.example") {
		t.Fatal("opening the link confirmed the forward")
	}
	confirm := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/vayumail/forward/confirm", strings.NewReader("t="+url.QueryEscape(tok)))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rec := httptest.NewRecorder()
		a.handleRuleForwardConfirm(rec, req)
		return rec
	}
	if rec := confirm(); rec.Code != http.StatusOK || !accts.RuleForwardConfirmed(ctx, "dana@example.com", "accounts@ledger.example") {
		t.Fatalf("the button did not confirm: %d", rec.Code)
	}
	if rec := confirm(); rec.Code != http.StatusNotFound {
		t.Errorf("the link worked twice: %d", rec.Code)
	}
}

// TestTheEditorIsDrawnFromTheStoredRule — an edit names the mailbox in the
// form, and the editor that comes back carries the stored address, not the
// form's text, whatever its case.
func TestTheEditorIsDrawnFromTheStoredRule(t *testing.T) {
	a := appWithMailAccounts(t)
	postRule(a, url.Values{"op": {"save"}, "c_field_0": {"subject"}, "c_op_0": {"contains"}, "c_value_0": {"x"}, "a_kind_0": {"pin"}})
	rules, _ := a.vayuMail.Accounts().FiltersFor(context.Background(), "dana@example.com")
	vals := url.Values{"op": {"edit"}, "id": {strconv.FormatInt(rules[0].ID, 10)}, "email": {"DANA@example.com"}}
	body := postForm(a.handleVayuOSFilterAction, "/os/vayumail/filters/action", vals.Encode(), rulesAdmin()).Body.String()
	if !strings.Contains(body, `name="email" value="dana@example.com"`) || strings.Contains(body, "DANA") {
		t.Fatalf("the editor is drawn from the form's address:\n%s", body)
	}
}
