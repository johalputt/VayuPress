// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/johalputt/vayupress/internal/i18n"
	"github.com/johalputt/vayupress/internal/youtube"
)

// The YouTube page is drawn in its reader's language, while what came from
// YouTube is not translated: a video's title is marked as such, and the
// reason a check failed is what the check recorded.
func TestTheYouTubePageIsDrawnInThePagesLanguage(t *testing.T) {
	a := youtubeTestApp(t)
	ctx := context.Background()
	f, _ := a.youtube.Follow(ctx, "", youtube.Channel{ID: ytChannel, Title: "Vayu Videos"}, false, false, true)
	_, _ = a.youtube.Claim(ctx, "", f.ID, youtube.Video{ID: "hhhhhhhhhhh", Title: "Held one", Kind: youtube.KindVideo, Published: time.Now()}, youtube.StateHeld)
	_ = a.youtube.Checked(ctx, f, 0, errors.New("YouTube answered 500"))

	body := a.youtubeBody(i18n.WithLang(ctx, "de"))
	for _, want := range []string{"1 Kanal gefolgt", "Jetzt abrufen", "Warten aufs Posten", "Alle posten", "Einem Kanal folgen", `aria-label="Schließen"`,
		"Fügen Sie einen YouTube-API-Schlüssel hinzu", "So funktioniert", `<span translate="no">Held one</span>`, `translate="no">YouTube answered 500`} {
		if !strings.Contains(body, want) {
			t.Errorf("the German page lacks %q", want)
		}
	}
	for _, english := range []string{"Fetch now", "Waiting for Post", "Follow a channel", "Last check", "Add a YouTube API key"} {
		if strings.Contains(body, english) {
			t.Errorf("the German page still says %q", english)
		}
	}
}

// What the follow sheet answers, and what its actions refuse, is in the
// language of the page that asked.
func TestTheYouTubePageRepliesInThePagesLanguage(t *testing.T) {
	a := youtubeTestApp(t)
	ask := func(h http.HandlerFunc, path string, form url.Values) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.Header.Set(pageLangHeader, "de")
		w := httptest.NewRecorder()
		consoleLanguage(h).ServeHTTP(w, r)
		return w
	}
	if got := ask(a.handleOSYouTubeResolve, "/os/api/youtube/resolve", url.Values{"address": {"youtube.com/@vayu"}, "site": {"nowhere"}}).Body.String(); !strings.Contains(got, "Wählen Sie eine Website, auf der gepostet wird.") {
		t.Errorf("the follow sheet answers a German page with %q", got)
	}
	var out struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	w := ask(a.handleOSYouTubeFollow, "/os/api/youtube/follow", url.Values{"channel": {"UC../../x"}, "site": {""}})
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	if out.Error.Message != "Das ist kein Kanal." {
		t.Errorf("the refusal to a German page reads %q", w.Body.String())
	}
}
