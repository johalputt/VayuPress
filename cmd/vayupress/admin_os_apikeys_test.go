// SPDX-License-Identifier: Apache-2.0

package main

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/johalputt/vayupress/internal/apikeys"
	"github.com/johalputt/vayupress/internal/secrets"
	"github.com/johalputt/vayupress/internal/ui"
)

// TestAPIKeysOwnSectionCSPSafe renders the issued-key list and the scoped-key
// create card across the full lifecycle (active / inactive / expired / revoked /
// internal) and asserts the fragment carries no inline style, no unsafe-eval,
// and no external asset host — the VayuOS CSP contract. It also proves the
// permission grid is fully wired: every section × action pair emits a checkbox.
func TestAPIKeysOwnSectionCSPSafe(t *testing.T) {
	past := time.Now().Add(-2 * time.Hour)
	future := time.Now().Add(48 * time.Hour)
	used := time.Now().Add(-30 * time.Minute)

	scoped := apikeys.NewPermissions()
	scoped.Grant(apikeys.SectionPosts, apikeys.ActionRead)
	scoped.Grant(apikeys.SectionPosts, apikeys.ActionWrite)
	scoped.Grant(apikeys.SectionMedia, apikeys.ActionRead)

	keys := []apikeys.Key{
		{ID: "sys", Label: "System (internal)", Prefix: "vp_sys000", Scope: apikeys.ScopeInternal, Active: true, Permissions: apikeys.Superuser()},
		{ID: "k-active", Label: "CI deploy", Prefix: "vp_active0", Scope: apikeys.ScopeExternal, Active: true, Permissions: scoped, LastUsedAt: &used, ExpiresAt: &future},
		{ID: "k-inactive", Label: "Paused bot", Prefix: "vp_inact00", Scope: apikeys.ScopeExternal, Active: false, Permissions: scoped},
		{ID: "k-expired", Label: "Old token", Prefix: "vp_exp0000", Scope: apikeys.ScopeExternal, Active: true, Permissions: scoped, ExpiresAt: &past},
		{ID: "k-revoked", Label: "Leaked key", Prefix: "vp_rev0000", Scope: apikeys.ScopeExternal, Active: false, Revoked: true, Permissions: scoped},
		{ID: "k-super", Label: "Automation root", Prefix: "vp_super00", Scope: apikeys.ScopeExternal, Active: true, Permissions: apikeys.Superuser()},
	}

	out := osAPIKeysOwnSection(keys)
	assertCSPSafe(t, "osAPIKeysOwnSection", out)

	// Each lifecycle state appears as a dot and a word, in its own tone.
	for _, want := range []ui.HTML{
		ui.State("ok", "Active"), ui.State("neutral", "Inactive"), ui.State("warn", "Expired"),
		ui.State("neutral", "Revoked"), ui.State("neutral", "System, auto-managed"),
	} {
		if !strings.Contains(out, string(want)) {
			t.Errorf("own section missing the state %s", want)
		}
	}
	// The create form is in a sheet, and the row that opens it is on the page.
	if !strings.Contains(out, `data-sheet="ak-create"`) || !strings.Contains(out, `<dialog class="sa-sheet" id="ak-create"`) {
		t.Error("the create sheet is not both opened and present")
	}
	// Lifecycle actions: activate is offered only for the inactive key; a live key
	// offers deactivate; expired/revoked offer delete.
	for _, want := range []string{`data-action="ak-activate"`, `data-action="ak-deactivate"`, `data-action="ak-rotate"`, `data-action="ak-revoke"`, `data-action="ak-delete"`} {
		if !strings.Contains(out, want) {
			t.Errorf("own section missing action %q", want)
		}
	}

	// The 12×6 permission grid must expose every section × action checkbox plus a
	// per-row "all" toggle and the grand superuser toggle.
	for _, sec := range apikeys.AllSections {
		if !strings.Contains(out, `data-section="`+string(sec)+`"`) {
			t.Errorf("permission grid missing section %q", sec)
		}
		for _, act := range apikeys.AllActions {
			cell := `data-section="` + string(sec) + `" data-action="` + string(act) + `"`
			if !strings.Contains(out, cell) {
				t.Errorf("permission grid missing checkbox %s:%s", sec, act)
			}
		}
	}
	if !strings.Contains(out, `id="ak-perm-super"`) {
		t.Error("permission grid missing the full-access (superuser) toggle")
	}
	if !strings.Contains(out, `class="ak-perm-all"`) {
		t.Error("permission grid missing per-row select-all toggles")
	}

	// A scoped key surfaces capability chips; a superuser/internal key collapses
	// to a single "Full access" badge (never a chip explosion).
	if !strings.Contains(out, "posts:read") || !strings.Contains(out, `class="ak-cap"`) {
		t.Error("scoped key did not render capability chips")
	}
	table := out[:strings.Index(out, "</table>")]
	if n := strings.Count(table, string(ui.State("accent", "Full access"))); n != 2 {
		t.Errorf("the superuser and internal keys must each read Full access in the list, got %d", n)
	}
	// Every key but the system key is managed from a sheet of its own, which
	// the row opens; the system key has nothing to manage.
	for _, k := range keys {
		opens := strings.Contains(table, `data-sheet="ak-key-`+k.ID+`"`)
		exists := strings.Contains(out, `<dialog class="sa-sheet" id="ak-key-`+k.ID+`"`)
		if want := k.Scope != apikeys.ScopeInternal; opens != want || exists != want {
			t.Errorf("key %s: sheet opened %v, present %v, want %v", k.ID, opens, exists, want)
		}
	}
	if strings.Contains(table, `data-action="ak-`) {
		t.Error("a lifecycle control is back in the table, which then scrolls sideways on a laptop")
	}
}

