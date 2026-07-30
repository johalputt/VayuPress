// SPDX-License-Identifier: Apache-2.0

package main

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestAssertUnderKeepRootRefusesEscape holds the point-of-use containment check
// to the same standard as the sanitiser that precedes it. It is deliberately
// tested on its own, with raw hostile input, rather than only through
// validateKeepTargetWritable: the reason it exists is that a future caller might
// reach a filesystem call without passing through sanitizeKeepTarget first, and
// a test that always goes through the sanitiser could not detect that.
func TestAssertUnderKeepRootRefusesEscape(t *testing.T) {
	bad := []struct{ in, why string }{
		{"", "empty"},
		{"var/backups", "relative — resolves against the working directory"},
		{"./backups", "relative"},
		{"/", "the root filesystem"},
		{"/var", "a permitted root, but not a folder inside it"},
		{"/mnt", "a permitted root itself"},
		{"/etc/vayupress", "a system directory"},
		{"/usr/local/backups", "outside every permitted root"},
		{"/proc/self", "kernel interface"},
		{"/variant/backups", "starts with /var but is NOT under it"},
		{"/opt-old/backups", "starts with /opt but is NOT under it"},
		{"/var/../etc/shadow", "traversal that resolves outside"},
		{"/var/backups/../../etc", "traversal below a valid root"},
	}
	for _, c := range bad {
		if got, err := assertUnderKeepRoot(c.in); err == nil {
			t.Errorf("assertUnderKeepRoot(%q) accepted it as %q — %s", c.in, got, c.why)
		}
	}

	good := map[string]string{
		"/var/backups/vayupress":  "/var/backups/vayupress",
		"/mnt/disk1/vk":           "/mnt/disk1/vk",
		"/backups/nightly":        "/backups/nightly",
		"/var/backups/vayupress/": "/var/backups/vayupress", // Clean drops the slash
		// ".." inside a NAME is legal and must be accepted. The first version of
		// the containment check tested for the substring and refused this, which
		// is a folder an operator is entitled to choose.
		"/var/back..ups": "/var/back..ups",
		"/mnt/..hidden":  "/mnt/..hidden",
	}
	for in, want := range good {
		got, err := assertUnderKeepRoot(in)
		if err != nil {
			t.Errorf("assertUnderKeepRoot(%q) refused a legitimate location: %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("assertUnderKeepRoot(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestSanitiserAndAssertionAgree pins the two checks to the same verdict. They
// are written differently on purpose — one resolves the relationship with
// filepath.Rel, the other tests the resolved prefix — so agreement is evidence
// rather than a restatement. A disagreement means one of them is wrong.
func TestSanitiserAndAssertionAgree(t *testing.T) {
	cases := []string{
		"/var/backups/vayupress", "/mnt/x/y", "/etc/passwd", "/var", "/",
		"/variant/backups", "/opt-old/x", "/usr/share/x", "relative/path",
		"/var/../etc", "/backups/a/b/c", "/home/johal/backups", "/proc/self",
	}
	for _, in := range cases {
		sanitized, serr := sanitizeKeepTarget(in)
		if serr != nil {
			// Whatever the sanitiser refuses, the assertion must refuse too — it is
			// the last line before the filesystem call.
			if _, aerr := assertUnderKeepRoot(filepath.Clean(in)); aerr == nil {
				t.Errorf("%q: sanitiser refused (%v) but the point-of-use assertion accepted it",
					in, serr)
			}
			continue
		}
		got, aerr := assertUnderKeepRoot(sanitized)
		if aerr != nil {
			t.Errorf("%q: sanitiser produced %q but the assertion then refused it: %v",
				in, sanitized, aerr)
			continue
		}
		if got != sanitized {
			t.Errorf("%q: assertion rewrote the sanitised path %q to %q", in, sanitized, got)
		}
		if !strings.HasPrefix(got, "/") {
			t.Errorf("%q: accepted a non-absolute path %q", in, got)
		}
	}
}
