// SPDX-License-Identifier: Apache-2.0

package main

// release_mirror_test.go — where the mirror answers, and whether a slow client
// can actually finish downloading from it.
//
// The download test runs under the real conditions that break it: a real
// http.Server with a short WriteTimeout, the real core middleware chain (which
// wraps the writer in gzip), and a client that reads slowly. A handler test with
// a ResponseRecorder has no write deadline at all and would pass against the
// very defect it exists for.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/johalputt/vayupress/internal/config"
	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/domain"
	"github.com/johalputt/vayupress/internal/update"
	"github.com/johalputt/vayupress/internal/users"
	"github.com/johalputt/vayupress/internal/vayushield"
)

const mirrorTestHost = "updates.example.com"

// heldMirror writes a mirror holding one release with one binary of n bytes,
// in the persisted format, and returns the server wrapping it.
func heldMirror(t *testing.T, n int) (*releaseMirrorServer, []byte) {
	t.Helper()
	root := t.TempDir()
	bin := bytes.Repeat([]byte("v"), n)
	dir := filepath.Join(root, "releases", "v3.17.66")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "vayupress"), bin, 0o644); err != nil {
		t.Fatal(err)
	}
	st := update.MirrorState{
		CheckedAt: time.Now().UTC(), SyncedAt: time.Now().UTC(),
		Releases: []update.MirroredRelease{{
			Tag: "v3.17.66", VerifiedAt: time.Now().UTC(),
			Raw:    json.RawMessage(`{"tag_name":"v3.17.66"}`),
			Assets: []update.MirroredAsset{{Name: "vayupress", Size: int64(n), SHA256: "00", Checks: []string{"sigstore"}}},
		}},
	}
	raw, _ := json.Marshal(st)
	if err := os.WriteFile(filepath.Join(root, "state.json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	return newReleaseMirrorServer(root), bin
}

func mirrorDomain(on bool) domain.Domain {
	raw, _ := domain.EncodeReleaseMirrorInto("", on)
	return domain.Domain{ID: "abc123", Host: mirrorTestHost, Status: domain.StatusActive, ConfigJSON: raw}
}

// withDomain stands in for domainMiddleware: it puts the resolved domain into
// the request context the way the real resolver does.
func withDomain(d domain.Domain) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxKeyDomain{}, d)))
		})
	}
}

// mirrorRouter is the production order: core chain, domain resolution, then
// the mirror middleware, with a sentinel standing in for everything routed
// after it.
func mirrorRouter(a *App, d domain.Domain) http.Handler {
	r := chi.NewRouter()
	r.Use(coreMiddleware()...)
	r.Use(withDomain(d))
	r.Use(a.releaseMirrorMiddleware)
	// chi assembles its middleware chain only once a route exists; without
	// one, every request goes straight to NotFound and no middleware runs.
	r.Get("/health", func(w http.ResponseWriter, _ *http.Request) {})
	r.NotFound(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-Reached", "site")
		w.WriteHeader(http.StatusTeapot)
	})
	return r
}