// TestAPIKeysVCBCSPSafe verifies the VCB gateway is CSP-safe and links to the
// compatibility docs and the live contract endpoints.
func TestAPIKeysVCBCSPSafe(t *testing.T) {
	out := string(osAPIKeysVCB())
	assertCSPSafe(t, "osAPIKeysVCB", out)
	for _, want := range []string{
		`href="/docs/compatibility/vcb"`,
		`href="/docs/compatibility/vayuapi"`,
		"/api/v1/vcb/contract",
		"plugins:read",
		"vayu-compat",
		`data-sheet="ak-vcb"`,
		`<dialog class="sa-sheet" id="ak-vcb"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("VCB section missing %q", want)
		}
	}
}

// TestAPIKeysServicesSectionCSPSafe renders the third-party credential cards
// (known providers + a stored custom credential) and asserts CSP-safety and
// that a stored secret is shown only as its masked hint, never in clear text.
func TestAPIKeysServicesSectionCSPSafe(t *testing.T) {
	creds := []secrets.Credential{
		{ID: "c1", Provider: secrets.ProviderOpenRouter, Label: "OpenRouter", Endpoint: "https://openrouter.ai/api/v1", HasSecret: true, Hint: "sk-…9f2", Enabled: true},
		{ID: "c2", Provider: secrets.ProviderCustom, Label: "Pushover", HasSecret: true, Hint: "az…7k", Enabled: false},
	}
	out := osAPIKeysServicesSection(creds)
	assertCSPSafe(t, "osAPIKeysServicesSection", out)

	if !strings.Contains(out, "Pushover") {
		t.Error("custom credential row not rendered")
	}
	if !strings.Contains(out, "IndexNow") || !strings.Contains(out, "OpenRouter") {
		t.Error("known-provider rows not rendered")
	}
	if strings.Contains(out, `type="password" data-cred-secret placeholder="sk-`) && strings.Contains(out, "sk-live") {
		t.Error("services section must never emit a plaintext secret value")
	}
	// Each state says which of three things is true: never set up, set up and
	// switched off, set up and on.
	for _, want := range []string{
		string(ui.State("ok", "On")),
		string(ui.State("neutral", "Off")), string(ui.State("neutral", "Not set up")),
	} {
		if !strings.Contains(out, want) {
			t.Errorf("services section missing %s", want)
		}
	}
	// Every sheet a row opens exists: one per provider, one per custom
	// credential, and the one that adds a credential.
	ids := []string{"ak-cc-c2", "ak-cc-new"}
	for _, p := range knownProviders {
		ids = append(ids, "ak-cred-"+p.Provider)
	}
	for _, id := range ids {
		if !strings.Contains(out, `data-sheet="`+id+`"`) || !strings.Contains(out, `<dialog class="sa-sheet" id="`+id+`"`) {
			t.Errorf("sheet %q is not both opened and present", id)
		}
	}
}

// TestParseAPIKeyExpiry proves the create handler accepts both the browser
// datetime-local shape and full RFC3339, normalises to UTC, and rejects junk.
func TestParseAPIKeyExpiry(t *testing.T) {
	rfc, err := parseAPIKeyExpiry("2030-01-02T15:04:05Z")
	if err != nil {
		t.Fatalf("RFC3339 must parse: %v", err)
	}
	if rfc.Location() != time.UTC {
		t.Errorf("expiry must be normalised to UTC, got %v", rfc.Location())
	}
	if _, err := parseAPIKeyExpiry("2030-01-02T15:04"); err != nil {
		t.Errorf("datetime-local (no seconds) must parse: %v", err)
	}
	if _, err := parseAPIKeyExpiry("2030-01-02T15:04:05"); err != nil {
		t.Errorf("datetime-local (with seconds) must parse: %v", err)
	}
	if _, err := parseAPIKeyExpiry("not-a-date"); err == nil {
		t.Error("garbage expiry must be rejected, not silently accepted")
	}
	if _, err := parseAPIKeyExpiry(""); err == nil {
		t.Error("empty string is not a valid expiry at the parse layer")
	}
}

