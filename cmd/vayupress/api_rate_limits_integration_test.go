// SPDX-License-Identifier: Apache-2.0

//go:build integration

package main

import (
	"net/http"
	"strings"
	"testing"
)

// An agent calling the API or MCP with a valid key is held to its key's
// budget alone: a per-address cap of 100 calls an hour stood behind the key
// check and stopped working agents within minutes.
func TestAnAgentIsNotCappedByItsAddress(t *testing.T) {
	srv, key := newTestHarness(t)
	for i := 1; i <= 120; i++ {
		req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/v1/queue", nil)
		req.Header.Set("X-API-Key", key)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode == http.StatusTooManyRequests {
			t.Fatalf("API call %d from one address was refused (429)", i)
		}
		req, _ = http.NewRequest(http.MethodPost, srv.URL+"/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
		req.Header.Set("Authorization", "Bearer "+key)
		req.Header.Set("Content-Type", "application/json")
		resp, err = http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode == http.StatusTooManyRequests {
			t.Fatalf("MCP call %d from one address was refused (429)", i)
		}
	}
}
