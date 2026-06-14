package main

import (
	_ "embed"
	"net/http"
)

// openapiSpec is the embedded OpenAPI 3.0 description of the HTTP API. It is
// served verbatim so the published contract is always shipped with the binary
// and can never drift away from the routes it documents.
//
//go:embed openapi.json
var openapiSpec []byte

// handleOpenAPISpec serves the embedded OpenAPI document. It is intentionally a
// public, read-only endpoint: the spec describes the surface, it does not expose
// any data. No third-party UI is bundled, keeping the strict CSP intact.
func (a *App) handleOpenAPISpec(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	_, _ = w.Write(openapiSpec)
}
