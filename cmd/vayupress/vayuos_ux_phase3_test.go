// SPDX-License-Identifier: Apache-2.0

package main

// vayuos_ux_phase3_test.go — regression pins for the Phase 3 structural work
// (docs/UX-AUDIT-2026-08-VAYUMAIL-VAYUTALK.md). The behaviour itself is pinned in
// internal/vayuos/vayutalk/web_cursor_test.go; these check the console is actually
// wired to it, because a correct engine with an unwired client fixes nothing.

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestTheConsoleUsesItsOwnReadCursor pins the ends of the cursor wire.
func TestTheConsoleUsesItsOwnReadCursor(t *testing.T) {
	page := withoutComments(readFileString(t, "vayuos_talk.go"))
	if !strings.Contains(page, "SubscribeWeb(self)") {
		t.Error("the console stream must use the web cursor, or a reconnect keeps re-flushing burned messages")
	}
	if !strings.Contains(page, "MarkWebReadAs(id, self)") {
		t.Error("the clearnet read signal must record the console's cursor")
	}
	// The whole point is that it does NOT destroy the app's copy: no ack on the
	// clearnet path.
	clearnet := page[strings.Index(page, "if !config.Cfg.OnionMode {"):]
	if end := strings.Index(clearnet, "\n\t}"); end > 0 {
		clearnet = clearnet[:end]
	}
	if strings.Contains(clearnet, "Ack") {
		t.Error("the clearnet console must never read-destroy — the phone app is the authoritative reader")
	}

	js := withoutComments(talkJS(t))
	if strings.Contains(js, "if (!onionWorld || !m") {
		t.Error("signalRead must run in the clearnet world too, or the cursor is never set")
	}
	if !strings.Contains(js, "function signalRead(") {
		t.Error("the read signal is missing")
	}
}

// TestTheMailAndTalkSurfacesUseTheConsolesOwnDialogs — window.prompt/confirm are
// unstyled, block the whole tab, and ignore the console's Escape/focus
// conventions, so the two apps were teaching operators two different products.
//
// window.alert is allowed exactly once: acctToast's documented fallback for a
// page where the shell's toast is somehow absent. Anything beyond that is a
// regression.
func TestTheMailAndTalkSurfacesUseTheConsolesOwnDialogs(t *testing.T) {
	for _, f := range []string{"admin-os-mail.js", "admin-os-talk.js"} {
		src := withoutComments(readFileString(t, filepath.Join("..", "..", "static", "js", f)))
		for _, banned := range []string{"window.prompt", "window.confirm"} {
			if strings.Contains(src, banned) {
				t.Errorf("%s still calls %s — use vpConfirm or a console modal", f, banned)
			}
		}
		if n := strings.Count(src, "window.alert"); n > 1 {
			t.Errorf("%s calls window.alert %d times; only the toast fallback may", f, n)
		}
	}
}

// TestTheCursorDoesNotReplaceTheClientTombstone keeps both layers: the server
// stops re-flushing, and the client still ignores an id it has already shown, so
// a signal lost in flight cannot resurrect a burned message.
func TestTheCursorDoesNotReplaceTheClientTombstone(t *testing.T) {
	js := withoutComments(talkJS(t))
	if !strings.Contains(js, "if (m.id && byId[m.id]) return") {
		t.Error("the client must still dedupe by id — the server cursor is the primary fix, not the only one")
	}
	if !strings.Contains(js, "m.expired = true") {
		t.Error("expired ids must still be remembered as tombstones")
	}
}
