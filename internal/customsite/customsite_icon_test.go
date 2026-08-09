// SPDX-License-Identifier: Apache-2.0

package customsite

import (
	"os"
	"path/filepath"
	"testing"
)

// Driven against docs/site — the tree scripts/build-selfhosted-site.sh turns
// into the marketing site's bundle — because assuming the convention is exactly
// what went wrong. That site declares assets/favicon-32.png and carries nothing
// at its root, so a root-only lookup reported it as having no icon while every
// one of its pages referenced one.
func TestTheRealMarketingBundleDeclaresAnIconWeCanFind(t *testing.T) {
	src := filepath.Join("..", "..", "docs", "site")
	if _, err := os.Stat(filepath.Join(src, "index.html")); err != nil {
		t.Skip("docs/site not present")
	}
	base := t.TempDir()
	current := filepath.Join(base, "current")
	if err := os.MkdirAll(filepath.Join(current, "assets"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"index.html", "assets/favicon-32.png"} {
		b, err := os.ReadFile(filepath.Join(src, f))
		if err != nil {
			t.Fatalf("read %s from the real site source: %v", f, err)
		}
		if err := os.WriteFile(filepath.Join(current, f), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	got, ok := IconPath(base)
	if !ok {
		t.Fatal("the marketing bundle reports no icon.\n\n" +
			"Its index.html declares one and the file is in the bundle; a lookup that " +
			"misses it shows a generic globe for a site that has a logo.")
	}
	if got != "/assets/favicon-32.png" {
		t.Errorf("IconPath = %q, want /assets/favicon-32.png", got)
	}
}

// A bundle must not be able to point the console at another host, or at a file
// outside itself. The bundle is operator-uploaded content, so its markup is the
// least trusted input this function reads.
func TestADeclaredIconCannotLeaveTheBundle(t *testing.T) {
	for _, href := range []string{
		"https://evil.example/logo.png",
		"//evil.example/logo.png",
		"http://evil.example/logo.png",
		"data:image/png;base64,AAAA",
		"../../../etc/passwd",
		"/../../etc/passwd",
	} {
		base := t.TempDir()
		current := filepath.Join(base, "current")
		if err := os.MkdirAll(current, 0o755); err != nil {
			t.Fatal(err)
		}
		doc := `<!doctype html><html><head><link rel="icon" href="` + href + `"></head><body>x</body></html>`
		if err := os.WriteFile(filepath.Join(current, "index.html"), []byte(doc), 0o644); err != nil {
			t.Fatal(err)
		}
		if got, ok := IconPath(base); ok {
			t.Errorf("a bundle declaring href=%q resolved to %q; it must be refused", href, got)
		}
	}
}

// The off-origin refusal must be the URL check doing the work, not the file
// simply being absent.
//
// Mutation found this: deleting the scheme/host check left every case above
// passing, because "https://evil.example/logo.png" cleans to the local path
// "/https:/evil.example/logo.png" and no such file existed. A bundle is operator
// -uploaded content and can contain a file of ANY legal name, so the version
// that only checks existence would resolve an icon from a path that reads as
// another origin — and the next reader of that value has no way to tell.
func TestAnOffOriginHrefIsRefusedEvenWhenAFileOfThatNameExists(t *testing.T) {
	base := t.TempDir()
	current := filepath.Join(base, "current")
	// The path "https://evil.example/logo.png" cleans to.
	planted := filepath.Join(current, "https:", "evil.example")
	if err := os.MkdirAll(planted, 0o755); err != nil {
		t.Skipf("this filesystem cannot hold the planted name: %v", err)
	}
	if err := os.WriteFile(filepath.Join(planted, "logo.png"), []byte("planted"), 0o644); err != nil {
		t.Skipf("cannot write the planted file: %v", err)
	}
	doc := `<!doctype html><html><head>` +
		`<link rel="icon" href="https://evil.example/logo.png">` +
		`</head><body>x</body></html>`
	if err := os.WriteFile(filepath.Join(current, "index.html"), []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, ok := IconPath(base); ok {
		t.Errorf("an off-origin href resolved to %q because a file of that literal name "+
			"existed in the bundle; the refusal must come from reading the href, not from "+
			"the file happening to be absent", got)
	}
}
