// SPDX-License-Identifier: Apache-2.0

package customsite

import (
	"bytes"
	"strings"
	"testing"
)

// png is not text: a byte sequence no JSON string or UTF-8 reader carries
// intact, which is what an edit must leave alone.
var png = []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n', 0xff, 0x00, 0xfe}

func liveSite(t *testing.T) string {
	t.Helper()
	base := t.TempDir()
	z := zipOf(t, map[string]string{"index.html": "V1", "a.css": "body{}", "img/logo.png": string(png)})
	if _, err := deployBytes(base, z); err != nil {
		t.Fatal(err)
	}
	return base
}

func TestEditChangesOnlyWhatItNames(t *testing.T) {
	base := liveSite(t)
	m, err := Edit(base, map[string][]byte{"index.html": []byte("V2"), "new/page.html": []byte("P")}, []string{"a.css"}, 1<<30)
	if err != nil {
		t.Fatalf("Edit: %v", err)
	}
	if m.Files != 3 {
		t.Errorf("the edited site has %d files, want 3 (index, the new page, the untouched logo)", m.Files)
	}
	if got := serveGet(t, base, "/"); got != "V2" {
		t.Errorf("/ = %q after writing it, want V2", got)
	}
	if got := serveGet(t, base, "/new/page.html"); got != "P" {
		t.Errorf("the added page serves %q", got)
	}
	if servedTrue(base, "/a.css") {
		t.Error("a.css is still served after it was deleted")
	}
	if got, err := ReadFile(base, "img/logo.png"); err != nil || !bytes.Equal(got, png) {
		t.Errorf("a file the edit did not name changed: %v %q", err, got)
	}
	// The site it replaced is kept, like an upload's.
	if err := Rollback(base); err != nil {
		t.Fatal(err)
	}
	if got := serveGet(t, base, "/"); got != "V1" || !servedTrue(base, "/a.css") {
		t.Errorf("rolling back did not bring the site before the edit back: / = %q", got)
	}
}

func TestEditRefusesAndLeavesTheSiteAsItWas(t *testing.T) {
	// One seed per rule, and each asserts its own reason.
	cases := []struct {
		name   string
		put    map[string][]byte
		remove []string
		why    string
	}{
		{"nothing", nil, nil, "nothing to change"},
		{"escape", map[string][]byte{"../x.html": nil}, nil, "path traversal"},
		{"absolute", map[string][]byte{"/x.html": nil}, nil, "absolute path"},
		{"not a web file", map[string][]byte{"notes.md": nil}, nil, "only static web files"},
		// ._index.html has an allowed extension, so only the junk rule refuses it.
		{"system junk", map[string][]byte{"img/._index.html": nil}, nil, "only static web files"},
		{"deleting a missing file", nil, []string{"b.css"}, `no file "b.css" to delete`},
		{"deleting outside", nil, []string{"../manifest.json"}, "path traversal"},
		{"written and deleted", map[string][]byte{"a.css": nil}, []string{"a.css"}, "both written and deleted"},
		{"no front page", nil, []string{"index.html"}, "must contain an index.html"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			base := liveSite(t)
			_, err := Edit(base, c.put, c.remove, 1<<30)
			if err == nil || !strings.Contains(err.Error(), c.why) {
				t.Fatalf("Edit = %v, want a refusal saying %q", err, c.why)
			}
			if got := serveGet(t, base, "/"); got != "V1" || !servedTrue(base, "/a.css") {
				t.Errorf("a refused edit changed the live site: / = %q", got)
			}
			if len(History(base)) != 0 {
				t.Error("a refused edit was recorded as a deployment")
			}
		})
	}
}

func TestEditIsBoundByTheBudget(t *testing.T) {
	base := liveSite(t)
	if _, err := Edit(base, map[string][]byte{"big.html": bytes.Repeat([]byte("x"), 100)}, nil, 50); err == nil || !strings.Contains(err.Error(), ErrNoSpace.Error()) {
		t.Fatalf("Edit past the budget = %v, want %v", err, ErrNoSpace)
	}
}

func TestEditOnADomainWithNoSite(t *testing.T) {
	base := t.TempDir()
	if files, err := Files(base); err != nil || len(files) != 0 {
		t.Fatalf("Files with nothing deployed = %v, %v", files, err)
	}
	if _, err := Edit(base, map[string][]byte{"index.html": []byte("first")}, nil, 1<<30); err != nil {
		t.Fatalf("a first site written file by file: %v", err)
	}
	files, err := Files(base)
	if err != nil || len(files) != 1 || files[0] != (File{Path: "index.html", Size: 5}) {
		t.Errorf("Files = %+v, %v", files, err)
	}
}

func TestReadFileStaysInTheSite(t *testing.T) {
	base := liveSite(t)
	for name, why := range map[string]string{
		"../manifest.json": "path traversal",
		"missing.html":     `no file "missing.html"`,
	} {
		if _, err := ReadFile(base, name); err == nil || !strings.Contains(err.Error(), why) {
			t.Errorf("ReadFile(%q) = %v, want %q", name, err, why)
		}
	}
	if _, err := ReadFile(t.TempDir(), "index.html"); err == nil || !strings.Contains(err.Error(), "no website") {
		t.Errorf("ReadFile with nothing deployed = %v", err)
	}
}
