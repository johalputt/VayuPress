package storage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// LocalBackend stores content-addressed files in a local directory.
// Files are stored as <dir>/<sha256hex> — immutable, deduplicated.
type LocalBackend struct {
	dir string
}

// NewLocal creates a LocalBackend storing files in dir.
func NewLocal(dir string) (*LocalBackend, error) {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, fmt.Errorf("storage.local: mkdir %s: %w", dir, err)
	}
	return &LocalBackend{dir: dir}, nil
}

// Put stores data and returns its SHA-256 hex content ID.
func (b *LocalBackend) Put(_ context.Context, data []byte, _ string) (string, error) {
	h := sha256.Sum256(data)
	id := hex.EncodeToString(h[:])
	path := filepath.Join(b.dir, id)
	if _, err := os.Stat(path); err == nil {
		return id, nil // already stored (content-addressed dedup)
	}
	if err := os.WriteFile(path, data, 0o640); err != nil {
		return "", fmt.Errorf("storage.local: write: %w", err)
	}
	return id, nil
}

// Get retrieves content by SHA-256 hex ID.
func (b *LocalBackend) Get(_ context.Context, id string) (io.ReadCloser, error) {
	path := filepath.Join(b.dir, id)
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("storage.local: get %s: %w", id, err)
	}
	return f, nil
}

// Name returns "local".
func (b *LocalBackend) Name() string { return "local" }