// A 56 MB binary does not cross a slow link in thirty seconds, and the hosts
// that need this mirror are exactly the ones with bad routes. Scaled down: a
// 300 ms WriteTimeout, and a client that reads nothing for 700 ms.
//
// The body has to outgrow what the kernel will buffer, or the server finishes
// writing into socket buffers before its deadline and the test passes against
// the defect — a 2 MB body did exactly that. Linux caps a socket's send buffer
// at 4 MB by default and grows the receiver's only as the application reads,
// so 16 MB leaves the server blocked mid-write when its deadline passes.
func TestAMirrorDownloadOutlivesTheServerWriteTimeout(t *testing.T) {
	s, bin := heldMirror(t, 16<<20)
	a := &App{relMirror: s}

	srv := httptest.NewUnstartedServer(mirrorRouter(a, mirrorDomain(true)))
	srv.Config.WriteTimeout = 300 * time.Millisecond
	srv.Start()
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/download/github/johalputt/VayuPress/releases/download/v3.17.66/vayupress", nil)
	req.Host = mirrorTestHost
	// Asked for explicitly so the gzip wrapper is in the chain, as it is for
	// any real client that advertises gzip.
	req.Header.Set("Accept-Encoding", "gzip")
	tr := &http.Transport{DisableCompression: true}
	defer tr.CloseIdleConnections()
	resp, err := (&http.Client{Transport: tr}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	time.Sleep(700 * time.Millisecond)
	got, err := io.ReadAll(resp.Body)
	if err != nil || len(got) != len(bin) {
		t.Fatalf("received %d of %d bytes (%v) — the server's write deadline cut the transfer off, "+
			"so every slow host falling back to the mirror fails mid-update", len(got), len(bin), err)
	}
}

// The mirror answers its paths only on a domain the operator switched it on
// for, and only for that domain's own hostname.
func TestTheMirrorAnswersOnlyWhereItIsSwitchedOn(t *testing.T) {
	s, _ := heldMirror(t, 16)
	a := &App{relMirror: s}
	const latest = "/api/github/repos/johalputt/VayuPress/releases/latest"

	primary := mirrorDomain(true)
	primary.IsPrimary = true
	disabled := mirrorDomain(true)
	disabled.Status = "disabled"

	cases := []struct {
		name   string
		d      domain.Domain
		host   string
		path   string
		mirror bool
	}{
		{"switched on", mirrorDomain(true), mirrorTestHost, latest, true},
		{"switched off", mirrorDomain(false), mirrorTestHost, latest, false},
		{"the primary, even with the flag", primary, mirrorTestHost, latest, false},
		{"a disabled domain", disabled, mirrorTestHost, latest, false},
		// domainMiddleware falls back to the primary for an unknown host; a
		// Host header that is not the mirror domain's must never be answered
		// as though it were.
		{"another hostname on the same request", mirrorDomain(true), "other.example.com", latest, false},
		{"not a mirror path", mirrorDomain(true), mirrorTestHost, "/download/something", false},
		{"the site's own pages", mirrorDomain(true), mirrorTestHost, "/", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "http://"+tc.host+tc.path, nil)
			rec := httptest.NewRecorder()
			mirrorRouter(a, tc.d).ServeHTTP(rec, req)
			answered := rec.Header().Get("X-Reached") == ""
			if answered != tc.mirror {
				t.Fatalf("mirror answered = %v, want %v (status %d)", answered, tc.mirror, rec.Code)
			}
			if tc.mirror && !strings.Contains(rec.Body.String(), `"tag_name":"v3.17.66"`) {
				t.Fatalf("mirror body: %s", rec.Body.String())
			}
		})
	}

	// A Tor Space makes no clearnet requests and serves no mirror.
	prev := config.Cfg.OnionMode
	config.Cfg.OnionMode = true
	defer func() { config.Cfg.OnionMode = prev }()
	rec := httptest.NewRecorder()
	mirrorRouter(a, mirrorDomain(true)).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "http://"+mirrorTestHost+latest, nil))
	if rec.Header().Get("X-Reached") != "site" {
		t.Fatal("a Tor Space answered the mirror protocol")
	}
}

// The shield's bypass predicate is the same function. It must not match
// anything but mirror paths on the mirror domain, or it disables bot protection
// for pages that were never meant to skip it.
func TestTheShieldBypassMatchesOnlyTheMirror(t *testing.T) {
	s, _ := heldMirror(t, 16)
	a := &App{relMirror: s}
	on := mirrorDomain(true)
	req := func(host, path string, d domain.Domain) *http.Request {
		r := httptest.NewRequest(http.MethodGet, "http://"+host+path, nil)
		return r.WithContext(context.WithValue(r.Context(), ctxKeyDomain{}, d))
	}
	if !a.shieldBypass(req(mirrorTestHost, "/download/github/johalputt/VayuPress/releases/download/v3.17.66/vayupress", on)) {
		t.Fatal("the fallback client's download would be challenged — it is a plain HTTP client and can never solve one")
	}
	for _, p := range []string{"/", "/blog/post", "/downloads", "/download", "/api/v1/articles", "/mirror", "/mirror/status"} {
		if a.shieldBypass(req(mirrorTestHost, p, on)) {
			t.Errorf("%s skipped the shield on the mirror domain", p)
		}
	}
	if a.isReleaseMirrorRequest(req(mirrorTestHost, "/mirror/status.json", mirrorDomain(false))) {
		t.Error("a domain with the mirror off skipped the shield on a mirror path")
	}
	if (&App{}).isReleaseMirrorRequest(req(mirrorTestHost, "/mirror/status.json", on)) {
		t.Error("with no mirror in the process, the predicate still matched")
	}
}

