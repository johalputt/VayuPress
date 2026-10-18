// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/johalputt/vayupress/internal/avatar"
	"github.com/johalputt/vayupress/internal/config"
	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/members"
	"github.com/johalputt/vayupress/internal/render"
	"github.com/johalputt/vayupress/internal/secrets"
	"github.com/johalputt/vayupress/internal/settings"
)

// accountApp opens a fresh install with one member signed in, and returns the
// app, the member and their session cookie.
func accountApp(t *testing.T, paid bool) (*App, *members.Member, *http.Cookie) {
	t.Helper()
	config.Cfg.DBPath = ":memory:"
	config.Cfg.Domain = "example.com"
	if err := dbpkg.Init(); err != nil {
		t.Fatalf("db init: %v", err)
	}
	t.Cleanup(func() {
		dbpkg.ClosePools()
		_ = dbpkg.DB.Close()
	})
	// Members sign in while membership is on, and the account panel is served.
	render.SetMembershipEnabled(true)
	t.Cleanup(func() { render.SetMembershipEnabled(false) })
	store := members.New(dbpkg.DB)
	a := &App{members: store, siteSettings: settings.New(dbpkg.DB)}
	m, err := store.UpsertScoped(t.Context(), "", "reader@example.com")
	if err != nil {
		t.Fatalf("create member: %v", err)
	}
	if paid {
		if err := store.SetTier(t.Context(), m.Email, members.TierPaid); err != nil {
			t.Fatalf("set tier: %v", err)
		}
	}
	token, err := store.CreateSession(t.Context(), m.ID)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	m, _ = store.Get(t.Context(), m.Email)
	return a, m, &http.Cookie{Name: memberCookie, Value: token}
}

// renderAccountPage drives the real handler with a real member session and returns
// the page HTML. Structural assertions on the shipped markup are worth more than
// assertions on a builder in isolation.
func renderAccountPage(t *testing.T, paid bool) string {
	t.Helper()
	a, _, c := accountApp(t, paid)
	return getAccount(t, a, c, "/members/account")
}

func getAccount(t *testing.T, a *App, c *http.Cookie, target string) string {
	t.Helper()
	req := httptest.NewRequest("GET", target, nil)
	req.AddCookie(c)
	rec := httptest.NewRecorder()
	a.handleMemberAccount(rec, req)
	if rec.Code != 200 {
		t.Fatalf("account page = %d, want 200", rec.Code)
	}
	return rec.Body.String()
}

// The page is one grammar (3cx): sections laid open, one row per setting, no
// folded boxes, no emoji headings, at most one filled button.
func TestTheAccountPageIsOneGrammar(t *testing.T) {
	for _, paid := range []bool{false, true} {
		page := renderAccountPage(t, paid)
		for _, id := range []string{`id="profile"`, `id="plan"`, `id="security"`, `id="emails"`, `id="activity"`} {
			if !strings.Contains(page, id) {
				t.Errorf("paid=%v: the page has no section %s", paid, id)
			}
		}
		for _, not := range []string{"<details", "ma-acc", "ma-card", "ma-chip"} {
			if strings.Contains(page, not) {
				t.Errorf("paid=%v: the page still carries %q", paid, not)
			}
		}
		for _, r := range page {
			if r >= 0x1F300 && r <= 0x1FAFF || r >= 0x2600 && r <= 0x27BF {
				t.Errorf("paid=%v: the page carries the pictograph %q", paid, r)
				break
			}
		}
		// The rows' own actions: a control opened in place has its own one.
		n := 0
		for _, part := range strings.Split(page, `<div class="acct-row__a">`)[1:] {
			if end := strings.Index(part, "</div>"); end >= 0 && strings.Contains(part[:end], "acct-btn--primary") {
				n++
			}
		}
		if n > 1 {
			t.Errorf("paid=%v: %d rows with a filled button; a page has at most one", paid, n)
		}
	}
}

