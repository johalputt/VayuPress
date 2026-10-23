// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/johalputt/vayupress/internal/alerts"
	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/domain"
	"github.com/johalputt/vayupress/internal/users"
)

func alertsApp(t *testing.T) (*App, domain.Domain) {
	t.Helper()
	a, d := shieldSitesApp(t)
	if _, err := dbpkg.DB.Exec(`INSERT INTO domains(id,host,site_type,is_primary,status,created_at,updated_at) VALUES('c2','two.example','blog',0,'active',datetime('now'),datetime('now'))`); err != nil {
		t.Fatal(err)
	}
	a.alerts = alerts.New(dbpkg.DB, nil)
	return a, d
}

// alertCall sends one request to the alerts' API as role, for site (nil for
// the install's API).
func alertCall(a *App, h http.HandlerFunc, method, path string, site *domain.Domain, aid, body string, role string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	rc := chi.NewRouteContext()
	if site != nil {
		rc.URLParams.Add("id", site.ID)
	}
	if aid != "" {
		rc.URLParams.Add("aid", aid)
	}
	ctx := context.WithValue(r.Context(), chi.RouteCtxKey, rc)
	if site != nil {
		ctx = context.WithValue(ctx, ctxScopedDomainKey, *site)
	}
	ctx = context.WithValue(ctx, ctxUserKey, &users.User{ID: "op", Role: role})
	w := httptest.NewRecorder()
	h(w, r.WithContext(ctx))
	return w
}

const alertBody = `{"domain_id":"%s","alert":{"name":"Spike","metric":"views","op":"above","threshold":"0","window":"15","cooldown":"60","severity":"danger","mail_to":""}}`

func alertJSON(site string) string { return strings.Replace(alertBody, "%s", site, 1) }

// Only an administrator adds an alert; a body naming another site saves
// nothing; the reason a draft is refused is the server's own.
func TestOnlyAnAdministratorAddsAnAlertToTheSiteItNames(t *testing.T) {
	a, d := alertsApp(t)
	base := "/os/d/c1/api/analytics/alerts"
	if w := alertCall(a, a.handleOSAlertCreate, http.MethodPost, base, &d, "", alertJSON("c1"), users.RoleEditor); w.Code != http.StatusForbidden {
		t.Errorf("an editor's alert answered %d, want 403", w.Code)
	}
	if w := alertCall(a, a.handleOSAlertCreate, http.MethodPost, base, &d, "", alertJSON("c2"), users.RoleAdmin); w.Code != http.StatusBadRequest {
		t.Errorf("a body naming another site answered %d, want 400", w.Code)
	}
	bad := strings.Replace(alertJSON("c1"), `"window":"15"`, `"window":"7"`, 1)
	if w := alertCall(a, a.handleOSAlertCreate, http.MethodPost, base, &d, "", bad, users.RoleAdmin); w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "window") {
		t.Errorf("an unoffered window answered %d: %s", w.Code, w.Body.String())
	}
	w := alertCall(a, a.handleOSAlertCreate, http.MethodPost, base, &d, "", alertJSON("c1"), users.RoleAdmin)
	if w.Code != http.StatusOK {
		t.Fatalf("the administrator's alert answered %d: %s", w.Code, w.Body.String())
	}
	if list, _ := a.alerts.List(context.Background(), "c1"); len(list) != 1 {
		t.Fatalf("site one holds %d alerts, want 1", len(list))
	}
	for _, site := range []string{"c2", alerts.Install} {
		if list, _ := a.alerts.List(context.Background(), site); len(list) != 0 {
			t.Errorf("the alert was saved for %s too", site)
		}
	}
}

// One site's URL cannot delete, or mark seen, another site's alert.
func TestASitesURLReachesItsOwnAlertsAlone(t *testing.T) {
	a, d := alertsApp(t)
	ctx := context.Background()
	two := domain.Domain{ID: "c2", Host: "two.example"}
	w := alertCall(a, a.handleOSAlertCreate, http.MethodPost, "/os/d/c2/api/analytics/alerts", &two, "", alertJSON("c2"), users.RoleAdmin)
	var created struct{ ID string }
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil || created.ID == "" {
		t.Fatalf("no id came back: %s", w.Body.String())
	}
	if w := alertCall(a, a.handleOSAlertDelete, http.MethodDelete, "/os/d/c1/api/analytics/alerts/"+created.ID, &d, created.ID, "", users.RoleAdmin); w.Code != http.StatusNotFound {
		t.Errorf("site one's URL deleting site two's alert answered %d, want 404", w.Code)
	}
	if list, _ := a.alerts.List(ctx, "c2"); len(list) != 1 {
		t.Fatalf("site two's alert is gone")
	}
	// Threshold 0 above, and site two has no views: a check fires nothing.
	// Give it one view, and it fires.
	if _, err := dbpkg.DB.Exec(`INSERT INTO analytics_pageviews(id,session_id,url_path,event_type,domain_id,created_at,visitor_id) VALUES('p1','s','/',1,'c2',datetime('now'),'v')`); err != nil {
		t.Fatal(err)
	}
	a.checkAlerts(ctx)
	alertCall(a, a.handleOSAlertsSeen, http.MethodPost, "/os/d/c1/api/analytics/alerts/seen", &d, "", "", users.RoleAdmin)
	if u, _ := a.alerts.UnseenBySite(ctx); len(u) != 1 || u[0].Site != "c2" {
		t.Errorf("marking site one seen cleared site two's firing: %+v", u)
	}
	if w := alertCall(a, a.handleOSAlertDelete, http.MethodDelete, "/os/d/c2/api/analytics/alerts/"+created.ID, &two, created.ID, "", users.RoleAdmin); w.Code != http.StatusOK {
		t.Errorf("site two deleting its own alert answered %d", w.Code)
	}
}