// Downloads are budgeted per client; the cheap JSON is not. The budget is
// asserted by its specific refusal, and a second client is unaffected.
func TestMirrorDownloadsAreBudgetedPerClient(t *testing.T) {
	s, _ := heldMirror(t, 16)
	a := &App{relMirror: s}
	h := mirrorRouter(a, mirrorDomain(true))
	get := func(ip, path string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodGet, "http://"+mirrorTestHost+path, nil)
		r.RemoteAddr = ip + ":40000"
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		return rec
	}
	const file = "/download/github/johalputt/VayuPress/releases/download/v3.17.66/vayupress"
	for i := 0; i < releaseMirrorDownloadsPerHour; i++ {
		if rec := get("198.51.100.7", file); rec.Code != http.StatusOK {
			t.Fatalf("download %d refused with %d inside the budget", i+1, rec.Code)
		}
	}
	rec := get("198.51.100.7", file)
	if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") == "" {
		t.Fatalf("download %d → %d, want 429 with Retry-After", releaseMirrorDownloadsPerHour+1, rec.Code)
	}
	if rec := get("198.51.100.8", file); rec.Code != http.StatusOK {
		t.Fatalf("another client was refused by the first client's budget: %d", rec.Code)
	}
	for i := 0; i < 3; i++ {
		if rec := get("198.51.100.7", "/api/github/repos/johalputt/VayuPress/releases/latest"); rec.Code != http.StatusOK {
			t.Fatalf("a check for updates was refused by the download budget: %d", rec.Code)
		}
	}
}

// When every transfer slot is taken the next download is shed with a retry
// hint rather than queued behind them.
func TestMirrorDownloadsAreShedWhenEverySlotIsBusy(t *testing.T) {
	s, _ := heldMirror(t, 16)
	for i := 0; i < releaseMirrorSlots; i++ {
		s.slots <- struct{}{}
	}
	a := &App{relMirror: s}
	rec := httptest.NewRecorder()
	mirrorRouter(a, mirrorDomain(true)).ServeHTTP(rec, httptest.NewRequest(http.MethodGet,
		"http://"+mirrorTestHost+"/download/github/johalputt/VayuPress/releases/download/v3.17.66/vayupress", nil))
	if rec.Code != http.StatusServiceUnavailable || rec.Header().Get("Retry-After") != "60" {
		t.Fatalf("with every slot busy → %d (Retry-After %q), want 503 with a retry hint", rec.Code, rec.Header().Get("Retry-After"))
	}
}

// The console row reports state while collapsed, and the card renders what the
// mirror holds and the operator-only error text.
func TestTheConsoleReportsTheMirrorsState(t *testing.T) {
	s, _ := heldMirror(t, 16)
	a := &App{relMirror: s}
	if got := a.releaseMirrorChip(mirrorDomain(true)); !strings.Contains(got, ">Serving v3.17.66<") {
		t.Errorf("chip = %s", got)
	}
	if got := a.releaseMirrorChip(mirrorDomain(false)); !strings.Contains(got, ">Off<") {
		t.Errorf("chip for a switched-off domain = %s", got)
	}
	card := a.releaseMirrorCard(mirrorDomain(true))
	for _, want := range []string{`id="release-mirror-on" checked`, "v3.17.66", "data-release-mirror-sync", `data-checked="20`} {
		if !strings.Contains(card, want) {
			t.Errorf("card lacks %q", want)
		}
	}
}

