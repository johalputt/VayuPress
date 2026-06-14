package main

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
)

func TestOpenAPISpecIsValidJSON(t *testing.T) {
	var doc map[string]interface{}
	if err := json.Unmarshal(openapiSpec, &doc); err != nil {
		t.Fatalf("embedded openapi.json is not valid JSON: %v", err)
	}
	if doc["openapi"] == nil || doc["paths"] == nil {
		t.Fatalf("openapi spec missing required top-level keys")
	}

	rec := httptest.NewRecorder()
	(&App{}).handleOpenAPISpec(rec, httptest.NewRequest("GET", "/api/v1/openapi.json", nil))
	if rec.Code != 200 {
		t.Fatalf("handler: want 200, got %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json; charset=utf-8" {
		t.Fatalf("unexpected content-type: %q", ct)
	}
}
