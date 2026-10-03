// SPDX-License-Identifier: Apache-2.0

package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"
)

// The mailbox recovery pages were the old glass card on a gradient
// (signup.css) while the sign-in they are reached from, and return to, is
// Still Air. Every page the flow can show, in its error and partial-cleanup
// states too, is the sign-in's own shell, and every class it uses is one the
// console stylesheet defines: read from the stylesheet itself, not from the
// code that renders the page.
func TestEveryRecoveryPageIsTheSignInsStillAir(t *testing.T) {
	css, err := os.ReadFile("../../static/css/vayuos.css")
	if err != nil {
		t.Fatal(err)
	}
	defined := map[string]bool{}
	for _, m := range regexp.MustCompile(`\.(-?[A-Za-z_][A-Za-z0-9_-]*)`).FindAllStringSubmatch(regexp.MustCompile(`(?s)/\*.*?\*/`).ReplaceAllString(string(css), ""), -1) {
		defined[m[1]] = true
	}
	pages := map[string]string{
		"send a link":   recoveryFormPage("That address is not valid."),
		"link sent":     recoverySentPage(),
		"new password":  recoveryPasswordFormPage("tok", "The two passwords differ."),
		"recovery code": recoveryCodeFormPage("a@b.c", "That code did not work."),
		"done":          recoveryDonePage("a@b.c", mailResetOutcome{AppPasswordsRevoked: 2, SessionsRevoked: 1, QueueHeld: 1, NotifiedContact: "x@y.z", Problems: []string{"p"}}),
		"notice":        recoveryNoticePage("Link expired", "Ask for a new one."),
		"ask":           recoveryAskFormPage("a@b.c", "Enter your address."),
		"asked":         recoveryAskedPage(),
	}
	classAttr := regexp.MustCompile(`class="([^"]*)"`)
	for name, card := range pages {
		w := httptest.NewRecorder()
		(&App{}).renderRecoveryPage(w, httptest.NewRequest(http.MethodGet, "/mail/recover", nil), card)
		body := w.Body.String()
		if !strings.Contains(body, `data-ui="still-air"`) || !strings.Contains(body, "/os/static/css/vayuos.css") || !strings.Contains(body, `class="login-card"`) {
			t.Errorf("%s: not the sign-in's Still Air shell", name)
		}
		if strings.Contains(body, "signup.css") {
			t.Errorf("%s: still loads the old stylesheet", name)
		}
		for _, m := range classAttr.FindAllStringSubmatch(body, -1) {
			for _, c := range strings.Fields(m[1]) {
				if !defined[c] {
					t.Errorf("%s: class %q is not in the console stylesheet", name, c)
				}
			}
		}
	}
}
