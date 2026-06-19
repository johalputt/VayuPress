package backup

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

const VayuVersion = "1.0.0"

// FileEntry represents a file in the backup manifest.
type FileEntry struct {
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

// Manifest describes the backup archive contents.
type Manifest struct {
	CreatedAt    time.Time   `json:"created_at"`
	VayuVersion  string      `json:"vayu_version"`
	ArticleCount int64       `json:"article_count"`
	Files        []FileEntry `json:"files"`
}

// Options configures a backup operation.
type Options struct {
	DBPath   string
	OutPath  string
	Compress bool
}

// Create creates a backup archive from the given SQLite database.
func Create(opts Options) (string, error) {
	// Open the source DB to count articles and vacuum into temp file.
	db, err := sql.Open("sqlite3", opts.DBPath)
	if err != nil {
		return "", fmt.Errorf("open db: %w", err)
	}
	defer db.Close()

	if err := db.Ping(); err != nil {
		return "", fmt.Errorf("ping db: %w", err)
	}

	var articleCount int64
	row := db.QueryRow("SELECT COUNT(*) FROM articles")
	if err := row.Scan(&articleCount); err != nil {
		// Table may not exist yet; treat as 0.
		articleCount = 0
	}

	// VACUUM INTO a temp file.
	tmpDB, err := os.CreateTemp("", "vayu-backup-*.db")
	if err != nil {
		return "", fmt.Errorf("create temp db: %w", err)
	}
	tmpDBPath := tmpDB.Name()
	tmpDB.Close()
	defer os.Remove(tmpDBPath)

	if _, err := db.Exec(fmt.Sprintf("VACUUM INTO '%s'", tmpDBPath)); err != nil {
		return "", fmt.Errorf("vacuum into: %w", err)
	}

	// Compute sha256 of the vacuumed db.
	dbChecksum, dbSize, err := checksumFile(tmpDBPath)
	if err != nil {
		return "", fmt.Errorf("checksum db: %w", err)
	}

	manifest := Manifest{
		CreatedAt:    time.Now().UTC(),
		VayuVersion:  VayuVersion,
		ArticleCount: articleCount,
		Files: []FileEntry{
			{Name: "vayupress.db", Size: dbSize, SHA256: dbChecksum},
		},
	}

	manifestJSON, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal manifest: %w", err)
	}

	// Determine output path.
	outPath := opts.OutPath
	if outPath == "" {
		outPath = fmt.Sprintf("vayupress-backup-%s.tar.gz", time.Now().Format("2006-01-02"))
	}
	// If outPath is a directory, generate a filename inside it.
	if info, err := os.Stat(outPath); err == nil && info.IsDir() {
		outPath = filepath.Join(outPath, fmt.Sprintf("vayupress-backup-%s.tar.gz", time.Now().Format("2006-01-02")))
	}

	outFile, err := os.Create(outPath)
	if err != nil {
		return "", fmt.Errorf("create output file: %w", err)
	}
	defer outFile.Close()

	var tw *tar.Writer
	if opts.Compress {
		gw := gzip.NewWriter(outFile)
		defer gw.Close()
		tw = tar.NewWriter(gw)
	} else {
		tw = tar.NewWriter(outFile)
	}
	defer tw.Close()

	// Add vayupress.db.
	if err := addFileToTar(tw, tmpDBPath, "vayupress.db"); err != nil {
		return "", fmt.Errorf("add db to tar: %w", err)
	}

	// Add manifest.
	if err := addBytesToTar(tw, manifestJSON, "backup-manifest.json"); err != nil {
		return "", fmt.Errorf("add manifest to tar: %w", err)
	}

	return outPath, nil
}

// Verify checks the integrity of a backup archive by validating SHA256 checksums.
func Verify(archivePath string) error {
	manifest, files, err := readArchive(archivePath)
	if err != nil {
		return err
	}

	for _, entry := range manifest.Files {
		data, ok := files[entry.Name]
		if !ok {
			return fmt.Errorf("file %q listed in manifest but not found in archive", entry.Name)
		}
		h := sha256.Sum256(data)
		got := hex.EncodeToString(h[:])
		if got != entry.SHA256 {
			return fmt.Errorf("checksum mismatch for %q: want %s, got %s", entry.Name, entry.SHA256, got)
		}
		if int64(len(data)) != entry.Size {
			return fmt.Errorf("size mismatch for %q: want %d, got %d", entry.Name, entry.Size, len(data))
		}
	}
	return nil
}

// List returns the manifest from an archive.
func List(archivePath string) (*Manifest, error) {
	manifest, _, err := readArchive(archivePath)
	return manifest, err
}

// readArchive opens a (possibly gzipped) tar archive and returns the manifest and file contents.
func readArchive(archivePath string) (*Manifest, map[string][]byte, error) {
	f, err := os.Open(archivePath)
	if err != nil {
		return nil, nil, fmt.Errorf("open archive: %w", err)
	}
	defer f.Close()

	gr, err := gzip.NewReader(f)
	if err != nil {
		return nil, nil, fmt.Errorf("gzip reader: %w", err)
	}
	defer gr.Close()

	tr := tar.NewReader(gr)
	files := make(map[string][]byte)

	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, nil, fmt.Errorf("read tar: %w", err)
		}
		data, err := io.ReadAll(tr)
		if err != nil {
			return nil, nil, fmt.Errorf("read file %q: %w", hdr.Name, err)
		}
		files[hdr.Name] = data
	}

	manifestData, ok := files["backup-manifest.json"]
	if !ok {
		return nil, nil, fmt.Errorf("backup-manifest.json not found in archive")
	}

	var manifest Manifest
	if err := json.Unmarshal(manifestData, &manifest); err != nil {
		return nil, nil, fmt.Errorf("parse manifest: %w", err)
	}

	return &manifest, files, nil
}

func checksumFile(path string) (string, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()

	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

func addFileToTar(tw *tar.Writer, srcPath, name string) error {
	f, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return err
	}

	hdr := &tar.Header{
		Name:    name,
		Mode:    0644,
		Size:    info.Size(),
		ModTime: info.ModTime(),
	}
	if err := tw.WriteHeader(hdr); err != nil {
		return err
	}
	_, err = io.Copy(tw, f)
	return err
}

func addBytesToTar(tw *tar.Writer, data []byte, name string) error {
	hdr := &tar.Header{
		Name:    name,
		Mode:    0644,
		Size:    int64(len(data)),
		ModTime: time.Now(),
	}
	if err := tw.WriteHeader(hdr); err != nil {
		return err
	}
	_, err := tw.Write(data)
	return err
}
