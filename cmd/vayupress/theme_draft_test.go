// SPDX-License-Identifier: Apache-2.0

package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/settings"
)

// A Theme Studio draft is actually kept, and a discard actually clears it. The
// endpoint answered "ok" for its whole life while the settings store dropped
// the key it wrote under, so no draft was ever saved and the resume banner had
// nothing to offer.
func TestAThemeStudioDraftIsKeptAndDiscarded(t *testing.T) {
	a := newIconApp(t, nil)
	post := func(body string) int {
		t.Helper()
		rec := httptest.NewRecorder()
		a.handleThemeDraftSave(rec, httptest.NewRequest(http.MethodPost, "/os/api/theme/draft", strings.NewReader(body)))
		return rec.Code
	}
	stored := func() string {
		return a.siteSettings.Get(t.Context(), settings.ForPrimary(), settings.KeyThemeStudioDraft)
	}
	if code := post(`{"draft":"eyJtIjp7fX0="}`); code != http.StatusOK {
		t.Fatalf("saving a draft answered %d", code)
	}
	if got := stored(); got != "eyJtIjp7fX0=" {
		t.Fatalf("a saved draft reads back as %q; the store dropped it", got)
	}
	if got := themeDraftFor(map[string]string{settings.KeyThemeStudioDraft: stored()}); got == "" {
		t.Error("the Theme Studio page would not offer the saved draft")
	}
	if code := post(`{"discard":true}`); code != http.StatusOK || stored() != "" {
		t.Errorf("a discard answered %d and left %q", code, stored())
	}
	// A draft is this install's unapplied work, never part of a theme bundle.
	if !settings.NotPortable[settings.KeyThemeStudioDraft] {
		t.Error("the draft key is portable, so an exported theme would carry it")
	}
}
