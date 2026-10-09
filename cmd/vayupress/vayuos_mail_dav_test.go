// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/johalputt/vayupress/internal/auth"
	"github.com/johalputt/vayupress/internal/users"
)

// davServer is the whole route table over a mailbox, dana@example.com, with
// one app password.
func davServer(t *testing.T, a *App) *httptest.Server {
	t.Helper()
	router := chi.NewRouter()
	a.registerRoutes(router, t.TempDir())
	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)
	return srv
}

func davPropfind(t *testing.T, srv *httptest.Server, path, user, password string) (int, http.Header, string) {
	t.Helper()
	r, _ := http.NewRequest("PROPFIND", srv.URL+path, strings.NewReader(`<?xml version="1.0"?><D:propfind xmlns:D="DAV:"><D:prop><D:current-user-principal/></D:prop></D:propfind>`))
	r.Header.Set("Content-Type", "application/xml")
	r.Header.Set("Depth", "0")
	if user != "" {
		r.SetBasicAuth(user, password)
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, resp.Header, string(b)
}

// An app reaches the address book through the router itself, which must
// know PROPFIND, and signs in as a mail app does: with an app password,
// never the mailbox's own password while the mailbox wants approved devices.
func TestContactsSyncSignsInAsMailAppsDo(t *testing.T) {
	a := appWithMailAccounts(t)
	hash, err := auth.HashSecretArgon2id("phone-app-pass")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.vayuMail.Accounts().CreateAppPassword(context.Background(), "dana@example.com", "Phone", hash); err != nil {
		t.Fatal(err)
	}
	srv := davServer(t, a)

	for _, user := range []string{"dana@example.com", "Dana", " dana "} {
		code, _, body := davPropfind(t, srv, "/dav/card/", user, "phone-app-pass")
		if code != http.StatusMultiStatus || !strings.Contains(body, "/dav/card/dana@example.com/") {
			t.Errorf("%q with its app password: %d\n%s", user, code, body)
		}
	}
	code, h, _ := davPropfind(t, srv, "/.well-known/carddav", "", "")
	if code != http.StatusMovedPermanently || h.Get("Location") != "/dav/card/" {
		t.Fatalf("well-known: %d %q", code, h.Get("Location"))
	}
	for name, c := range map[string]struct{ user, password string }{
		"the mailbox password": {"dana@example.com", "main-mailbox-pass"},
		"no such mailbox":      {"nobody@example.com", "phone-app-pass"},
	} {
		if code, _, _ := davPropfind(t, srv, "/dav/cal/", c.user, c.password); code != http.StatusUnauthorized {
			t.Errorf("%s: %d", name, code)
		}
	}
}

// With Mail not running there is nothing at /dav.
func TestNoSyncWithoutMail(t *testing.T) {
	srv := davServer(t, &App{})
	if code, _, _ := davPropfind(t, srv, "/dav/card/", "dana@example.com", "x"); code != http.StatusNotFound {
		t.Fatalf("%d", code)
	}
}

// A phone cannot answer a challenge, and keeps syncing while the site is
// down for maintenance.
func TestSyncIsNeitherChallengedNorTakenDown(t *testing.T) {
	if !containsPrefix(shieldBypassPrefixes, "/dav") {
		t.Error("/dav is not in shieldBypassPrefixes")
	}
	if !maintenancePathExempt("/dav/card/dana@example.com/") {
		t.Error("/dav is not exempt from maintenance")
	}
}

// Connect gives the sync addresses on the site's own address, as the browser
// reached it.
func TestConnectGivesTheSyncAddresses(t *testing.T) {
	a := appWithMailAccounts(t)
	admin := &users.User{ID: "admin1", Email: "boss@example.com", Role: users.RoleAdmin}
	req := httptest.NewRequest(http.MethodGet, "https://mail.example.com/os/vayumail/connect", nil)
	req.Header.Set("X-Forwarded-Proto", "https")
	rec := httptest.NewRecorder()
	a.handleVayuOSConnect(rec, withUser(req, admin))
	for _, want := range []string{">https://mail.example.com<", ">https://mail.example.com/dav/card/<", ">https://mail.example.com/dav/cal/<"} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Errorf("Connect does not give %s", want)
		}
	}
}
