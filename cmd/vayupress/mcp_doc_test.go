// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/apikeys"
)

// docs/compatibility/mcp.md listed fifteen tools and said the connector could
// not touch VayuShield while it served six VayuShield tools and twenty-odd for
// hosted sites. The page is held to the server: every tool a full-control key
// sees is in its table, and the table names no tool the server lacks.
func TestTheMCPPageListsEveryTool(t *testing.T) {
	rec := httptest.NewRecorder()
	(&App{}).buildMCPServer().ServeHTTP(rec, keyCtxRequest(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`,
		apikeys.SuperuserKeyInfo("s", "root", apikeys.ScopeExternal)))
	var out struct {
		Result struct {
			Tools []struct {
				Name string `json:"name"`
			} `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || len(out.Result.Tools) == 0 {
		t.Fatalf("tools/list: %v %s", err, rec.Body.String())
	}
	b, err := os.ReadFile(filepath.Join("..", "..", "docs", "compatibility", "mcp.md"))
	if err != nil {
		t.Fatal(err)
	}
	doc := string(b)
	table := doc[strings.Index(doc, "## Tools"):strings.Index(doc, "## Security model")]
	served := map[string]bool{}
	for _, tool := range out.Result.Tools {
		served[tool.Name] = true
		if !strings.Contains(table, "`"+tool.Name+"`") {
			t.Errorf("the server offers %s, which the MCP page's tool table does not list", tool.Name)
		}
	}
	for _, m := range regexp.MustCompile("`([a-z]+(?:_[a-z]+)+)`").FindAllStringSubmatch(table, -1) {
		if !served[m[1]] {
			t.Errorf("the MCP page lists %s, which the server does not offer", m[1])
		}
	}
}
