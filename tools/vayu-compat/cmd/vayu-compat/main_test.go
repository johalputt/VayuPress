// SPDX-License-Identifier: Apache-2.0

package main

import (
	"testing"

	"github.com/johalputt/vayupress/internal/vcb"
	"github.com/johalputt/vayupress/internal/vcb/validate"
)

// TestCheckManifestKindDetection proves the filename and shape sniffing route
// a manifest to the right validator.
func TestCheckManifestKindDetection(t *testing.T) {
	plugin := []byte(`{"vcb":1,"name":"p","version":"1.0.0","executable":"bin/p"}`)
	theme := []byte(`{"vcb":1,"tokens":{"Name":"T"}}`)

	r, err := checkManifest(plugin, "plugin.json", validate.Options{})
	if err != nil || r.Kind != "plugin" {
		t.Fatalf("plugin.json: kind=%v err=%v", r, err)
	}
	r, err = checkManifest(theme, "theme.json", validate.Options{})
	if err != nil || r.Kind != "theme" {
		t.Fatalf("theme.json: kind=%v err=%v", r, err)
	}
	// Unrecognised filename falls back to shape sniffing on "tokens".
	r, err = checkManifest(theme, "whatever.json", validate.Options{})
	if err != nil || r.Kind != "theme" {
		t.Fatalf("shape sniff: kind=%v err=%v", r, err)
	}
}

// TestUnknownFieldRejected pins the strict-parse behaviour: a typo like
// "api_permission" must be a hard parse error, never a silent no-grant.
func TestUnknownFieldRejected(t *testing.T) {
	bad := []byte(`{"vcb":1,"name":"p","version":"1.0.0","executable":"bin/p","api_permission":["posts:read"]}`)
	if _, err := vcb.ParsePluginManifest(bad); err == nil {
		t.Fatal("unknown manifest field must be rejected")
	}
}