// Every control a member has is on the page, each where it belongs.
func TestTheAccountPageHasEveryControl(t *testing.T) {
	page := renderAccountPage(t, false)
	for _, want := range []string{
		`<form id="acct-form" method="POST" action="/members/account"`,
		`name="name" form="acct-form"`,
		`name="reply_notify" value="1" form="acct-form"`, `name="newsletter" value="1" form="acct-form"`,
		`data-acct-upload`, `action="/members/account/delete"`, `name="confirm"`,
		`action="/members/logout"`, `href="/pricing"`, "reader@example.com",
	} {
		if !strings.Contains(page, want) {
			t.Errorf("the page has no %q", want)
		}
	}
	// The picture chooser offers the automatic picture and every cartoon.
	if got := strings.Count(page, `data-acct-avatar="`); got != avatar.CartoonCount+1 {
		t.Errorf("%d pictures to choose from, want %d", got, avatar.CartoonCount+1)
	}
	// Without the page's script, nothing that opens in place is hidden: the
	// panels are only folded once the script has marked the page.
	if !strings.Contains(page, "classList.add('acct-js')") {
		t.Error("the page does not mark itself as scripted, so its folded rows would never fold")
	}
	css := repoFile(t, "static/css/signup.css")
	if !strings.Contains(css, ".acct-js .acct-more:not(.is-open) { display: none; }") {
		t.Error("the rows that open in place are not folded only under the script")
	}
}

// Advertising and claiming an address open the account panel, which is
// served only while membership is on: without it their buttons would do
// nothing, so they are not drawn.
func TestPanelOnlyRowsNeedThePanel(t *testing.T) {
	a, _, c := accountApp(t, false)
	if page := getAccount(t, a, c, "/members/account"); !strings.Contains(page, `data-acct-portal="advertise"`) || !strings.Contains(page, "/static/js/portal.js") {
		t.Fatal("with membership on, the page offers no Advertising, or does not load the panel")
	}
	render.SetMembershipEnabled(false)
	page := getAccount(t, a, c, "/members/account")
	if strings.Contains(page, `data-acct-portal="`) {
		t.Error("with the panel not served, the page draws buttons that would open it")
	}
}

// What happens next to a plan, and what the member can do about it. Only
// Stripe renews by itself, so only Stripe is offered Cancel renewal.
func TestRenewalSaysWhatHappensNext(t *testing.T) {
	now := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	end := now.AddDate(0, 0, 16)
	trial := now.AddDate(0, 0, 5)
	cases := []struct {
		name          string
		sub           *members.Subscription
		stripe        bool
		label, action string
	}{
		{"stripe renews", &members.Subscription{Cadence: members.CadenceMonthly, CurrentPeriodEnd: &end}, true, "Next payment", "cancel"},
		{"stripe trial", &members.Subscription{Cadence: members.CadenceMonthly, CurrentPeriodEnd: &end, TrialEnd: &trial}, true, "Trial ends", "cancel"},
		{"stripe cancelled", &members.Subscription{Cadence: members.CadenceMonthly, CurrentPeriodEnd: &end, CancelAtPeriodEnd: true}, true, "Ends", "keep"},
		{"offline cancelled", &members.Subscription{Cadence: members.CadenceMonthly, CurrentPeriodEnd: &end, CancelAtPeriodEnd: true}, false, "Ends", ""},
		{"offline order", &members.Subscription{Cadence: members.CadenceYearly, CurrentPeriodEnd: &end}, false, "Paid until", "renew"},
		{"complimentary", &members.Subscription{Cadence: members.CadenceComplimentary, CurrentPeriodEnd: &end}, true, "Renews", ""},
	}
	for _, c := range cases {
		r, ok := renewalFor(c.sub, c.stripe, now)
		if !ok || r.Label != c.label || r.Action != c.action {
			t.Errorf("%s: got %q/%q (%v), want %q/%q", c.name, r.Label, r.Action, ok, c.label, c.action)
		}
	}
	if _, ok := renewalFor(nil, true, now); ok {
		t.Error("no subscription still produced a renewal row")
	}
	if _, ok := renewalFor(&members.Subscription{Cadence: members.CadenceMonthly}, true, now); ok {
		t.Error("a subscription with no period end produced a renewal row")
	}
}