// A firing is a bell line for its site, in its severity, linking to its
// page, until it is marked seen; mail it could not send says why.
func TestAFiringIsInTheBellUntilSeen(t *testing.T) {
	a, d := alertsApp(t)
	ctx := context.Background()
	draft := alerts.Draft{Name: "Spike", Metric: "views", Op: "above", Threshold: "0", Window: "15", Cooldown: "60", Severity: "danger", MailTo: "ops@example.com"}
	al, err := draft.Validate("c1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.alerts.Create(ctx, al); err != nil {
		t.Fatal(err)
	}
	if _, err := dbpkg.DB.Exec(`INSERT INTO analytics_pageviews(id,session_id,url_path,event_type,domain_id,created_at,visitor_id) VALUES('p1','s','/',1,'c1',datetime('now'),'v')`); err != nil {
		t.Fatal(err)
	}
	a.checkAlerts(ctx)
	bell := a.osNotifications(ctx, &osSettings{AccessLevel: accessAdmin})
	var line *osNotification
	for i := range bell {
		if bell[i].Kind == "alert" {
			line = &bell[i]
		}
	}
	if line == nil || line.Href != "/os/d/c1/analytics#alerts" || line.Severity != "danger" || line.line() != "1 alert fired on one.example" {
		t.Fatalf("the bell's alert line is %+v", line)
	}
	h, _ := a.alerts.History(ctx, "c1", 5)
	if len(h) != 1 || h[0].Mailed != "not sent: this install has no outgoing mail set up" {
		t.Errorf("an unsent mail does not say why: %+v", h)
	}
	alertCall(a, a.handleOSAlertsSeen, http.MethodPost, "/os/d/c1/api/analytics/alerts/seen", &d, "", "", users.RoleAdmin)
	for _, n := range a.osNotifications(ctx, &osSettings{AccessLevel: accessAdmin}) {
		if n.Kind == "alert" {
			t.Errorf("a seen firing is still in the bell: %+v", n)
		}
	}
	// A removed site's firings no longer point the bell at its page.
	if _, err := dbpkg.DB.Exec(`INSERT INTO alert_events(alert_id,domain_id,name,metric,op,threshold,window_min,value,severity) VALUES('x','c1','Spike','views','above',0,15,1,'warn')`); err != nil {
		t.Fatal(err)
	}
	if err := a.domains.Delete(ctx, "c1"); err != nil {
		t.Fatal(err)
	}
	for _, n := range a.osNotifications(ctx, &osSettings{AccessLevel: accessAdmin}) {
		if n.Kind == "alert" {
			t.Errorf("a removed site's firing is in the bell: %+v", n)
		}
	}
}

// The page lists the alerts and their firings; the form and the buttons
// that write are drawn for an administrator alone.
func TestTheAlertsSectionWritesOnlyForAnAdministrator(t *testing.T) {
	a, _ := alertsApp(t)
	ctx := context.Background()
	al, _ := alerts.Draft{Name: "Quiet <b>", Metric: "refused", Op: "above", Threshold: "50", Window: "60", Cooldown: "180", Severity: "warn"}.Validate("c1")
	if _, err := a.alerts.Create(ctx, al); err != nil {
		t.Fatal(err)
	}
	admin := a.alertsSection(ctx, "c1", "/os/d/c1/api/analytics/alerts", true, "n")
	for _, want := range []string{"Quiet &lt;b&gt;", "Requests the shield refused above 50 in 1 hour, at most once every 3 hours",
		"Not checked yet", "Never", `data-sheet="alert-new"`, "data-alert-delete", `data-alerts-api="/os/d/c1/api/analytics/alerts"`} {
		if !strings.Contains(admin, want) {
			t.Errorf("the administrator's section has no %q", want)
		}
	}
	if strings.Contains(admin, "Quiet <b>") {
		t.Errorf("the alert's name is not escaped")
	}
	editor := a.alertsSection(ctx, "c1", "/os/d/c1/api/analytics/alerts", false, "n")
	for _, not := range []string{`data-sheet="alert-new"`, "data-alert-delete", "alert-name"} {
		if strings.Contains(editor, not) {
			t.Errorf("an editor's section carries %q", not)
		}
	}
	if !strings.Contains(editor, "Quiet &lt;b&gt;") {
		t.Errorf("an editor does not see the alert")
	}
}
