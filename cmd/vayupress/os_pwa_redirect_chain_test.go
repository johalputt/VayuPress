// SPDX-License-Identifier: Apache-2.0

//go:build integration

package main

import (
	"net/http"
	"net/http/cookiejar"
	"strings"
	"testing"
)

// walkRedirects follows the chain from path and returns every URL visited, in
// order, stopping when the server stops redirecting or the chain exceeds max.
// It is the browser's view: same cookie jar throughout, GET only.
func walkRedirects(t *testing.T, base, path string, jar http.CookieJar, max int) []string {
	t.Helper()
	var chain []string
	c := &http.Client{
		Jar: jar,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			chain = append(chain, req.URL.Path)
			if len(via) >= max {
				return http.ErrUseLastResponse
			}
			return nil
		},
	}
	resp, err := c.Get(base + path)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer resp.Body.Close()
	return chain
}

// TestOSStartURLDoesNotLoop is the regression guard for the installed-PWA report:
// opening the VayuOS app (start_url "/os/") produced ERR_TOO_MANY_REDIRECTS until
// the user cleared cookies. The start URL must settle, signed in or not.
func TestOSStartURLDoesNotLoop(t *testing.T) {
	srv, _ := newTestHarness(t)

	for _, tc := range []struct {
		name string
		jar  func() http.CookieJar
	}{
		{"no cookie at all", func() http.CookieJar {
			j, _ := cookiejar.New(nil)
			return j
		}},
		{"stale session cookie", func() http.CookieJar {
			j, _ := cookiejar.New(nil)
			u, _ := http.NewRequest("GET", srv.URL, nil)
			j.SetCookies(u.URL, []*http.Cookie{{Name: "vp_session", Value: "not-a-real-token"}})
			return j
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			chain := walkRedirects(t, srv.URL, "/os/", tc.jar(), 12)
			t.Logf("chain from /os/ : %s", strings.Join(chain, " -> "))
			if len(chain) >= 12 {
				t.Fatalf("redirect loop from the PWA start URL: %s", strings.Join(chain, " -> "))
			}
		})
	}
}
