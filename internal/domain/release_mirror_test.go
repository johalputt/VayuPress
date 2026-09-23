// SPDX-License-Identifier: Apache-2.0

package domain

import "testing"

// The mirror switch lives in the same config_json envelope as the brand a client
// may edit, so both directions of the merge rule matter: turning the mirror on
// must not erase the brand, and a brand save must not turn the mirror off.
//
// And the envelope collapses to "" when every key is empty. A mirror-only
// config is not empty; if encodeConfig forgot this key, switching the mirror on
// for a domain with no brand would store nothing and the switch would silently
// never take.
func TestReleaseMirrorSwitchSurvivesTheEnvelope(t *testing.T) {
	raw, err := EncodeReleaseMirrorInto("", true)
	if err != nil {
		t.Fatal(err)
	}
	if !(Domain{ConfigJSON: raw}).ReleaseMirror() {
		t.Fatalf("stored %q — turning the mirror on for a domain with no other config stored nothing", raw)
	}

	withBrand, err := EncodeBrandConfigInto(raw, Brand{SiteName: "Updates"})
	if err != nil {
		t.Fatal(err)
	}
	d := Domain{ConfigJSON: withBrand}
	if !d.ReleaseMirror() {
		t.Fatal("saving a brand turned the release mirror off")
	}
	if b, ok := d.Brand(); !ok || b.SiteName != "Updates" {
		t.Fatal("the brand did not survive beside the mirror switch")
	}

	off, err := EncodeReleaseMirrorInto(withBrand, false)
	if err != nil {
		t.Fatal(err)
	}
	d = Domain{ConfigJSON: off}
	if d.ReleaseMirror() {
		t.Fatal("the mirror could not be switched off")
	}
	if b, ok := d.Brand(); !ok || b.SiteName != "Updates" {
		t.Fatal("switching the mirror off erased the brand")
	}
}
