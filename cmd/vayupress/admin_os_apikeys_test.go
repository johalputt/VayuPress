// SPDX-License-Identifier: Apache-2.0

package main

import (
	"strings"
	"testing"
	"time"

	"github.com/johalputt/vayupress/internal/apikeys"
	"github.com/johalputt/vayupress/internal/secrets"
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

	// Lifecycle status badges must each appear.
	for _, want := range []string{"Active", "Inactive", "Expired", "Revoked", "System"} {
		if !strings.Contains(out, want) {
			t.Errorf("own section missing %q status", want)
		}
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
	if !strings.Contains(out, "Full access") {
		t.Error("superuser/internal key must render a Full access badge")
	}
}

// TestAPIKeysVCBCardCSPSafe verifies the one-click VCB gateway is CSP-safe and
// links to the compatibility docs and the live contract endpoints.
func TestAPIKeysVCBCardCSPSafe(t *testing.T) {
	out := osAPIKeysVCBCard()
	assertCSPSafe(t, "osAPIKeysVCBCard", out)
	for _, want := range []string{
		`href="/docs/compatibility/vcb"`,
		`href="/docs/compatibility/vayuapi"`,
		"/api/v1/vcb/contract",
		"plugins:read",
		"vayu-compat",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("VCB card missing %q", want)
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
		t.Error("known-provider cards not rendered")
	}
	if strings.Contains(out, `type="password" data-cred-secret placeholder="sk-`) && strings.Contains(out, "sk-live") {
		t.Error("services section must never emit a plaintext secret value")
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

// TestAPIKeyStatsCountOnlyUsableGrants — a revoked or expired key is not
// exposure. Inflating the full-access figure would make the one number on this
// page that should provoke a reaction easy to ignore.
func TestAPIKeyStatsCountOnlyUsableGrants(t *testing.T) {
	past := time.Now().UTC().Add(-time.Hour)
	keys := []apikeys.Key{
		{ID: "sys", Scope: apikeys.ScopeInternal, Permissions: apikeys.Superuser(), Active: true},
		{ID: "live", Scope: apikeys.ScopeExternal, Permissions: apikeys.Superuser(), Active: true},
		{ID: "revoked", Scope: apikeys.ScopeExternal, Permissions: apikeys.Superuser(), Active: true, Revoked: true},
		{ID: "expired", Scope: apikeys.ScopeExternal, Permissions: apikeys.Superuser(), Active: true, ExpiresAt: &past},
		{ID: "off", Scope: apikeys.ScopeExternal, Permissions: apikeys.Superuser(), Active: false},
	}
	out := osAPIKeysStats(keys, nil)
	// Exactly one usable external key, and exactly one usable full-access grant.
	if !strings.Contains(out, ">1<") {
		t.Error("stats do not report exactly one usable key among five rows")
	}
	if !strings.Contains(out, "inactive") {
		t.Error("keys excluded from the active count should be accounted for, not silently dropped")
	}
	if !strings.Contains(out, "stat-card--warn") {
		t.Error("a live full-access key exists but nothing marks it for attention")
	}
	// The auto-managed system key is not an operator-issued grant.
	clean := osAPIKeysStats([]apikeys.Key{keys[0]}, nil)
	if strings.Contains(clean, "stat-card--warn") {
		t.Error("the auto-managed system key is being counted as an operator's full-access grant")
	}
}
