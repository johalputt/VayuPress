// Package archive provides immutable snapshot management for VayuPress.
// Snapshots capture the database and static assets at a point in time,
// enabling restore, audit, and historical replay.
package archive

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

// Manifest describes a snapshot.
type Manifest struct {
	ID        string    `json:"id"`
	CreatedAt time.Time `json:"created_at"`
	Files     []FileEntry `json:"files"`
}

// FileEntry is a single file in a snapshot with its checksum.
type FileEntry struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

// Manager manages snapshots in a directory.
type Manager struct {
	dir string
}

// NewManager creates a Manager that stores snapshots in dir.
func NewManager(dir string) (*Manager, error) {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, fmt.Errorf("archive: mkdir %s: %w", dir, err)
	}
	return &Manager{dir: dir}, nil
}

// Create takes a snapshot of the files in sourcePaths.
// Returns the path to the created .tar.gz snapshot.
func (m *Manager) Create(id string, sourcePaths []string) (string, error) {
	outPath := filepath.Join(m.dir, id+".tar.gz")
	f, err := os.Create(outPath)
	if err != nil {
		return "", fmt.Errorf("archive: create %s: %w", outPath, err)
	}
	defer f.Close()

	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)

	manifest := Manifest{ID: id, CreatedAt: time.Now().UTC()}

	for _, src := range sourcePaths {
		entry, err := addFile(tw, src)
		if err != nil {
			return "", fmt.Errorf("archive: add %s: %w", src, err)
		}
		manifest.Files = append(manifest.Files, entry)
	}

	// Embed manifest.json in the archive.
	manifestJSON, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return "", err
	}
	hdr := &tar.Header{Name: "manifest.json", Size: int64(len(manifestJSON)), Mode: 0o644}
	if err := tw.WriteHeader(hdr); err != nil {
		return "", err
	}
	if _, err := tw.Write(manifestJSON); err != nil {
		return "", err
	}

	if err := tw.Close(); err != nil {
		return "", err
	}
	if err := gz.Close(); err != nil {
		return "", err
	}
	return outPath, nil
}

// List returns IDs of all snapshots in the manager directory.
func (m *Manager) List() ([]string, error) {
	entries, err := os.ReadDir(m.dir)
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".gz" {
			ids = append(ids, e.Name()[:len(e.Name())-7]) // strip .tar.gz
		}
	}
	return ids, nil
}

func addFile(tw *tar.Writer, src string) (FileEntry, error) {
	f, err := os.Open(src)
	if err != nil {
		return FileEntry{}, err
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return FileEntry{}, err
	}

	hdr, err := tar.FileInfoHeader(info, "")
	if err != nil {
		return FileEntry{}, err
	}
	hdr.Name = filepath.Base(src)
	if err := tw.WriteHeader(hdr); err != nil {
		return FileEntry{}, err
	}

	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(tw, h), f); err != nil {
		return FileEntry{}, err
	}

	return FileEntry{
		Path:   hdr.Name,
		SHA256: hex.EncodeToString(h.Sum(nil)),
		Size:   info.Size(),
	}, nil
}
