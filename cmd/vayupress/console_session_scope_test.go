// SPDX-License-Identifier: Apache-2.0

package main

import (
	"strings"
	"testing"
)

// S8 — the console identity must cost nothing on a hostname that cannot carry it.
//
// resolveConsoleUser runs on PUBLIC pages, not just admin ones: the site nav
// calls it to show "your account" instead of "Sign in", and resolveCommenter
// calls it to treat a signed-in operator as a member-equivalent with full access
// to gated content.
//
// vp_session is host-only (see internal/auth/session_host_binding_test.go), so
// on a client's hostname the cookie is simply absent and both of those resolve
// to nil. That is the correct answer, and it must be reached WITHOUT a database
// round trip: this runs on every public request on every hosted domain, and a
// per-pageview session query on thirty client sites — to answer a question whose
// answer is always no there — is a cost paid for nothing.
func TestResolvingAConsoleUserCostsNothingWithoutACookie(t *testing.T) {
	body := goFuncBody(readSourceFile(t, "handlers_auth.go"), "resolveConsoleUser")
	if body == "" {
		t.Fatal("resolveConsoleUser is gone")
	}
	guard := strings.Index(body, `token == ""`)
	query := strings.Index(body, "a.sessions.Validate(")
	if guard < 0 {
		t.Fatal("resolveConsoleUser no longer returns early when the request carries no session " +
			"cookie, so every public pageview on every hosted domain now runs a session query " +
			"to establish something the absent cookie already answered")
	}
	if query < 0 {
		t.Fatal("resolveConsoleUser no longer validates the token at all")
	}
	if guard > query {
		t.Error("the empty-token guard runs AFTER the session lookup, so the lookup happens anyway")
	}
}

// The operator-as-member acceptance in resolveCommenter is host-blind by
// construction — it asks who is signed in, not where. That is safe only because
// the cookie cannot arrive on another host. Recording the dependency here so a
// change to the cookie's scope is understood to reach further than login: a
// domain-wide session would silently grant an operator paid-member access to
// gated content on every site the widened cookie reaches.
func TestTheOperatorAsMemberPathDependsOnTheCookieNotTravelling(t *testing.T) {
	body := goFuncBody(readSourceFile(t, "handlers_members.go"), "resolveCommenter")
	if body == "" {
		t.Fatal("resolveCommenter is gone")
	}
	if !strings.Contains(body, "a.resolveConsoleUser(r)") {
		t.Skip("resolveCommenter no longer accepts a console session; this dependency is moot")
	}
	if !strings.Contains(body, "Paid: true") {
		t.Fatal("the operator branch no longer grants paid access — if that is deliberate this " +
			"test should go, but it must not change silently")
	}
	// The cookie's host-only property is what bounds this. Assert the writer
	// still sets no Domain, from the caller's side, so the two files cannot
	// drift apart without one of them failing.
	if strings.Contains(readSourceFile(t, "../../internal/auth/session.go"), "Domain:") {
		t.Error("the session cookie writer now sets a Domain attribute. resolveCommenter treats " +
			"any signed-in operator as a paid member on whatever host the request arrived at, " +
			"and it is the cookie's host-only scope that keeps that to one hostname")
	}
}
