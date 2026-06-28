package update

import (
	"os"
	"path/filepath"
	"testing"
)

// TestCleanExePathStripsDeleted proves the core self-update activation fix: the
// "(deleted)" marker the kernel appends to a swapped binary's path is removed so
// the re-exec targets the file that now holds the NEW binary, not the unlinked
// old inode.
func TestCleanExePathStripsDeleted(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "vayupress")
	if err := os.WriteFile(real, []byte("#!/bin/true\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	// A real, existing path resolves to itself (symlinks resolved).
	if got := cleanExePath(real); got != real {
		t.Errorf("clean(%q) = %q, want %q", real, got, real)
	}

	// The kernel's post-swap form "<path> (deleted)" must strip back to the path
	// that now exists on disk.
	if got := cleanExePath(real + " (deleted)"); got != real {
		t.Errorf("clean(deleted) = %q, want %q", got, real)
	}

	// Empty stays empty (RelaunchExec rejects it).
	if got := cleanExePath(""); got != "" {
		t.Errorf("clean(\"\") = %q, want empty", got)
	}
}

// TestCleanExePathSymlinkResolved proves a symlinked install path resolves to
// the real binary so execve launches the actual file.
func TestCleanExePathSymlinkResolved(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "vayupress.real")
	link := filepath.Join(dir, "vayupress")
	if err := os.WriteFile(real, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlinks unsupported: %v", err)
	}
	if got := cleanExePath(link); got != real {
		t.Errorf("clean(symlink) = %q, want %q", got, real)
	}
}
