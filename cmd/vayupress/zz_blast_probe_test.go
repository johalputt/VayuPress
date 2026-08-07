// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/apikeys"
	"github.com/johalputt/vayupress/internal/auth"
	"github.com/johalputt/vayupress/internal/users"
)

func blastKeyReq(t *testing.T, vals string) *http.Request {
	t.Helper()
	perms := apikeys.NewPermissions()
	perms.Grant(apikeys.SectionMail, apikeys.ActionWrite)
	auth.SetExtraAPIKeyResolver(func(k string) (apikeys.KeyInfo, bool) {
		if k == "probe-key" {
			return apikeys.KeyInfo{ID: "k1", Scope: apikeys.ScopeExternal, Perms: perms}, true
		}
		return apikeys.KeyInfo{}, false
	})
	t.Cleanup(func() { auth.SetExtraAPIKeyResolver(nil) })
	req := httptest.NewRequest(http.MethodPost, "/os/vayumail/accounts/action", strings.NewReader(vals))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-API-Key", "probe-key")
	return req
}

func TestBlastMatrix(t *testing.T) {
	ctx := context.Background()
	type row struct{ name, seedRole, form string }
	rows := []row{
		{"key: promote plain mailbox -> administrator", "mailbox", "op=role&email=dana@example.com&role=administrator"},
		{"key: promote plain mailbox -> author", "mailbox", "op=role&email=dana@example.com&role=author"},
		{"key: set plain mailbox -> reviewer", "mailbox", "op=role&email=dana@example.com&role=reviewer"},
		{"key: set plain mailbox -> mailbox", "mailbox", "op=role&email=dana@example.com&role=mailbox"},
		{"key: demote administrator -> mailbox", "administrator", "op=role&email=dana@example.com&role=mailbox"},
		{"key: toggle plain mailbox off", "mailbox", "op=toggle&email=dana@example.com&active=false"},
		{"key: toggle administrator off", "administrator", "op=toggle&email=dana@example.com&active=false"},
		{"key: delete plain mailbox", "mailbox", "op=delete&email=dana@example.com"},
		{"key: delete administrator", "administrator", "op=delete&email=dana@example.com"},
		{"key: quota on administrator", "administrator", "op=quota&email=dana@example.com&quota_mb=50"},
		{"key: retention on administrator", "administrator", "op=retention&email=dana@example.com&retention_days=30"},
	}
	for _, rw := range rows {
		a := appWithMailAccounts(t)
		if err := a.vayuMail.Accounts().SetRole(ctx, "dana@example.com", rw.seedRole); err != nil {
			t.Fatal(err)
		}
		req := blastKeyReq(t, rw.form)
		rec := httptest.NewRecorder()
		a.handleVayuOSAccountsAction(rec, req)
		after := a.vayuMail.Accounts().RoleFor(ctx, "dana@example.com")
		t.Logf("%-46s status=%d roleAfter=%q", rw.name, rec.Code, after)
	}

	// Session paths: CMS admin and a vmail administrator session.
	a := appWithMailAccounts(t)
	admin := &users.User{ID: "admin1", Email: "boss@example.com", Role: users.RoleAdmin}
	rec := postAcctAction(a, "op=role&email=dana@example.com&role=administrator", admin)
	t.Logf("%-46s status=%d roleAfter=%q", "cms admin session: promote to administrator",
		rec.Code, a.vayuMail.Accounts().RoleFor(ctx, "dana@example.com"))

	u, mailOnly, ok := a.resolveMailSessionUser(ctx, "dana@example.com")
	t.Logf("vmail administrator session resolves: ok=%v cmsRole=%q mailOnly=%v", ok, u.Role, mailOnly)
	rec2 := postAcctAction(a, "op=role&email=dana@example.com&role=editor", u)
	t.Logf("%-46s status=%d roleAfter=%q", "vmail administrator session: set editor",
		rec2.Code, a.vayuMail.Accounts().RoleFor(ctx, "dana@example.com"))
}
