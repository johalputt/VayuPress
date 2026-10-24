// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/apikeys"
	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/settings"
)

// callsAdmitted sends n calls as ki and returns how many were served.
func callsAdmitted(a *App, ki apikeys.KeyInfo, n int) int {
	ok := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	served := 0
	for i := 0; i < n; i++ {
		rec := httptest.NewRecorder()
		a.serveWithKeyUsage(rec, httptest.NewRequest("GET", "/api/v1/queue", nil), ki, ok)
		if rec.Code == http.StatusOK {
			served++
		}
	}
	return served
}

// A key the operator made unlimited is counted against nothing: more calls
// than the built-in budget in one minute are all served.
func TestAnUnlimitedKeyIsNeverCounted(t *testing.T) {
	t.Setenv("VAYU_API_RATE_PER_MIN", "")
	ki := apikeys.KeyInfo{ID: "rate-unlimited", Scope: apikeys.ScopeExternal, RatePerMin: apikeys.Unlimited}
	if got := callsAdmitted(&App{}, ki, 700); got != 700 {
		t.Errorf("an unlimited key was served %d of 700 calls in a minute", got)
	}
}

// The API keys page's default is every key's that sets none, as a number or
// unlimited, and a key's own budget outranks it.
func TestTheKeysDefaultIsTheOperators(t *testing.T) {
	openMigratedDB(t)
	a := &App{siteSettings: settings.New(dbpkg.DB)}
	set := func(v string) {
		t.Helper()
		if err := a.siteSettings.SetMany(context.Background(), settings.ForPrimary(), map[string]string{settings.KeyAPIRatePerMin: v}); err != nil {
			t.Fatal(err)
		}
	}
	set("2")
	if got := callsAdmitted(a, apikeys.KeyInfo{ID: "rate-default-2", Scope: apikeys.ScopeExternal}, 5); got != 2 {
		t.Errorf("with a default of 2 a minute, a key with none of its own was served %d of 5", got)
	}
	if got := callsAdmitted(a, apikeys.KeyInfo{ID: "rate-own-4", Scope: apikeys.ScopeExternal, RatePerMin: 4}, 6); got != 4 {
		t.Errorf("a key with its own 4 a minute was served %d of 6 under a default of 2", got)
	}
	set("unlimited")
	if got := callsAdmitted(a, apikeys.KeyInfo{ID: "rate-default-unl", Scope: apikeys.ScopeExternal}, 700); got != 700 {
		t.Errorf("with the default unlimited, a key with none of its own was served %d of 700", got)
	}
	if got := a.otherLimitsNote(context.Background()); !strings.Contains(got, "API and MCP keys: unlimited each") {
		t.Errorf("the Shield page's note does not say the keys' default: %s", got)
	}
}

// A key's budget is the default, unlimited or 1 to 100000, and the system
// key has none to set.
func TestAKeysBudgetIsOneTheStoreCanHold(t *testing.T) {
	openMigratedDB(t)
	s := apikeys.New(dbpkg.DB)
	k, _, err := s.CreateWithPermissions(context.Background(), "", "agent", apikeys.Superuser(), nil, apikeys.Unlimited)
	if err != nil || k.RatePerMin != apikeys.Unlimited {
		t.Fatalf("an unlimited key could not be made: %v (%d)", err, k.RatePerMin)
	}
	for _, bad := range []int{-2, apikeys.MaxRatePerMin + 1} {
		if err := s.SetRatePerMin(context.Background(), k.ID, bad); !errors.Is(err, apikeys.ErrBadRate) {
			t.Errorf("a budget of %d was stored (err %v)", bad, err)
		}
	}
	if err := s.SetRatePerMin(context.Background(), apikeys.InternalKeyID, 5); !errors.Is(err, apikeys.ErrInternalProtected) {
		t.Errorf("the system key's budget was set (err %v)", err)
	}
}
