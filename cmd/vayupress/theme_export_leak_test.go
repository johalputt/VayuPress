// SPDX-License-Identifier: Apache-2.0

package main

// theme_export_leak_test.go — what an exported "theme" is allowed to contain.
//
// ATTACK: ask an operator for their theme.
//
// The panel offers "Download the full theme as JSON … or import one to apply it
// everywhere", and the handler's own comment promised the bundle carried "no
// secrets" and was "safe to share". It carried `tor.space_api_key` — a live API
// key — because the exporter walked settings.AllKeys, which is the set of things
// that may be WRITTEN, not the set of things that are a look.
//
// Also in the bundle: the shield's allow and deny CIDR lists, the cluster peer
// list, the third-party intelligence feeds an operator had subscribed to,
// payment configuration, the BTCPay store id, contact and feedback addresses,
// and — after ADR-0155 P2 added it to AllKeys — the VayuKeep backup destination.
// Sharing a colour scheme handed over all of it.
//
// The root cause is worth more than the fix: the list of "keys that are not part
// of a theme" existed only in a TEST. The conformance test kept it, the exporter
// did not consult it, and nothing connected them. It is production state now
// (settings.NotPortable) and the test reads it instead of keeping a copy.

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	_ "github.com/mattn/go-sqlite3"

	"github.com/johalputt/vayupress/internal/settings"
)