// fakeStripeFor points the app's outbound client at a fake Stripe holding one
// live subscription for cus_reader, and records every change made to it.
func fakeStripeFor(t *testing.T, a *App, refuse bool) *[]string {
	t.Helper()
	var mu sync.Mutex
	var calls []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte(`{"data":[{"id":"sub_1","status":"active"}]}`))
			return
		}
		_ = r.ParseForm()
		mu.Lock()
		calls = append(calls, r.Method+" "+r.URL.Path+" "+r.PostForm.Encode())
		mu.Unlock()
		if refuse {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"message":"refused"}}`))
			return
		}
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(srv.Close)
	target, _ := url.Parse(srv.URL)
	a.outboundClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		r.URL.Scheme, r.URL.Host = target.Scheme, target.Host
		return http.DefaultTransport.RoundTrip(r)
	})}
	a.secrets = secrets.New(dbpkg.DB, nil, filepath.Join(t.TempDir(), ".kek"))
	if _, err := a.secrets.Upsert(context.Background(), secrets.ProviderStripe, "Stripe", "", "sk_test_x", true, false); err != nil {
		t.Fatalf("store the Stripe key: %v", err)
	}
	return &calls
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func postAccount(t *testing.T, h http.HandlerFunc, c *http.Cookie, target string, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("POST", target, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(c)
	rec := httptest.NewRecorder()
	h(rec, req)
	return rec
}

// A Stripe member stops their plan renewing from the page, and can take it
// back. Stripe is changed, and the local record follows only when it agreed.
func TestAMemberStopsAndKeepsTheirRenewal(t *testing.T) {
	a, m, c := accountApp(t, true)
	end := time.Now().AddDate(0, 0, 20)
	if err := a.members.StartSubscription(t.Context(), members.SubscriptionInput{MemberID: m.ID, TierSlug: members.TierPaid,
		Cadence: members.CadenceMonthly, AmountCents: 19900, Currency: "INR", CurrentPeriodEnd: &end}); err != nil {
		t.Fatal(err)
	}
	if err := a.members.SetStripeCustomer(t.Context(), m.Email, "cus_reader"); err != nil {
		t.Fatal(err)
	}
	calls := fakeStripeFor(t, a, false)

	page := getAccount(t, a, c, "/members/account")
	if !strings.Contains(page, `action="/members/account/renewal"`) || !strings.Contains(page, "Cancel renewal") {
		t.Fatal("a Stripe member's page offers no way to stop the renewal")
	}
	rec := postAccount(t, a.handleMemberRenewal, c, "/members/account/renewal", url.Values{"renew": {"0"}})
	if loc := rec.Header().Get("Location"); loc != "/members/account?renewal=off#plan" {
		t.Fatalf("stopping the renewal answered %d %q", rec.Code, loc)
	}
	if got := strings.Join(*calls, "|"); got != "POST /v1/subscriptions/sub_1 cancel_at_period_end=true" {
		t.Errorf("Stripe was asked %q", got)
	}
	if sub, _ := a.members.ActiveSubscription(t.Context(), m.ID); sub == nil || !sub.CancelAtPeriodEnd {
		t.Error("the plan still renews in the local record")
	}
	if page := getAccount(t, a, c, "/members/account?renewal=off"); !strings.Contains(page, "Keep my plan") || !strings.Contains(page, "Your plan will not renew") {
		t.Error("after stopping it, the page offers no way to keep the plan or does not say what happened")
	}

	*calls = nil
	rec = postAccount(t, a.handleMemberRenewal, c, "/members/account/renewal", url.Values{"renew": {"1"}})
	if loc := rec.Header().Get("Location"); loc != "/members/account?renewal=on#plan" {
		t.Fatalf("keeping the plan answered %q", loc)
	}
	if got := strings.Join(*calls, "|"); got != "POST /v1/subscriptions/sub_1 cancel_at_period_end=false" {
		t.Errorf("Stripe was asked %q", got)
	}
	if sub, _ := a.members.ActiveSubscription(t.Context(), m.ID); sub == nil || sub.CancelAtPeriodEnd {
		t.Error("the plan does not renew again in the local record")
	}
}

// When Stripe refuses, nothing changes here either: the page must never say
// "Ends" while Stripe goes on charging.
func TestARefusedRenewalChangeChangesNothing(t *testing.T) {
	a, m, c := accountApp(t, true)
	end := time.Now().AddDate(0, 0, 20)
	_ = a.members.StartSubscription(t.Context(), members.SubscriptionInput{MemberID: m.ID, TierSlug: members.TierPaid,
		Cadence: members.CadenceMonthly, AmountCents: 500, CurrentPeriodEnd: &end})
	_ = a.members.SetStripeCustomer(t.Context(), m.Email, "cus_reader")
	fakeStripeFor(t, a, true)
	rec := postAccount(t, a.handleMemberRenewal, c, "/members/account/renewal", url.Values{"renew": {"0"}})
	if loc := rec.Header().Get("Location"); loc != "/members/account?renewal=failed#plan" {
		t.Fatalf("a refused change answered %q", loc)
	}
	if sub, _ := a.members.ActiveSubscription(t.Context(), m.ID); sub == nil || sub.CancelAtPeriodEnd {
		t.Error("Stripe refused, yet the local record says the plan ends")
	}
}

// A member deletes their own account by typing their address. A wrong address
// deletes nothing; payments that cannot be stopped delete nothing.
func TestAMemberDeletesTheirAccount(t *testing.T) {
	a, m, c := accountApp(t, false)
	rec := postAccount(t, a.handleMemberAccountDelete, c, "/members/account/delete", url.Values{"confirm": {"someone@else.example"}})
	if loc := rec.Header().Get("Location"); loc != "/members/account?delete=confirm#leave" {
		t.Fatalf("a wrong address answered %q", loc)
	}
	if _, err := a.members.Get(t.Context(), m.Email); err != nil {
		t.Fatal("a wrong confirmation deleted the account")
	}

	rec = postAccount(t, a.handleMemberAccountDelete, c, "/members/account/delete", url.Values{"confirm": {" READER@example.com "}})
	if loc := rec.Header().Get("Location"); loc != "/members?deleted=1" {
		t.Fatalf("deleting answered %d %q", rec.Code, loc)
	}
	if _, err := a.members.Get(t.Context(), m.Email); err == nil {
		t.Error("the account is still there")
	}
	if !strings.Contains(rec.Header().Get("Set-Cookie"), memberCookie+"=;") {
		t.Error("the member cookie was not cleared")
	}
}

func TestAnAccountWhosePaymentsCannotStopIsNotDeleted(t *testing.T) {
	a, m, c := accountApp(t, true)
	end := time.Now().AddDate(0, 0, 20)
	_ = a.members.StartSubscription(t.Context(), members.SubscriptionInput{MemberID: m.ID, TierSlug: members.TierPaid,
		Cadence: members.CadenceMonthly, AmountCents: 500, CurrentPeriodEnd: &end})
	_ = a.members.SetStripeCustomer(t.Context(), m.Email, "cus_reader")
	calls := fakeStripeFor(t, a, true)
	rec := postAccount(t, a.handleMemberAccountDelete, c, "/members/account/delete", url.Values{"confirm": {m.Email}})
	if loc := rec.Header().Get("Location"); loc != "/members/account?delete=failed#leave" {
		t.Fatalf("answered %q", loc)
	}
	if got := strings.Join(*calls, "|"); got != "DELETE /v1/subscriptions/sub_1 " {
		t.Errorf("Stripe was asked %q", got)
	}
	if _, err := a.members.Get(t.Context(), m.Email); err != nil {
		t.Error("the account was deleted while its payments went on")
	}
}

// The switches save by fetch and are answered in JSON; the form without the
// script still lands back on the page.
func TestTheEmailSwitchesSaveAsTheyChange(t *testing.T) {
	a, m, c := accountApp(t, false)
	req := httptest.NewRequest("POST", "/members/account", strings.NewReader("name=Asha&newsletter=1"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	req.AddCookie(c)
	rec := httptest.NewRecorder()
	a.handleMemberAccountUpdate(rec, req)
	var got map[string]bool
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &got) != nil || !got["saved"] {
		t.Fatalf("a switch's save answered %d %q", rec.Code, rec.Body.String())
	}
	m, _ = a.members.Get(t.Context(), m.Email)
	if !m.NewsletterOptIn || m.ReplyNotify {
		t.Errorf("newsletter=%v replies=%v, want true/false", m.NewsletterOptIn, m.ReplyNotify)
	}
	rec = postAccount(t, a.handleMemberAccountUpdate, c, "/members/account", url.Values{"name": {"Asha"}})
	if loc := rec.Header().Get("Location"); loc != "/members/account?saved=1" {
		t.Errorf("the form without the script answered %q", loc)
	}
}

// The chooser's previews are the member's own pictures, for the member alone.
func TestPicturePreviewsAreTheMembersOwn(t *testing.T) {
	a, _, c := accountApp(t, false)
	r := chi.NewRouter()
	r.Get("/api/v1/members/avatar/preview/{choice}", a.handleMemberAvatarPreview)
	for target, want := range map[string]int{
		"/api/v1/members/avatar/preview/auto": 200,
		"/api/v1/members/avatar/preview/0":    200,
		"/api/v1/members/avatar/preview/11":   200,
		"/api/v1/members/avatar/preview/12":   404,
		"/api/v1/members/avatar/preview/-1":   404,
		"/api/v1/members/avatar/preview/x":    404,
	} {
		req := httptest.NewRequest("GET", target, nil)
		req.AddCookie(c)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		if rec.Code != want {
			t.Errorf("%s = %d, want %d", target, rec.Code, want)
		}
		if want == 200 && !strings.HasPrefix(rec.Header().Get("Content-Type"), "image/svg+xml") {
			t.Errorf("%s is not an SVG", target)
		}
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/members/avatar/preview/auto", nil))
	if rec.Code != 404 {
		t.Errorf("a preview answered %d to no one signed in", rec.Code)
	}
}

// The panel's facts come with ?detail=1 only, so a page view does not pay for
// them.
func TestThePanelsFactsComeOnlyWhenAsked(t *testing.T) {
	a, _, c := accountApp(t, false)
	get := func(target string) map[string]any {
		req := httptest.NewRequest("GET", target, nil)
		req.AddCookie(c)
		rec := httptest.NewRecorder()
		a.handleMemberMe(rec, req)
		var out map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return out
	}
	if _, has := get("/api/v1/members/me")["account"]; has {
		t.Error("every page view carries the panel's facts")
	}
	acct, _ := get("/api/v1/members/me?detail=1")["account"].(map[string]any)
	if acct == nil || acct["plan"] == nil || acct["emails"] == nil {
		t.Fatalf("the panel's facts are missing: %v", acct)
	}
	if _, has := acct["mailbox"]; has {
		t.Error("a member with no mailbox is told of one")
	}
}

// TestAccountPageDump writes the rendered page for visual inspection when
// VP_DUMP_ACCOUNT_HTML names a path. It asserts nothing; it exists so the design
// can be reviewed in a browser against the real markup rather than a mock.
func TestAccountPageDump(t *testing.T) {
	path := os.Getenv("VP_DUMP_ACCOUNT_HTML")
	if path == "" {
		t.Skip("set VP_DUMP_ACCOUNT_HTML to write the rendered account page")
	}
	if err := os.WriteFile(path, []byte(renderAccountPage(t, false)), 0o600); err != nil {
		t.Fatalf("write dump: %v", err)
	}
}
