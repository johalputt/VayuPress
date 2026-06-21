package main

// handlers_diagram.go — server-side diagram live preview (ADR-0070, Phase 3).
//
// POST /api/v1/admin/diagram/preview
// Body:   {"source":"flowchart TD\n A-->B"}
// Returns:{"svg":"<svg…>"} | {"error":"…","fallback":true}
//
// The editor calls this (debounced) to show a live, themeable SVG without ever
// loading a client-side Mermaid library — the strict CSP stays untouched. Results
// are content-addressed in diagram_cache so repeated previews/saves are free.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/diagram"
	"github.com/johalputt/vayupress/internal/mode"
)

func (a *App) handleDiagramPreview(w http.ResponseWriter, r *http.Request) {
	writeJSONResp := func(code int, v interface{}) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		json.NewEncoder(w).Encode(v) //nolint:errcheck
	}

	cur := mode.Global.Current()
	if cur == mode.ModeReadOnly || cur == mode.ModeQuarantined {
		writeJSONResp(503, map[string]string{"error": "diagram preview unavailable in " + string(cur) + " mode"})
		return
	}

	var req struct {
		Source string `json:"source"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64*1024)).Decode(&req); err != nil {
		writeJSONResp(400, map[string]string{"error": "invalid JSON body"})
		return
	}
	src := strings.TrimSpace(req.Source)
	if src == "" {
		writeJSONResp(400, map[string]string{"error": "missing 'source'"})
		return
	}

	sum := sha256.Sum256([]byte(src))
	hash := hex.EncodeToString(sum[:])

	if svg := loadDiagramCache(hash); svg != "" {
		writeJSONResp(200, map[string]string{"svg": svg})
		return
	}

	svg, err := diagram.Render(src)
	if err != nil {
		if errors.Is(err, diagram.ErrUnsupported) {
			writeJSONResp(200, map[string]interface{}{
				"error":    "Unsupported diagram type. Supported: flowchart, sequenceDiagram.",
				"fallback": true,
			})
			return
		}
		writeJSONResp(200, map[string]interface{}{"error": err.Error(), "fallback": true})
		return
	}

	saveDiagramCache(hash, svg)
	writeJSONResp(200, map[string]string{"svg": svg})
}

func loadDiagramCache(hash string) string {
	if dbpkg.DB == nil {
		return ""
	}
	var svg string
	if err := dbpkg.DB.QueryRow(`SELECT svg FROM diagram_cache WHERE hash = ?`, hash).Scan(&svg); err != nil {
		return ""
	}
	return svg
}

func saveDiagramCache(hash, svg string) {
	if dbpkg.DB == nil {
		return
	}
	_, _ = dbpkg.DB.Exec(
		`INSERT INTO diagram_cache (hash, svg) VALUES (?, ?) ON CONFLICT(hash) DO NOTHING`,
		hash, svg)
}