// exportedBundle runs the REAL handler and returns the settings it emitted.
//
// The first version of this helper re-derived the filter — "the same way the
// handler derives it" — and a mutation proved that worthless: putting the
// original `for key := range settings.AllKeys` back into handleThemeExport, the
// exact code that was leaking the API key, left every test in this file green.
// A test that reimplements the thing it is testing agrees with itself no matter
// what production does.
//
// So this exercises the handler over HTTP and reads the bytes it produced. It is
// slower and it is the only version that can fail.
func exportedBundle(t *testing.T) map[string]string {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(`CREATE TABLE site_settings (scope TEXT NOT NULL DEFAULT '', ` +
		`key TEXT NOT NULL, value TEXT NOT NULL DEFAULT '', ` +
		`updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP, PRIMARY KEY(scope,key))`); err != nil {
		t.Fatalf("settings schema: %v", err)
	}
	store := settings.New(db)

	// Every writable key carries a DISTINCT canary value, so a leak is visible in
	// the bytes even when the key name looks innocent.
	seed := map[string]string{}
	for k := range settings.AllKeys {
		seed[k] = "canary-" + k
	}
	if err := store.SetMany(context.Background(), settings.ForPrimary(), seed); err != nil {
		t.Fatalf("seed settings: %v", err)
	}

	a := &App{siteSettings: store}
	rr := httptest.NewRecorder()
	a.handleThemeExport(rr, httptest.NewRequest(http.MethodGet, "/os/api/theme/export", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("export returned %d: %s", rr.Code, rr.Body.String())
	}
	var env struct {
		Settings map[string]string `json:"settings"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode bundle: %v (body %s)", err, rr.Body.String())
	}
	return env.Settings
}

// exportedKeys is the key set the real handler emitted.
func exportedKeys(t *testing.T) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	for k := range exportedBundle(t) {
		out[k] = true
	}
	if len(out) == 0 {
		t.Fatal("the handler emitted no settings at all; this guard cannot see a leak")
	}
	return out
}

// THE test. A credential must never be in a file the product tells you to share.
func TestTheThemeBundleCarriesNoCredential(t *testing.T) {
	exported := exportedKeys(t)

	// Named individually rather than pattern-matched, because a pattern is a
	// guess about what future secrets will be called and this list is the set
	// that was actually leaking.
	for _, secret := range []string{
		settings.KeyTorSpaceAPIKey,
		settings.KeyBTCPayStoreID,
		settings.KeyBTCPayURL,
	} {
		if exported[secret] {
			t.Errorf("the theme bundle carries %q — the panel invites an operator to share this "+
				"file and apply it everywhere", secret)
		}
	}

	// And a belt-and-braces sweep, because the next credential will not be on
	// the list above. Any key whose NAME says secret has no business in a look.
	for key := range exported {
		lower := strings.ToLower(key)
		for _, marker := range []string{"api_key", "apikey", "secret", "token", "password", "passphrase"} {
			if strings.Contains(lower, marker) {
				t.Errorf("exported key %q looks like a credential (%q) and is in a shareable bundle",
					key, marker)
			}
		}
	}
}

// Network policy is not a look. Importing somebody's theme must not be able to
// tell you who they block, and exporting yours must not tell them.
func TestTheThemeBundleCarriesNoNetworkPolicy(t *testing.T) {
	exported := exportedKeys(t)
	for _, k := range []string{
		settings.KeyShieldAllowCIDRs,
		settings.KeyShieldDenyCIDRs,
		settings.KeyShieldAllowCountries,
		settings.KeyShieldDenyCountries,
		settings.KeyShieldClusterPeers,
		settings.KeyShieldIntelFeeds,
	} {
		if exported[k] {
			t.Errorf("the theme bundle carries %q — access control and cluster topology are not "+
				"a colour scheme", k)
		}
	}
}

// Infrastructure an operator did not agree to publish. The backup target is the
// one ADR-0155 added, and it is the sharpest: it names where this install's
// encrypted backups go.
func TestTheThemeBundleCarriesNoInfrastructureDetail(t *testing.T) {
	exported := exportedKeys(t)
	for _, k := range []string{
		settings.KeyVayuKeepTarget,
		settings.KeyVayuKeepEnabled,
		settings.KeyTalkHost,
		settings.KeyStartupMillis,
		settings.KeyContactEmail,
		settings.KeyFeedbackEmail,
	} {
		if exported[k] {
			t.Errorf("the theme bundle carries %q, which is a property of this machine or its "+
				"operator rather than of its appearance", k)
		}
	}
}

// The bundle must still contain a theme. A guard that empties the export has
// broken the feature rather than secured it, and "nothing leaks" is trivially
// satisfied by shipping nothing.
func TestTheThemeBundleStillCarriesATheme(t *testing.T) {
	exported := exportedKeys(t)
	if len(exported) == 0 {
		t.Fatal("the export is empty; the leak was closed by removing the feature")
	}
	for _, k := range []string{
		settings.KeySiteName,
		settings.KeySiteTagline,
		settings.KeyThemeCustomCSS,
	} {
		if !exported[k] {
			t.Errorf("%q is no longer exported, so a shared theme has lost part of the look", k)
		}
	}
}

// The exporter and the conformance test must read the SAME set. Two copies is
// how a credential ended up in a shareable file for as long as it did.
func TestTheExporterAndTheEditorAgreeOnWhatAThemeIs(t *testing.T) {
	if len(settings.NotPortable) == 0 {
		t.Fatal("NotPortable is empty, so the exporter has no line to honour")
	}
	// Every non-portable key must still be writable — this set restricts what
	// LEAVES the install, not what an operator may configure. Confusing the two
	// would silently disable the VayuKeep panel again.
	for k := range settings.NotPortable {
		if !settings.AllKeys[k] {
			t.Errorf("%q is marked not-portable but is also not writable; NotPortable governs "+
				"export, not configuration", k)
		}
	}
}

// A real bundle, encoded, must not contain a planted secret value anywhere in
// its bytes. Key-level checks miss a value copied into a portable key.
func TestAPlantedSecretDoesNotSurviveIntoTheEncodedBundle(t *testing.T) {
	bundle := exportedBundle(t)
	// Every writable key was seeded with "canary-<key>", so any non-portable key
	// that reached the bundle is visible by its own value regardless of the key
	// it arrived under.
	for _, secret := range []string{
		settings.KeyTorSpaceAPIKey, settings.KeyShieldDenyCIDRs, settings.KeyVayuKeepTarget,
	} {
		want := "canary-" + secret
		for k, v := range bundle {
			if v == want {
				t.Errorf("the value of %q was exported under key %q", secret, k)
			}
		}
	}
}
