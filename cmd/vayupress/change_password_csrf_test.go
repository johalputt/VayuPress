// SPDX-License-Identifier: Apache-2.0

package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/auth"
)

func TestChangePasswordPageReusesValidCSRFToken(t *testing.T) {
	firstReq := httptest.NewRequest(http.MethodGet, "/os/change-password", nil)
	firstRec := httptest.NewRecorder()
	(&App{}).handleOSChangePassword(firstRec, firstReq)

	var token string
	for _, c := range firstRec.Result().Cookies() {
		if c.Name == "vp_csrf" {
			token = c.Value
			break
		}
	}
	if token == "" || !auth.ValidateCSRFToken(token, "") {
		t.Fatal("first response did not issue a valid CSRF cookie")
	}
	if !strings.Contains(firstRec.Body.String(), `name="csrf_token" value="`+token+`"`) {
		t.Fatal("first form token does not match its CSRF cookie")
	}

	secondReq := httptest.NewRequest(http.MethodGet, "/os/change-password", nil)
	secondReq.AddCookie(&http.Cookie{Name: "vp_csrf", Value: token})
	secondRec := httptest.NewRecorder()
	(&App{}).handleOSChangePassword(secondRec, secondReq)

	if !strings.Contains(secondRec.Body.String(), `name="csrf_token" value="`+token+`"`) {
		t.Fatal("second page load rotated a valid token and invalidated the first form")
	}
	for _, c := range secondRec.Result().Cookies() {
		if c.Name == "vp_csrf" && c.Value != token {
			t.Fatalf("second page load changed CSRF cookie: got %q, want existing token", c.Value)
		}
	}
}
