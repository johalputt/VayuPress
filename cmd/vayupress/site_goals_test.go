// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/johalputt/vayupress/internal/analytics"
	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/domain"
	vasession "github.com/johalputt/vayupress/internal/vayuanalytics/session"
)

// A goal belongs to a site. A goal made on a site's own
// Analytics counts that site's traffic alone; the install's goals, and every
// goal made before goals had a site, count every site as they always did;
// and a site's page can neither show nor remove another site's goal.
func TestAGoalBelongsToItsSite(t *testing.T) {
	openMigratedDB(t)
	ctx := context.Background()
	st := analytics.New(dbpkg.DB)
	st.UseVisits(vasession.NewMasterHasher([]byte("goals")))
	a := &App{analytics: st}
	collect := func(site, ip, path string) {
		t.Helper()
		if err := st.Collect(ctx, analytics.CollectRequest{URL: path, Hostname: site + ".test", EventType: 1, Lang: "en"},
			ip, "Mozilla/5.0 (Windows NT 10.0) Chrome/130.0", site); err != nil {
			t.Fatal(err)
		}
	}
	collect("s1", "198.51.100.1", "/thank-you")
	collect("s2", "198.51.100.2", "/thank-you")
	collect("s2", "198.51.100.3", "/thank-you")

	// A goal from before migration 114 names no site: it is the install's.
	if _, err := dbpkg.DB.Exec(`INSERT INTO analytics_goals(id,name,kind,target) VALUES('old','Old','path','/thank-you')`); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateGoalScoped(ctx, "s1", "Signed up", "path", "/thank-you"); err != nil {
		t.Fatal(err)
	}

	install, _ := st.GoalResults(ctx, 30)
	if len(install) != 1 || install[0].ID != "old" || install[0].UniqueVisitors != 3 {
		t.Errorf("the install's goals: %+v; want the old goal alone, reached by all 3 visitors", install)
	}
	one, _ := st.GoalResultsScoped(ctx, "s1", 30)
	if len(one) != 1 || one[0].Name != "Signed up" || one[0].UniqueVisitors != 1 || one[0].ConversionRate != 100 {
		t.Errorf("site one's goals: %+v; want its own goal, 1 of its 1 visitor", one)
	}
	if two, _ := st.GoalResultsScoped(ctx, "s2", 30); len(two) != 0 {
		t.Errorf("site two shows another site's goals: %+v", two)
	}

	page := a.renderSiteAnalyticsBody(ctx, hostedView(domain.Domain{ID: "s2"}), "two.test", 30)
	if strings.Contains(page, "Signed up") || strings.Contains(page, ">Old<") {
		t.Error("site two's page shows a goal that is not its own")
	}
	if !strings.Contains(page, `data-goal-base="/os/d/s2/analytics/goals"`) {
		t.Error("site two's goals form does not write to site two")
	}

	// Site two's console cannot remove site one's goal, nor the install's.
	del := func(site, gid string) int {
		r := httptest.NewRequest(http.MethodDelete, "/os/d/"+site+"/analytics/goals/"+gid, nil)
		rc := chi.NewRouteContext()
		rc.URLParams.Add("id", site)
		rc.URLParams.Add("gid", gid)
		r = r.WithContext(context.WithValue(context.WithValue(r.Context(), chi.RouteCtxKey, rc),
			ctxScopedDomainKey, domain.Domain{ID: site, Host: site + ".test"}))
		w := httptest.NewRecorder()
		a.handleOSScopedDeleteGoal(w, r)
		return w.Code
	}
	sid := one[0].ID
	if code := del("s2", sid); code != http.StatusNotFound {
		t.Errorf("site two deleting site one's goal answered %d, want 404", code)
	}
	if code := del("s2", "old"); code != http.StatusNotFound {
		t.Errorf("site two deleting the install's goal answered %d, want 404", code)
	}
	if code := del("s1", sid); code != http.StatusOK {
		t.Errorf("site one deleting its own goal answered %d, want 200", code)
	}
	if left, _ := st.ListGoalsScoped(ctx, "s1"); len(left) != 0 {
		t.Errorf("site one's goal survived its deletion: %+v", left)
	}
}

// A funnel belongs to a site the same way: it walks that site's visits only.
func TestAFunnelWalksItsSiteAlone(t *testing.T) {
	openMigratedDB(t)
	ctx := context.Background()
	st := analytics.New(dbpkg.DB)
	st.UseVisits(vasession.NewMasterHasher([]byte("funnels")))
	collect := func(site, ip, path string) {
		t.Helper()
		if err := st.Collect(ctx, analytics.CollectRequest{URL: path, Hostname: site + ".test", EventType: 1, Lang: "en"},
			ip, "Mozilla/5.0 (Windows NT 10.0) Chrome/130.0", site); err != nil {
			t.Fatal(err)
		}
	}
	collect("s1", "198.51.100.1", "/cart")
	collect("s2", "198.51.100.2", "/cart")
	steps := []analytics.FunnelStep{{Name: "Cart", URLPath: "/cart"}}
	fid, err := st.CreateFunnelScoped(ctx, "s1", "Buy", steps, 30)
	if err != nil {
		t.Fatal(err)
	}
	_, res, err := st.GetFunnelScoped(ctx, "s1", fid)
	if err != nil || len(res) != 1 || res[0].Visitors != 1 {
		t.Errorf("site one's funnel: %+v, %v; want 1 visit at the cart, its own", res, err)
	}
	if _, _, err := st.GetFunnelScoped(ctx, "s2", fid); err == nil {
		t.Error("site two read site one's funnel")
	}
	if fs, _ := st.ListFunnels(ctx); len(fs) != 0 {
		t.Errorf("the install's funnels list a site's funnel: %+v", fs)
	}
}
