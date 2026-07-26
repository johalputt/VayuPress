// SPDX-License-Identifier: Apache-2.0

package vayutor

import (
	"strings"
	"testing"
)

func TestParseTorVersion(t *testing.T) {
	cases := []struct {
		in   string
		want torVersion
		ok   bool
	}{
		{"Tor version 0.4.8.13.", torVersion{0, 4, 8, 13}, true},
		{"Tor version 0.4.2.7 (git-x).", torVersion{0, 4, 2, 7}, true},
		{"14.0.1", torVersion{14, 0, 1, 0}, true},
		{"14.5", torVersion{14, 5, 0, 0}, true},
		{"no version here", torVersion{}, false},
	}
	for _, c := range cases {
		got, ok := parseTorVersion(c.in)
		if ok != c.ok || got != c.want {
			t.Errorf("parseTorVersion(%q) = %v,%v want %v,%v", c.in, got, ok, c.want, c.ok)
		}
	}
}

func TestTorVersionAtLeast(t *testing.T) {
	min := torVersion{0, 4, 7, 0}
	older := []string{"0.4.2.7", "0.4.6.10", "0.3.5.19", "0.4.7"} // 0.4.7 == min → atLeast true
	newer := []string{"0.4.7.1", "0.4.8.13", "0.5.0.0", "1.0.0.0"}
	// 0.4.7.0 (== min) must satisfy atLeast.
	if v, _ := parseTorVersion("0.4.7"); !v.atLeast(min) {
		t.Errorf("0.4.7 should be atLeast %v", min)
	}
	for _, s := range older {
		v, _ := parseTorVersion(s)
		if s == "0.4.7" {
			continue
		}
		if v.atLeast(min) {
			t.Errorf("%s should NOT be atLeast %v", s, min)
		}
	}
	for _, s := range newer {
		v, _ := parseTorVersion(s)
		if !v.atLeast(min) {
			t.Errorf("%s should be atLeast %v", s, min)
		}
	}
}

func TestParseDistIndexSortsAndCaps(t *testing.T) {
	// Apache-style autoindex snippet, deliberately unordered, with an alpha dir
	// that must be excluded and a duplicate that must be de-duped.
	body := `
	<a href="13.0.9/">13.0.9/</a>
	<a href="14.0.1/">14.0.1/</a>
	<a href="14.5a1/">14.5a1/</a>
	<a href="14.0.1/">14.0.1/</a>
	<a href="12.5.6/">12.5.6/</a>
	<a href="14.0.10/">14.0.10/</a>
	`
	got := parseDistIndex(body, 3)
	want := []string{"14.0.10", "14.0.1", "13.0.9"}
	if len(got) != len(want) {
		t.Fatalf("parseDistIndex len = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("parseDistIndex[%d] = %q, want %q (full %v)", i, got[i], want[i], got)
		}
	}
	for _, v := range got {
		if strings.Contains(v, "a") {
			t.Errorf("alpha version leaked into results: %q", v)
		}
	}
}

func TestExpertBundleURLs(t *testing.T) {
	urls := expertBundleURLs("14.0.1")
	if len(urls) != 4 {
		t.Fatalf("want 4 candidate URLs, got %d: %v", len(urls), urls)
	}
	// Must cover both the primary dist host and the archive mirror, and both the
	// current and legacy filename conventions.
	joined := strings.Join(urls, "\n")
	for _, want := range []string{
		"dist.torproject.org/torbrowser/14.0.1/tor-expert-bundle-linux-x86_64-14.0.1.tar.gz",
		"archive.torproject.org/tor-package-archive/torbrowser/14.0.1/tor-expert-bundle-linux-x86_64-14.0.1.tar.gz",
		"tor-expert-bundle-14.0.1-linux-x86_64.tar.gz",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("expertBundleURLs missing %q in:\n%s", want, joined)
		}
	}
	for _, u := range urls {
		if !strings.HasPrefix(u, "https://") {
			t.Errorf("non-HTTPS candidate URL: %q", u)
		}
	}
}

// TestDistDisabledKeepsLegacyResolve verifies that with managed download OFF, a
// managedTor never attempts a version check or download — resolveBinary just
// uses whatever tor is on PATH (or "" if none), preserving pre-feature behaviour.
func TestDistDisabledKeepsLegacyResolve(t *testing.T) {
	m := newManagedTor("", t.TempDir())
	if m.distIsEnabled() {
		t.Fatal("managed download should be OFF by default")
	}
	// With an explicit binary override, resolveBinary must return it verbatim
	// regardless of dist settings.
	m2 := newManagedTor("/opt/custom/tor", t.TempDir())
	if got := m2.resolveBinary(); got != "/opt/custom/tor" {
		t.Errorf("resolveBinary override = %q, want /opt/custom/tor", got)
	}
}

func TestLibDirForOnlyManagedBinary(t *testing.T) {
	m := newManagedTor("", t.TempDir())
	if got := m.libDirFor("/usr/bin/tor"); got != "" {
		t.Errorf("libDirFor(system tor) = %q, want empty", got)
	}
	// Simulate a resolved managed binary.
	m.distMu.Lock()
	m.distBin = "/var/lib/vayupress/tor/dist/tor"
	m.distLibDir = "/var/lib/vayupress/tor/dist"
	m.distMu.Unlock()
	if got := m.libDirFor(m.distBin); got != m.distLibDir {
		t.Errorf("libDirFor(managed) = %q, want %q", got, m.distLibDir)
	}
	if got := m.libDirFor("/usr/bin/tor"); got != "" {
		t.Errorf("libDirFor(other) = %q, want empty", got)
	}
}
