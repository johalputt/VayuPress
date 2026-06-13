package archive_test

import (
	"os"
	"testing"

	"github.com/johalputt/vayupress/internal/archive"
)

func TestCreateAndList(t *testing.T) {
	tmpDir := t.TempDir()
	mgr, err := archive.NewManager(tmpDir + "/snapshots")
	if err != nil {
		t.Fatal(err)
	}

	// Create a test source file
	src := tmpDir + "/data.txt"
	if err := os.WriteFile(src, []byte("hello world"), 0o644); err != nil {
		t.Fatal(err)
	}

	path, err := mgr.Create("snap-001", []string{src})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("snapshot file missing: %v", err)
	}

	ids, err := mgr.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(ids) != 1 || ids[0] != "snap-001" {
		t.Errorf("expected [snap-001], got %v", ids)
	}
}
