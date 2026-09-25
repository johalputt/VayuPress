// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/johalputt/vayupress/internal/config"
	"github.com/johalputt/vayupress/internal/settings"
)

var settingKeyAttr = regexp.MustCompile(`data-setting-key="([^"]*)"`)

// Every control a Settings page draws saves to a key the store keeps. The
// store's SetMany skips a key it does not know and returns nil, so a control
// wired to one reported "Saved" and stored nothing: members.enabled,
// members.magic_link and smtp.from shipped that way. The keys are read from
// the rendered page, not from the declarations, so the menu and footer
// editors (whose keys are in their markup) are held too.
func TestEverySettingsControlSavesToAKeyTheStoreKeeps(t *testing.T) {
	seen := 0
	for _, c := range settingsCategories {
		body := string(settingsPageBody(t.Context(), nil, c))
		for _, m := range settingKeyAttr.FindAllStringSubmatch(body, -1) {
			seen++
			if !settings.AllKeys[m[1]] {
				t.Errorf("Settings › %s draws a control for %q, which the settings store drops", c.Label, m[1])
			}
		}
	}
	if seen == 0 {
		t.Fatal("no Settings page drew a single control; the pattern no longer matches the markup")
	}
}

// A search result links to #id on its category's page, so an id used twice
// would land on the wrong row.
func TestEverySettingsRowHasItsOwnAnchor(t *testing.T) {
	where := map[string]string{}
	for _, c := range settingsCategories {
		for _, g := range c.Groups {
			for _, f := range g.Fields {
				if f.ID == "" {
					t.Errorf("Settings › %s › %s has no id for search to land on", c.Label, f.Label)
				}
				if prev, dup := where[f.ID]; dup {
					t.Errorf("id %q is both %s and %s › %s", f.ID, prev, c.Label, f.Label)
				}
				where[f.ID] = c.Label + " › " + f.Label
			}
		}
	}
}

// The settings API refuses a key the store would drop, instead of answering
// "ok" for a write that never happened, and still takes a key it keeps.
func TestTheSettingsAPIRefusesAKeyTheStoreWouldDrop(t *testing.T) {
	a := newIconApp(t, nil)
	post := func(key string) (int, string) {
		t.Helper()
		body, _ := json.Marshal(map[string]string{"key": key, "value": "x"})
		rec := httptest.NewRecorder()
		a.handleOSSettingsAPI(rec, httptest.NewRequest(http.MethodPost, "/os/api/settings", strings.NewReader(string(body))))
		var e struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &e)
		return rec.Code, e.Error.Code
	}

	if code, reason := post("members.magic_link"); code != http.StatusBadRequest || reason != "unknown-setting" {
		t.Errorf("an unknown key: %d %q, want 400 unknown-setting", code, reason)
	}
	if code, _ := post(settings.KeySiteTagline); code != http.StatusOK {
		t.Fatalf("a known key: %d, want 200", code)
	}
	if got := a.siteSettings.Get(t.Context(), settings.ForPrimary(), settings.KeySiteTagline); got != "x" {
		t.Errorf("a known key answered ok but stored %q", got)
	}
}

// A category page marks its own category current, not the front page: the
// shell locates the section from the matched route, which for every category
// is the one pattern /os/settings/{group}.
func TestASettingsCategoryIsTheCurrentSection(t *testing.T) {
	r := chi.NewRouter()
	var got string
	r.Get("/os/settings/{group}", func(_ http.ResponseWriter, r *http.Request) {
		_, sec := saLocate(saClearnetApps, "settings", resolvedRoute(chi.RouteContext(r.Context())))
		if sec != nil {
			got = sec.Label
		}
	})
	r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/os/settings/appearance", nil))
	if got != "Appearance" {
		t.Errorf("/os/settings/appearance marks %q current, want Appearance", got)
	}
}

// Email delivery says how this install's mail actually leaves it. It once said
// "your site sends no email of its own" on an install that sends through the
// built-in engine whenever SMTP_HOST is unset and DOMAIN is set.
func TestEmailDeliverySaysWhichTransportSends(t *testing.T) {
	cfg := config.Cfg
	t.Cleanup(func() { config.Cfg = cfg })
	facts := func(a *App) string { return string(settingsEmailFacts(t.Context(), a)) }

	config.Cfg.SMTPHost, config.Cfg.SMTPPort, config.Cfg.SMTPUsername = "smtp.example.net", 587, ""
	if got := facts(nil); !strings.Contains(got, "smtp.example.net:587") || !strings.Contains(got, "without a sign-in") {
		t.Errorf("with SMTP_HOST set and no username, the page reads:\n%s", got)
	}

	config.Cfg.SMTPHost = ""
	engine, _ := deleteWiringApp(t)
	if got := facts(engine); !strings.Contains(got, "own mail engine, VayuMail") || !strings.Contains(got, "noreply@example.com") {
		t.Errorf("with VayuMail enabled and no SMTP_HOST, the page reads:\n%s", got)
	}

	if got := facts(&App{}); !strings.Contains(got, "Nothing: sign-in links and receipts cannot be sent") {
		t.Errorf("with neither, the page reads:\n%s", got)
	}
}
