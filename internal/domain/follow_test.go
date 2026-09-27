// SPDX-License-Identifier: Apache-2.0

package domain

import "testing"

// What a domain follows shares the config_json envelope with the brand, the
// website and the mirror switch, so the merge rule is tested both ways, and a
// follow-only config must not collapse to "" (which would store nothing).
func TestFollowSurvivesTheEnvelope(t *testing.T) {
	want := Follow{Repo: "johalputt/vayupress", Branch: "main", Site: "updates"}
	raw, err := EncodeFollowInto("", &want)
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := (Domain{ConfigJSON: raw}).Follow(); !ok || got != want {
		t.Fatalf("stored %q — following a repository on a domain with no other config stored %+v", raw, got)
	}

	withMirror, err := EncodeReleaseMirrorInto(raw, true)
	if err != nil {
		t.Fatal(err)
	}
	d := Domain{ConfigJSON: withMirror}
	if got, ok := d.Follow(); !ok || got != want || !d.ReleaseMirror() {
		t.Fatalf("switching the mirror on lost what the domain follows: %+v", got)
	}

	off, err := EncodeFollowInto(withMirror, nil)
	if err != nil {
		t.Fatal(err)
	}
	d = Domain{ConfigJSON: off}
	if _, ok := d.Follow(); ok {
		t.Fatal("a domain could not stop following")
	}
	if !d.ReleaseMirror() {
		t.Fatal("stopping the follow switched the mirror off")
	}
}
