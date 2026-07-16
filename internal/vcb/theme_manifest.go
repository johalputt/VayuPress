package vcb

// theme_manifest.go — the on-disk theme manifest ("theme.json"). A VayuPress
// theme is a theme.Tokens value (design tokens + options + optional CustomCSS)
// plus catalogue metadata. Until VCB there was no declared artifact a
// third-party theme author could ship and validate before import: tokens were
// stored as an opaque JSON blob and every mistake surfaced late (a bad colour
// aborts compile at apply time; a misspelled option key is a silent no-op).
// ThemeManifest wraps the REAL structs — theme.Tokens and theme.ThemeMeta —
// unchanged, so a manifest that validates is exactly what the compiler and
// catalogue consume.

import (
	"fmt"

	"github.com/johalputt/vayupress/internal/theme"
)

// ThemeManifestFilename is the canonical file name a theme package carries at
// its root.
const ThemeManifestFilename = "theme.json"

// ThemeManifest is the versioned, on-disk declaration of a theme.
type ThemeManifest struct {
	// VCB is the manifest schema version (must equal ManifestVersion).
	VCB int `json:"vcb"`

	// Tokens is the theme itself — the exact struct CompileCSS consumes.
	// Field names are the wire format (Name, BgDark, …, custom_css, options).
	Tokens theme.Tokens `json:"tokens"`

	// Meta is the catalogue card (tagline, description, tags, category). Its
	// Name must match Tokens.Name; Category must be one of the fixed
	// catalogue categories.
	Meta theme.ThemeMeta `json:"meta,omitempty"`

	// MinHost / MaxHost bound the VayuPress versions this theme supports
	// (empty = open). Themes rarely need this; declare it when the theme
	// depends on options introduced in a specific release.
	MinHost string `json:"min_host,omitempty"`
	MaxHost string `json:"max_host,omitempty"`

	// APIPermissions lists "section:action" capabilities in case the theme
	// ships companion automation that calls the VayuPress API (e.g.
	// "themes:apply", "design:apply"). Most themes declare none.
	APIPermissions []string `json:"api_permissions,omitempty"`
}

// LoadThemeManifest reads and parses a theme.json with unknown-field
// rejection and a size bound. Deep validation lives in internal/vcb/validate.
func LoadThemeManifest(path string) (*ThemeManifest, error) {
	raw, err := readBounded(path)
	if err != nil {
		return nil, err
	}
	return ParseThemeManifest(raw)
}

// ParseThemeManifest parses manifest bytes with unknown-field rejection at the
// manifest level. (theme.Tokens itself keeps standard decoding: its wire shape
// is owned by internal/theme, and its Options map intentionally carries only
// known keys — the validator flags unknown ones explicitly.)
func ParseThemeManifest(raw []byte) (*ThemeManifest, error) {
	var m ThemeManifest
	if err := strictUnmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("vcb: parse theme manifest: %w", err)
	}
	return &m, nil
}

// ThemeOptionSchema returns the machine-readable customization contract for a
// named theme: the shared options plus any per-theme extras, each with its
// allowed choice values. This is the same catalogue the admin design studio
// renders — re-exported so theme authors and the validator consume one source
// of truth.
func ThemeOptionSchema(name string) []theme.Option {
	return theme.OptionsFor(name)
}

// ThemeCategories returns the fixed catalogue category vocabulary a
// manifest's Meta.Category must belong to.
func ThemeCategories() []string {
	return theme.AllCategories()
}
