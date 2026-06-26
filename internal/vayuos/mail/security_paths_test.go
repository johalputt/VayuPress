package mail

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestMaildirRejectsPathTraversal proves a hostile domain/username can never
// escape the Maildir base directory (CWE-22).
func TestMaildirRejectsPathTraversal(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	md := NewMaildir(base)

	cases := []struct{ domain, user string }{
		{"../../etc", "passwd"},
		{"example.com", "../../../../tmp/evil"},
		{"..", ".."},
		{"a/../../b", "c/d"},
		{"example.com", "bob"},
	}
	for _, c := range cases {
		if _, err := md.Deliver(c.domain, c.user, []byte("X-Test: 1\r\n\r\nhi")); err != nil {
			t.Fatalf("deliver(%q,%q): %v", c.domain, c.user, err)
		}
	}
	// Every file written must live under base — nothing escaped.
	baseAbs, _ := filepath.Abs(base)
	err := filepath.Walk(base, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		abs, _ := filepath.Abs(p)
		if !strings.HasPrefix(abs, baseAbs) {
			t.Fatalf("path escaped base: %s", abs)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	// The traversal segments must have been neutralised (no parent dirs created
	// outside base).
	if _, err := os.Stat(filepath.Join(filepath.Dir(baseAbs), "etc")); err == nil {
		t.Fatalf("traversal created a sibling 'etc' dir outside base")
	}
}

func TestSafeSegment(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"example.com": "example.com",
		"bob":         "bob",
		"../../etc":   "etc",
		"a/b/c":       "c",
		"..":          "_",
		"":            "_",
		"  spaced  ":  "spaced",
		"/abs/path/x": "x",
		".Sent":       ".Sent",
	}
	for in, want := range cases {
		if got := safeSegment(in); got != want {
			t.Errorf("safeSegment(%q) = %q, want %q", in, got, want)
		}
	}
}