// TestInternalKeyOffersNoImpossibleActions is the regression test for a control
// that could only ever fail.
//
// The store refuses every lifecycle operation on the internal/system key by
// design: rotating it would hand the caller a fresh unconditional superuser
// token (audit C2). The row offered a Rotate button anyway, so pressing it
// always produced an error. A control that cannot succeed is worse than no
// control — it reads as a capability, and its refusal reads as a bug rather than
// as the protection it actually is.
func TestInternalKeyOffersNoImpossibleActions(t *testing.T) {
	internal := apikeys.Key{
		ID: apikeys.InternalKeyID, Label: "System (internal)", Prefix: "vp_sys000",
		Scope: apikeys.ScopeInternal, Permissions: apikeys.Superuser(), Active: true,
		CreatedAt: time.Now().Add(-30 * 24 * time.Hour),
	}
	out := osAPIKeysOwnSection([]apikeys.Key{internal})

	for _, forbidden := range []string{
		`data-action="ak-rotate" data-id="` + apikeys.InternalKeyID + `"`,
		`data-action="ak-revoke" data-id="` + apikeys.InternalKeyID + `"`,
		`data-action="ak-delete" data-id="` + apikeys.InternalKeyID + `"`,
		`data-action="ak-deactivate" data-id="` + apikeys.InternalKeyID + `"`,
	} {
		if strings.Contains(out, forbidden) {
			t.Errorf("the system key offers %s, which the store always refuses", forbidden)
		}
	}
	if !strings.Contains(out, "Protected") {
		t.Error("the system key should say why it has no controls, not just show a blank cell")
	}
}

// TestAPIKeyStateCountsOnlyUsableGrants — a revoked or expired key is not
// exposure. Inflating the full-access figure would make the one number on this
// page that should provoke a reaction easy to ignore.
func TestAPIKeyStateCountsOnlyUsableGrants(t *testing.T) {
	past := time.Now().UTC().Add(-time.Hour)
	keys := []apikeys.Key{
		{ID: "sys", Scope: apikeys.ScopeInternal, Permissions: apikeys.Superuser(), Active: true},
		{ID: "live", Scope: apikeys.ScopeExternal, Permissions: apikeys.Superuser(), Active: true},
		{ID: "revoked", Scope: apikeys.ScopeExternal, Permissions: apikeys.Superuser(), Active: true, Revoked: true},
		{ID: "expired", Scope: apikeys.ScopeExternal, Permissions: apikeys.Superuser(), Active: true, ExpiresAt: &past},
		{ID: "off", Scope: apikeys.ScopeExternal, Permissions: apikeys.Superuser(), Active: false},
	}
	out := string(osAPIKeysState(keys))
	// Exactly one usable external key, and exactly one usable full-access grant;
	// the other three are accounted for, not silently dropped.
	for _, want := range []ui.HTML{
		ui.State("ok", "1 key active"),
		ui.State("neutral", "3 inactive"),
		ui.State("warn", "1 with full access"),
	} {
		if !strings.Contains(out, string(want)) {
			t.Errorf("state missing %s, got:\n%s", want, out)
		}
	}
	// The auto-managed system key is not an operator-issued grant.
	if clean := string(osAPIKeysState([]apikeys.Key{keys[0]})); clean != string(ui.State("neutral", "No keys issued")) {
		t.Errorf("the auto-managed system key is being counted as an operator's key: %s", clean)
	}
}

// TestModalPanelCanScroll is the regression test for a dialog whose actions
// could become unreachable.
//
// The backdrop is position:fixed and centres its child, so a panel taller than
// the viewport overflows off BOTH edges — and because the backdrop does not
// scroll, there is no way to reach the footer. The tier editor has a dozen
// fields and hit exactly that: the Save button existed and could not be clicked.
//
// The fix is structural, not cosmetic: cap the panel, let the BODY scroll inside
// it, and keep the header and actions pinned. The intermediate <form> has to
// carry the flex column too, and min-height:0 is what actually permits a flex
// child to shrink below its content — without it the body never scrolls.
func TestModalPanelCanScroll(t *testing.T) {
	css, err := os.ReadFile("../../static/css/vayuos.css")
	if err != nil {
		t.Skipf("stylesheet not readable: %v", err)
	}
	s := string(css)

	block := func(sel string) string {
		i := strings.Index(s, sel)
		if i < 0 {
			t.Fatalf("%s has disappeared from the stylesheet", sel)
		}
		return s[i : i+strings.Index(s[i:], "}")]
	}

	panel := block(".vp-os .modal-panel {")
	if !strings.Contains(panel, "max-height") {
		t.Error("the modal panel has no max-height; a tall dialog overflows the viewport with no way to scroll")
	}
	if !strings.Contains(panel, "flex-direction: column") {
		t.Error("the modal panel is not a flex column, so its body cannot be given a bounded scroll area")
	}

	body := block(".vp-os .modal-body {")
	if !strings.Contains(body, "overflow-y: auto") {
		t.Error("the modal body does not scroll; the footer becomes unreachable on a tall dialog")
	}
	if !strings.Contains(body, "min-height: 0") {
		t.Error("the modal body lacks min-height:0, so as a flex child it will not shrink and will not scroll")
	}

	// The tier editor wraps body+footer in a <form>; the column must pass through
	// it or the body has no bounded height to scroll within.
	form := block(".vp-os .modal-panel > form {")
	if !strings.Contains(form, "min-height: 0") {
		t.Error("the intermediate <form> does not pass the flex column through; the body will not scroll")
	}
}
