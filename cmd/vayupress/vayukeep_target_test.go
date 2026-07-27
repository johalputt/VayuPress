// SPDX-License-Identifier: Apache-2.0

package main

// vayukeep_target_test.go — the backup folder arrives from a form field and flows
// into MkdirAll and CreateTemp. CodeQL flagged it as untrusted data in a path
// expression (findings #104/#105) and was right: an `IsAbs` check is not a
// barrier. These cases pin the allow-list that now stands between the two.

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestSanitizeKeepTargetRejectsDangerousPaths(t *testing.T) {
	bad := []struct{ in, why string }{
		{"", "empty"},
		{"   ", "blank"},
		{"var/backups", "relative — would resolve against the working directory"},
		{"./backups", "relative"},
		{"~/backups", "shell expansion is not path resolution"},
		{"/etc", "a system directory"},
		{"/etc/vayupress", "inside a system directory"},
		{"/", "the root filesystem"},
		{"/usr/local/backups", "inside /usr"},
		{"/proc/self", "kernel interface"},
		{"/sys/kernel", "kernel interface"},
		{"/boot/backups", "boot partition"},
		{"/dev/shm/x", "device tree"},
		{"/var", "a permitted root, but not a folder inside it"},
		{"/mnt", "a permitted root itself"},
		{"/variant/backups", "starts with /var but is NOT under it"},
		{"/opt-old/backups", "starts with /opt but is NOT under it"},
		{"/var/backups\x00/etc", "embedded NUL"},
		{"/var/backups\nrm -rf", "embedded newline"},
		{"/var/backups\tx", "embedded control character"},
	}
	for _, c := range bad {
		if got, err := sanitizeKeepTarget(c.in); err == nil {
			t.Errorf("sanitizeKeepTarget(%q) accepted it as %q — %s", c.in, got, c.why)
		}
	}
}

func TestSanitizeKeepTargetAcceptsRealBackupLocations(t *testing.T) {
	good := map[string]string{
		"/var/backups/vayupress":   "/var/backups/vayupress",
		"/mnt/backup/vayu":         "/mnt/backup/vayu",
		"/media/usb1/vayupress":    "/media/usb1/vayupress",
		"/srv/backups":             "/srv/backups",
		"/opt/vayu/backups":        "/opt/vayu/backups",
		"/home/ankush/backups":     "/home/ankush/backups",
		"  /var/backups/vayu  ":    "/var/backups/vayu",
		"/var/backups//vayu/":      "/var/backups/vayu",
		"/var/backups/./vayu":      "/var/backups/vayu",
		"/var/tmp/../backups/vayu": "/var/backups/vayu",
	}
	for in, want := range good {
		got, err := sanitizeKeepTarget(in)
		if err != nil {
			t.Errorf("sanitizeKeepTarget(%q) refused a legitimate location: %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("sanitizeKeepTarget(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestSanitizeKeepTargetNeutralisesTraversal — the returned value must be the
// cleaned one, so a path that walks out of an allowed root is either rejected or
// resolved before it can reach a filesystem call.
func TestSanitizeKeepTargetNeutralisesTraversal(t *testing.T) {
	for _, in := range []string{
		"/var/../etc/vayupress",
		"/var/backups/../../etc",
		"/mnt/../../../root",
		"/opt/../usr/lib",
	} {
		got, err := sanitizeKeepTarget(in)
		if err != nil {
			continue // rejected outright is the correct outcome too
		}
		if strings.Contains(got, "..") {
			t.Errorf("sanitizeKeepTarget(%q) returned an unresolved traversal: %q", in, got)
		}
		// If it was accepted, the CLEANED result must still be under an allowed root.
		ok := false
		for _, root := range keepTargetRoots {
			if strings.HasPrefix(got, root+"/") {
				ok = true
				break
			}
		}
		if !ok {
			t.Errorf("sanitizeKeepTarget(%q) escaped the allow-list: %q", in, got)
		}
	}
}

// TestValidateKeepTargetWritableSanitisesItsOwnInput — the write test is the
// second place raw input could reach the filesystem, so it must not depend on the
// caller having sanitised first.
func TestValidateKeepTargetWritableSanitisesItsOwnInput(t *testing.T) {
	for _, in := range []string{"/etc/vayupress-probe", "relative/path", "/proc/self/x", "/"} {
		if err := validateKeepTargetWritable(in); err == nil {
			t.Errorf("validateKeepTargetWritable(%q) did not refuse an unsafe path", in)
		}
	}
}

// TestSanitizeKeepTargetRebuildsFromAConstantRoot — the value that reaches a
// filesystem call must be assembled from a package constant plus a component
// proven not to escape, never the operator's string with a check performed near
// it. That distinction is what makes this a barrier rather than a comment.
func TestSanitizeKeepTargetRebuildsFromAConstantRoot(t *testing.T) {
	got, err := sanitizeKeepTarget("/var/backups/vayupress")
	if err != nil {
		t.Fatalf("refused a legitimate location: %v", err)
	}
	underRoot := false
	for _, root := range keepTargetRoots {
		rel, rerr := filepath.Rel(root, got)
		if rerr != nil {
			continue
		}
		if rel == ".." || strings.HasPrefix(rel, "../") || filepath.IsAbs(rel) {
			continue
		}
		underRoot = true
		break
	}
	if !underRoot {
		t.Fatalf("the returned path %q is not contained by any allowed root", got)
	}

	// Every accepted result must be clean and absolute, whatever the input looked
	// like — no leftover separators, dot segments or traversal.
	for _, in := range []string{
		"/var//backups///vayu", "/var/backups/./vayu", "/var/x/../backups/vayu", "/mnt/./usb/../usb/vayu",
	} {
		out, err := sanitizeKeepTarget(in)
		if err != nil {
			continue
		}
		if out != filepath.Clean(out) || !filepath.IsAbs(out) {
			t.Errorf("sanitizeKeepTarget(%q) = %q, which is not a clean absolute path", in, out)
		}
		if strings.Contains(out, "..") || strings.Contains(out, "//") {
			t.Errorf("sanitizeKeepTarget(%q) = %q still carries traversal or empty segments", in, out)
		}
	}
}
