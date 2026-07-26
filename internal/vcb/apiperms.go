// SPDX-License-Identifier: Apache-2.0

// Package vcb defines the Vayu Compatibility Bible (VCB, ADR-0135): the
// versioned, machine-checkable contracts a third-party extension — plugin,
// theme, or tool — must satisfy to be compatible with VayuPress. The package
// deliberately contains no runtime behaviour of its own: every vocabulary it
// exposes is re-exported from (or shared with) the subsystem that actually
// enforces it, so the published contract and the running code cannot drift.
//
//   - apiperms.go       — the section:action capability vocabulary (from internal/apikeys)
//   - hooks.go          — the enumerated hook/event-name catalogue
//   - manifest.go       — the on-disk plugin manifest (plugin.json)
//   - theme_manifest.go — the on-disk theme manifest (theme.json)
//
// Static validation lives in internal/vcb/validate; the standalone CLI for
// extension authors and CI lives in tools/vayu-compat.
package vcb

import "github.com/johalputt/vayupress/internal/apikeys"

// Section and Action alias the canonical VayuAPI capability enum. A VCB
// manifest declares the API permissions its extension needs as
// "section:action" tokens; the admin key-manager UI pre-checks exactly those
// boxes when the operator mints the extension's key.
type (
	Section = apikeys.Section
	Action  = apikeys.Action
)

// AllSections / AllActions are the ordered canonical vocabularies — the same
// slices that drive the admin permission grid and API enforcement.
var (
	AllSections = apikeys.AllSections
	AllActions  = apikeys.AllActions
)

// ParseCapability splits a "section:action" token, reporting whether both
// parts are known (wildcards "*" included).
func ParseCapability(cap string) (Section, Action, bool) {
	return apikeys.ParseCapability(cap)
}

// ValidCapability reports whether cap is a well-formed, known
// "section:action" token. Extensions must not declare the superuser
// wildcard: a manifest asking for "*:*" (or "section:*") is legal syntax but
// the validator flags it — least privilege is the contract.
func ValidCapability(cap string) bool {
	_, _, ok := apikeys.ParseCapability(cap)
	return ok
}

// WildcardCapability reports whether cap uses a wildcard section or action.
// The validator rejects wildcard declarations in third-party manifests.
func WildcardCapability(cap string) bool {
	s, a, ok := apikeys.ParseCapability(cap)
	if !ok {
		return false
	}
	return s == apikeys.SectionAll || a == apikeys.ActionAll
}
