package update

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

// CreateBackup copies the SQLite DB (and its -wal/-shm sidecars if present) into
// destDir as a timestamped .tar.gz and returns the archive path. Pure stdlib.
func CreateBackup(dbPath, destDir string) (string, error) {
	if dbPath == "" {
		return "", fmt.Errorf("update: empty dbPath")
	}
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return "", fmt.Errorf("update: mkdir backup dir: %w", err)
	}

	ts := time.Now().UTC().Format("20060102T150405Z")
	base := filepath.Base(dbPath)
	archivePath := filepath.Join(destDir, fmt.Sprintf("backup-%s-%s.tar.gz", base, ts))

	out, err := os.Create(archivePath)
	if err != nil {
		return "", fmt.Errorf("update: create archive: %w", err)
	}
	defer out.Close()

	gz := gzip.NewWriter(out)
	defer gz.Close()
	tw := tar.NewWriter(gz)
	defer tw.Close()

	// Include the main DB and any sidecar files that exist.
	candidates := []string{dbPath, dbPath + "-wal", dbPath + "-shm"}
	added := false
	for _, p := range candidates {
		fi, statErr := os.Stat(p)
		if statErr != nil {
			continue // sidecar (or db) absent — skip
		}
		if err := addFileToTar(tw, p, fi); err != nil {
			return "", err
		}
		added = true
	}
	if !added {
		return "", fmt.Errorf("update: nothing to back up — %q not found", dbPath)
	}
	return archivePath, nil
}

func addFileToTar(tw *tar.Writer, path string, fi os.FileInfo) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("update: open %s: %w", path, err)
	}
	defer f.Close()

	hdr := &tar.Header{
		Name:    filepath.Base(path),
		Mode:    int64(fi.Mode().Perm()),
		Size:    fi.Size(),
		ModTime: fi.ModTime(),
	}
	if err := tw.WriteHeader(hdr); err != nil {
		return fmt.Errorf("update: tar header %s: %w", path, err)
	}
	if _, err := io.Copy(tw, f); err != nil {
		return fmt.Errorf("update: tar copy %s: %w", path, err)
	}
	return nil
}
