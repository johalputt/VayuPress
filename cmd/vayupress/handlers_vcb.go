package main

// handlers_vcb.go — the VCB HTTP surface (ADR-0135). Two read-only endpoints
// close the loop between the Compatibility Bible and the running host:
//
//   GET  /api/v1/vcb/contract  — machine-readable contract discovery: this
//        host's version, manifest schema version, hook catalogue, capability
//        vocabulary, webhook events, theme categories and option keys. An AI
//        agent or build tool reads this once and knows exactly what it may
//        declare.
//   POST /api/v1/vcb/validate  — validate a plugin.json or theme.json against
//        THIS host (its real version, its real vocabularies) without
//        installing anything. Returns the same findings vayu-compat prints,
//        as JSON with stable machine codes.
//
// Both require plugins:read (see api_capabilities.go) — they expose contract
// metadata and a pure function of the request body; nothing is mutated.

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/johalputt/vayupress/internal/theme"
	"github.com/johalputt/vayupress/internal/vcb"
	"github.com/johalputt/vayupress/internal/vcb/validate"
)

// handleVCBContract returns this host's machine-readable extension contract.
func (a *App) handleVCBContract(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, r, http.StatusOK, map[string]interface{}{
		"host_version":     Version,
		"manifest_version": vcb.ManifestVersion,
		"hooks":            vcb.AllHooks,
		"webhook_events":   vcb.WebhookEvents,
		"sections":         vcb.AllSections,
		"actions":          vcb.AllActions,
		"theme_categories": vcb.ThemeCategories(),
		// The shared option schema with allowed choice values per key —
		// per-theme extras appear in validation, keyed by the theme's name.
		"theme_options":     vcb.ThemeOptionSchema(""),
		"theme_option_keys": theme.OptionKeys(),
		"docs":              "/docs/compatibility/vcb",
	})
}

// handleVCBValidate validates a posted manifest against this running host.
// The kind is sniffed from the shape (a theme manifest carries "tokens").
func (a *App) handleVCBValidate(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, vcb.MaxManifestBytes+1))
	if err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "bad-request", "could not read request body", "/docs/compatibility/vcb")
		return
	}
	if int64(len(body)) > vcb.MaxManifestBytes {
		writeAPIError(w, r, http.StatusRequestEntityTooLarge, "manifest-too-large",
			"manifest exceeds the size cap — a manifest is a small declaration, not a data file", "/docs/compatibility/vcb")
		return
	}

	var probe map[string]json.RawMessage
	if err := json.Unmarshal(body, &probe); err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "bad-json", "manifest is not valid JSON: "+err.Error(), "/docs/compatibility/vcb")
		return
	}

	opts := validate.Options{HostVersion: Version}
	var res *validate.Result
	if _, isTheme := probe["tokens"]; isTheme {
		m, err := vcb.ParseThemeManifest(body)
		if err != nil {
			writeAPIError(w, r, http.StatusBadRequest, "bad-manifest", err.Error(), "/docs/compatibility/vcb")
			return
		}
		res = validate.Theme(m, opts)
	} else {
		m, err := vcb.ParsePluginManifest(body)
		if err != nil {
			writeAPIError(w, r, http.StatusBadRequest, "bad-manifest", err.Error(), "/docs/compatibility/vcb")
			return
		}
		res = validate.Plugin(m, opts)
	}

	findings := res.Findings
	if findings == nil {
		findings = []validate.Finding{}
	}
	writeJSON(w, r, http.StatusOK, map[string]interface{}{
		"kind":         res.Kind,
		"name":         res.Name,
		"ok":           res.OK(),
		"findings":     findings,
		"host_version": Version,
		"docs":         "/docs/compatibility/vcb",
	})
}
