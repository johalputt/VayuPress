package main

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/apikeys"
)

// TestVCBRouteCapability pins the VCB surface to plugins:read for EVERY verb —
// validation is a pure function of the request body, so even POST must not
// demand a write grant, and a posts-only key must be refused.
func TestVCBRouteCapability(t *testing.T) {
	for _, method := range []string{"GET", "POST"} {
		for _, path := range []string{"/api/v1/vcb/contract", "/api/v1/vcb/validate"} {
			sec, act, ok := capabilityFor(method, path)
			if !ok || sec != apikeys.SectionPlugins || act != apikeys.ActionRead {
				t.Errorf("capabilityFor(%s %s) = %v:%v ok=%v, want plugins:read", method, path, sec, act, ok)
			}
		}
	}

	reader := apikeys.NewPermissions()
	reader.Grant(apikeys.SectionPlugins, apikeys.ActionRead)
	ki := apikeys.KeyInfo{ID: "k", Scope: apikeys.ScopeExternal, Perms: reader}
	if !keyMayCall(ki, "POST", "/api/v1/vcb/validate") {
		t.Error("a plugins:read key must be able to validate manifests")
	}

	postsOnly := apikeys.NewPermissions()
	postsOnly.Grant(apikeys.SectionPosts, apikeys.ActionWrite)
	other := apikeys.KeyInfo{ID: "p", Scope: apikeys.ScopeExternal, Perms: postsOnly}
	if keyMayCall(other, "GET", "/api/v1/vcb/contract") {
		t.Error("a posts-only key must NOT reach the VCB surface")
	}
}

// TestVCBContractEndpoint verifies the discovery document carries every
// vocabulary an extension author needs, straight from the live enums.
func TestVCBContractEndpoint(t *testing.T) {
	a := &App{}
	rec := httptest.NewRecorder()
	a.handleVCBContract(rec, httptest.NewRequest("GET", "/api/v1/vcb/contract", nil))
	if rec.Code != 200 {
		t.Fatalf("contract = %d, want 200", rec.Code)
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("contract not JSON: %v", err)
	}
	for _, key := range []string{"host_version", "manifest_version", "hooks", "webhook_events", "sections", "actions", "theme_categories", "theme_options", "theme_option_keys", "docs"} {
		if _, ok := doc[key]; !ok {
			t.Errorf("contract missing %q", key)
		}
	}
	if !strings.Contains(rec.Body.String(), "article.create") {
		t.Error("contract must list the real hook catalogue")
	}
}

// TestVCBValidateEndpoint runs both manifest kinds through the endpoint: a
// valid plugin passes, a manifest with an uncatalogued hook fails with the
// stable machine code, a theme is kind-sniffed via its tokens, and junk is 400.
func TestVCBValidateEndpoint(t *testing.T) {
	a := &App{}
	type verdict struct {
		Kind string `json:"kind"`
		Name string `json:"name"`
		OK   bool   `json:"ok"`
	}
	post := func(body string) (*httptest.ResponseRecorder, verdict) {
		rec := httptest.NewRecorder()
		a.handleVCBValidate(rec, httptest.NewRequest("POST", "/api/v1/vcb/validate", strings.NewReader(body)))
		var v verdict
		_ = json.Unmarshal(rec.Body.Bytes(), &v)
		return rec, v
	}

	rec, v := post(`{"vcb":1,"name":"ok-plugin","version":"1.0.0","hooks":["article.create"],"executable":"bin/p"}`)
	if rec.Code != 200 || !v.OK || v.Kind != "plugin" {
		t.Errorf("valid plugin: code=%d verdict=%+v body=%s", rec.Code, v, rec.Body.String())
	}

	rec, v = post(`{"vcb":1,"name":"bad-plugin","version":"1.0.0","hooks":["article.created.v1"],"executable":"bin/p"}`)
	if rec.Code != 200 || v.OK || !strings.Contains(rec.Body.String(), "plugin.hook.unknown") {
		t.Errorf("unknown hook must fail with its machine code: %s", rec.Body.String())
	}

	rec, v = post(`{"vcb":1,"tokens":{"Name":"Sniffed Theme","BgDark":"#0b1020"}}`)
	if rec.Code != 200 || v.Kind != "theme" || v.Name != "Sniffed Theme" {
		t.Errorf("theme kind-sniffing failed: code=%d verdict=%+v", rec.Code, v)
	}

	rec, _ = post(`{"vcb":1,"name":"typo","version":"1.0.0","executable":"bin/p","api_permission":["posts:read"]}`)
	if rec.Code != 400 {
		t.Errorf("unknown manifest field must 400 (strict parse), got %d", rec.Code)
	}

	rec, _ = post(`not json`)
	if rec.Code != 400 {
		t.Errorf("junk body must 400, got %d", rec.Code)
	}
}