// An IPv6 client rotating addresses inside its own /64 is one client. Keyed on
// the exact address, a single routed prefix would be 2^64 fresh budgets.
func TestAnIPv6ClientsBudgetIsSharedAcrossItsPrefix(t *testing.T) {
	s, _ := heldMirror(t, 16)
	a := &App{relMirror: s, vayuShield: vayushield.New(vayushield.Config{Enabled: true})}
	h := mirrorRouter(a, mirrorDomain(true))
	const file = "/download/github/johalputt/VayuPress/releases/download/v3.17.66/vayupress"
	code := func(ip string) int {
		r := httptest.NewRequest(http.MethodGet, "http://"+mirrorTestHost+file, nil)
		r.RemoteAddr = "[" + ip + "]:40000"
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		return rec.Code
	}
	for i := 0; i < releaseMirrorDownloadsPerHour; i++ {
		if c := code(fmt.Sprintf("2001:db8:1:1::%x", i+1)); c != http.StatusOK {
			t.Fatalf("download %d → %d inside the budget", i+1, c)
		}
	}
	if c := code("2001:db8:1:1::ffff"); c != http.StatusTooManyRequests {
		t.Fatalf("a fresh address in the same /64 → %d, want 429 — rotating addresses resets the budget", c)
	}
	if c := code("2001:db8:1:2::1"); c != http.StatusOK {
		t.Fatalf("a different /64 was refused: %d", c)
	}
}

// The console's mirror controls are the operator's. A client login bound to the
// mirror domain — or no login at all — may not switch it, sync it, or read its
// status, which carries error text naming this machine's addresses and paths.
func TestTheMirrorControlsRefuseAnyoneButTheOperator(t *testing.T) {
	s, _ := heldMirror(t, 16)
	a := &App{relMirror: s}
	client := &users.User{ID: "c1", Email: "owner@updates.example.com", Role: users.RoleClient, ClientDomainID: "abc123"}
	for _, h := range []struct {
		name string
		fn   http.HandlerFunc
		body string
	}{
		{"switch", a.handleOSDomainReleaseMirror, `{"on":true}`},
		{"sync now", a.handleOSDomainReleaseMirrorSync, ``},
		{"status", a.handleOSReleaseMirrorStatus, ``},
	} {
		for _, who := range []struct {
			name string
			u    *users.User
		}{{"anonymous", nil}, {"the domain's own client", client}} {
			req := httptest.NewRequest(http.MethodPost, "/os/api/domains/abc123/release-mirror", strings.NewReader(h.body))
			if who.u != nil {
				req = withUser(req, who.u)
			}
			rec := httptest.NewRecorder()
			h.fn(rec, req)
			if rec.Code != http.StatusForbidden {
				t.Errorf("%s by %s → %d, want 403", h.name, who.name, rec.Code)
			}
			if strings.Contains(rec.Body.String(), "v3.17.66") {
				t.Errorf("%s by %s leaked the mirror's state", h.name, who.name)
			}
		}
	}
}

// Sync now on a domain whose mirror is off used to download and verify ~120 MB
// that nothing served, while the card went on reading "off". The server refuses
// it with the reason, and the button renders disabled.
func TestSyncNowIsRefusedWhileTheMirrorIsOff(t *testing.T) {
	openMigratedDB(t)
	s, _ := heldMirror(t, 16)
	reg := domain.New(dbpkg.DB, dbpkg.RDB)
	d, err := reg.Create(context.Background(), "updates.example.com", domain.SiteBlog, false)
	if err != nil {
		t.Fatal(err)
	}
	a := &App{relMirror: s, domains: reg}

	sync := func() *httptest.ResponseRecorder {
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("id", d.ID)
		req := httptest.NewRequest(http.MethodPost, "/os/api/domains/"+d.ID+"/release-mirror/sync", nil)
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
		req.Header.Set("X-API-Key", "test-key")
		rec := httptest.NewRecorder()
		a.handleOSDomainReleaseMirrorSync(rec, req)
		return rec
	}
	rec := sync()
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "Switch the mirror on") {
		t.Fatalf("sync with the mirror off → %d %s, want 409 naming the switch", rec.Code, rec.Body.String())
	}
	off, _ := reg.ByID(context.Background(), d.ID)
	if card := a.releaseMirrorCard(off); !strings.Contains(card, `data-release-mirror-sync disabled`) {
		t.Error("the Sync now button is live on a domain whose mirror is off")
	}

	if err := reg.SetReleaseMirror(context.Background(), d.ID, true); err != nil {
		t.Fatal(err)
	}
	// Accepting the sync with the switch on is not exercised here: it starts a
	// real fetch from GitHub, which has no place in the unit suite.
	on, _ := reg.ByID(context.Background(), d.ID)
	if card := a.releaseMirrorCard(on); strings.Contains(card, `data-release-mirror-sync disabled`) {
		t.Error("the Sync now button is disabled on a domain whose mirror is on")
	}
}
