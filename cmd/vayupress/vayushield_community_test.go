// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/johalputt/vayupress/internal/settings"
	"github.com/johalputt/vayupress/internal/vayushield/community"
)

const communityAttacker = "185.220.101.7"

func communityApp(t *testing.T, hub bool) *App {
	t.Helper()
	a, _ := shieldSitesApp(t)
	v := "off"
	if hub {
		v = "on"
	}
	if err := a.siteSettings.SetMany(context.Background(), settings.ForPrimary(), map[string]string{settings.KeyShieldCommunityHub: v}); err != nil {
		t.Fatal(err)
	}
	return a
}

// postReport sends one install's signed report to the hub, from `from`.
func postReport(t *testing.T, a *App, priv ed25519.PrivateKey, from string, addrs ...string) int {
	t.Helper()
	body, sig := community.SignReport(priv, time.Now(), addrs)
	r := httptest.NewRequest(http.MethodPost, "/api/v1/community/report", bytes.NewReader(body))
	r.Header.Set("X-VayuShield-Signature", sig)
	r.RemoteAddr = from + ":40000"
	rec := httptest.NewRecorder()
	a.handleCommunityReport(rec, r)
	return rec.Code
}

func hubGet(a *App, path string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, path, nil)
	if strings.HasSuffix(path, "/list") {
		a.handleCommunityList(rec, r)
	} else {
		a.handleCommunityKey(rec, r)
	}
	return rec
}

// Three installs on three networks report an address; the hub serves it in
// a list a reader verifies against the key it pinned on its first read, and
// a list signed by any other key is refused from then on.
func TestAnAddressThreeInstallsReportReachesAReader(t *testing.T) {
	hub := communityApp(t, true)
	for _, from := range []string{"9.9.1.1", "8.8.2.2", "7.7.3.3"} {
		_, priv, _ := ed25519.GenerateKey(nil)
		if code := postReport(t, hub, priv, from, communityAttacker); code != http.StatusNoContent {
			t.Fatalf("a report from %s was answered %d", from, code)
		}
	}
	list := hubGet(hub, "/api/v1/community/list")
	key := hubGet(hub, "/api/v1/community/key")
	if list.Code != http.StatusOK || key.Code != http.StatusOK {
		t.Fatalf("the hub answered %d for its list and %d for its key", list.Code, key.Code)
	}

	reader := communityApp(t, false)
	old := communityGet
	communityGet = func(_ context.Context, target string) ([]byte, error) {
		if target != "https://johal.in/api/v1/community/key" {
			t.Errorf("the key was asked of %s", target)
		}
		return key.Body.Bytes(), nil
	}
	t.Cleanup(func() { communityGet = old })
	got, err := reader.parseCommunityList(list.Body.Bytes())
	if err != nil || len(got) != 1 || got[0].Addr().String() != communityAttacker {
		t.Fatalf("the reader read %v, %v", got, err)
	}
	if pin := reader.communitySetting(context.Background(), settings.KeyShieldCommunityHubKey); !strings.HasPrefix(pin, "https://johal.in ") {
		t.Fatalf("the hub's key was not pinned: %q", pin)
	}
	_, other, _ := ed25519.GenerateKey(nil)
	communityGet = func(context.Context, string) ([]byte, error) {
		t.Error("a pinned key was asked for again")
		return nil, nil
	}
	if _, err := reader.parseCommunityList(community.SignList(other, time.Now(), []string{"185.220.101.9"})); err == nil {
		t.Fatal("a list signed by another key was read")
	}
}

// An install that is not a hub answers none of the hub's paths.
func TestAnInstallThatIsNotAHubAnswersNothing(t *testing.T) {
	a := communityApp(t, false)
	_, priv, _ := ed25519.GenerateKey(nil)
	if code := postReport(t, a, priv, "9.9.1.1", communityAttacker); code != http.StatusNotFound {
		t.Errorf("a report was answered %d", code)
	}
	for _, p := range []string{"/api/v1/community/list", "/api/v1/community/key"} {
		if code := hubGet(a, p).Code; code != http.StatusNotFound {
			t.Errorf("%s was answered %d", p, code)
		}
	}
}

// With reporting on, this install's own bans of public addresses are sent,
// signed, and taken off the queue once the hub took them; a private
// address is never queued, a hub that says "later" keeps them queued, and
// with reporting off nothing is sent.
func TestThisInstallsBansAreReportedSigned(t *testing.T) {
	a := communityApp(t, false)
	a.community = &communityQueue{addrs: map[string]bool{}}
	var sent [][]byte
	status := http.StatusTooManyRequests
	old := communityPost
	communityPost = func(_ context.Context, target string, body []byte, sig string) (int, error) {
		if target != "https://johal.in/api/v1/community/report" {
			t.Errorf("reported to %s", target)
		}
		if _, _, err := community.VerifyReport(body, sig, time.Now()); err != nil {
			t.Errorf("the report does not verify: %v", err)
		}
		sent = append(sent, body)
		return status, nil
	}
	t.Cleanup(func() { communityPost = old })
	a.noteCommunityBan("10.1.2.3")
	a.noteCommunityBan(communityAttacker + ":5555")
	ctx := context.Background()
	a.reportCommunity(ctx, time.Now())
	if len(sent) != 0 {
		t.Fatal("with reporting off, a report was sent")
	}
	if err := a.siteSettings.SetMany(ctx, settings.ForPrimary(), map[string]string{settings.KeyShieldCommunityReport: "on"}); err != nil {
		t.Fatal(err)
	}
	a.reportCommunity(ctx, time.Now())
	if len(sent) != 1 || !bytes.Contains(sent[0], []byte(`"`+communityAttacker+`"`)) || bytes.Contains(sent[0], []byte("10.1.2.3")) {
		t.Fatalf("sent %q", sent)
	}
	status = http.StatusNoContent
	a.reportCommunity(ctx, time.Now())
	if len(sent) != 2 {
		t.Fatal("an address the hub asked to wait for was not sent again")
	}
	a.reportCommunity(ctx, time.Now())
	if len(sent) != 2 {
		t.Fatal("an address the hub took was sent again")
	}
}

// The form saves a hub only as an https origin.
func TestTheHubIsSavedOnlyAsAnHTTPSOrigin(t *testing.T) {
	bs := func(string) string { return "off" }
	for in, want := range map[string]string{
		"https://hub.example/":          "https://hub.example",
		"http://hub.example":            "",
		"https://hub.example/api?x=1":   "",
		"javascript:alert(1)":           "",
		"https://hub.example:8443":      "https://hub.example:8443",
		"https://user@hub.example/path": "",
	} {
		r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(url.Values{"sh_comm_hub_url": {in}}.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if got := postedCommunity(r, bs)[settings.KeyShieldCommunityHubURL]; got != want {
			t.Errorf("%q was saved as %q, want %q", in, got, want)
		}
	}
}

// An address SSH protection bans is queued for the hub.
func TestAnSSHBanIsQueuedForTheHub(t *testing.T) {
	a := communityApp(t, false)
	a.community = &communityQueue{addrs: map[string]bool{}}
	a.sshWatch = newSSHWatch()
	now := time.Now()
	a.sshWatch.mu.Lock()
	for i := 0; i < sshFailures; i++ {
		a.noteSSHFailureLocked(strconv.FormatInt(now.Unix(), 10)+" "+communityAttacker+" root", now)
	}
	a.sshWatch.mu.Unlock()
	if !a.community.addrs[communityAttacker] {
		t.Fatalf("an SSH ban was not queued: %v", a.community.addrs)
	}
}
