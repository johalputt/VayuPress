// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/members"
	"github.com/johalputt/vayupress/internal/settings"
	"github.com/johalputt/vayupress/internal/users"
)

func membersApp(t *testing.T) *App {
	t.Helper()
	openMigratedDB(t)
	return &App{members: members.New(dbpkg.DB), siteSettings: settings.New(dbpkg.DB), userStore: users.New(dbpkg.DB)}
}

func getPage(t *testing.T, h http.HandlerFunc, target string) string {
	t.Helper()
	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodGet, target, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s: %d", target, rec.Code)
	}
	return rec.Body.String()
}

// Members is an overview: how many there are, the month's growth beside what
// happened lately, then the tiers. A figure shows only when it says something,
// so a new install shows none, and no form sits in the page.
func TestMembersIsAnOverviewOfFiguresThatSaySomething(t *testing.T) {
	a := membersApp(t)
	empty := getPage(t, a.handleOSMembers, "/os/members")
	for _, want := range []string{`data-page-kind="overview"`, `<span class="sa-overview__state">No members yet</span>`,
		"Nobody has joined in the last 30 days", "data-edit-tier", "data-new-tier", `href="/os/members/people"`} {
		if !strings.Contains(empty, want) {
			t.Errorf("empty install: missing %q", want)
		}
	}
	// The page's own content: from its kind to the tier sheet, which is a
	// dialog and may hold a form.
	_, own, _ := strings.Cut(empty, `data-page-kind="overview"`)
	own, _, _ = strings.Cut(own, "<dialog")
	for _, not := range []string{"stat-card", "<select", "data-new-user", `class="badge`, "style="} {
		if strings.Contains(own, not) {
			t.Errorf("empty install: the overview carries %q", not)
		}
	}

	ctx := context.Background()
	for _, e := range []string{"ann@readers.example", "bo@readers.example"} {
		if _, err := a.members.Upsert(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	if err := a.members.SetTier(ctx, "bo@readers.example", members.TierPaid); err != nil {
		t.Fatal(err)
	}
	page := getPage(t, a.handleOSMembers, "/os/members")
	for _, want := range []string{"2 members, 1 paying", ">+2<", "New members", "Pay after joining", "ann@readers.example joined"} {
		if !strings.Contains(page, want) {
			t.Errorf("missing %q", want)
		}
	}
	if strings.Contains(page, "Monthly revenue") {
		t.Error("revenue is shown with no subscription paying anything")
	}
}

// Everyone is a list; a member's plan and labels change in the inspector, and
// nothing in the page flow is a form field.
func TestEveryoneIsAListWithPlansChangedInTheInspector(t *testing.T) {
	a := membersApp(t)
	ctx := context.Background()
	for _, e := range []string{"ann@readers.example", "bo@readers.example"} {
		if _, err := a.members.Upsert(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	if err := a.members.SetTier(ctx, "bo@readers.example", members.TierPaid); err != nil {
		t.Fatal(err)
	}
	plain := getPage(t, a.handleOSMembersPeople, "/os/members/people")
	if strings.Contains(plain, ">Unconfirmed <") {
		t.Error("an Unconfirmed view is offered with nobody in it")
	}
	if _, err := dbpkg.DB.Exec(`INSERT INTO members(id,email,tier,status,created_at) VALUES('old','old@readers.example','free','active',datetime('now'))`); err != nil {
		t.Fatal(err)
	}

	page := getPage(t, a.handleOSMembersPeople, "/os/members/people")
	if !strings.Contains(page, `data-page-kind="list"`) || strings.Count(page, "data-list-row") != 3 {
		t.Fatalf("the list does not hold the three members")
	}
	flow, inspector, ok := strings.Cut(page, "data-list-inspector")
	if !ok || strings.Contains(flow, "<select") || !strings.Contains(inspector, `data-member-tier data-email="ann@readers.example"`) {
		t.Error("a member's plan is not changed in the inspector, or a select sits in the page flow")
	}
	for view, want := range map[string]string{"paying": "bo@readers.example", "free": "ann@readers.example", "unconfirmed": "old@readers.example"} {
		got := getPage(t, a.handleOSMembersPeople, "/os/members/people?view="+view)
		if strings.Count(got, "data-list-row") != 1 || !strings.Contains(got, want) {
			t.Errorf("view %s does not list only %s", view, want)
		}
	}
	if old := getPage(t, a.handleOSMembersPeople, "/os/members/people?view=unconfirmed"); !strings.Contains(old, `data-remove-member data-email="old@readers.example"`) {
		t.Error("an unconfirmed member cannot be removed from the inspector")
	}
}

// The team is administered in Settings, which edits in place, and its page
// loads the script its controls need.
func TestTheTeamIsASettingsCategory(t *testing.T) {
	a := membersApp(t)
	c, ok := settingsCategoryFor("team")
	if !ok || c.Script != "js/admin-os-members.js" {
		t.Fatalf("no Team category with its script: %+v", c)
	}
	roster := string(settingsPageBody(context.Background(), a, c))
	for _, want := range []string{`data-page-kind="settings"`, "data-new-user", "data-u-role"} {
		if !strings.Contains(roster, want) {
			t.Errorf("Settings › Team: missing %q", want)
		}
	}
	if strings.Contains(roster, `class="badge`) || strings.Contains(roster, `class="card`) {
		t.Error("Settings › Team still draws the Members card and its badges")
	}
}
