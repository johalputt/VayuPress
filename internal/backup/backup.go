package backup

// Package backup produces and restores fully-encrypted VayuPress backups.
//
// Threat model: a copied backup file must be USELESS to anyone but its
// creator. The archive (tar+gzip of the data directory: SQLite DB, settings,
// media, VayuMail maildirs, PGP key store) is streamed through
// AES-256-GCM, keyed by Argon2id from the operator's passphrase. Without the
// passphrase there is no feasible way — with any modern tool — to read,
// alter, or even enumerate the contents; every chunk is independently
// authenticated, so tampering is detected before a single byte is written on
// restore.
//
// Format (versioned, self-describing):
//
//	magic "VPBK1\n" · salt[16] · frames…
//	frame = len(uint32 BE) · AES-256-GCM ciphertext (nonce = frame counter)
//
// The Argon2id parameters (t=3, m=64MiB, p=2) follow the project's password
// hashing posture; the 96-bit nonce is a strictly increasing counter, unique
// per key because the key is unique per backup (random salt).

import (
	"archive/tar"
	"compress/gzip"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/crypto/argon2"
)

const (
	magic     = "VPBK1\n"
	saltLen   = 16
	chunkSize = 1 << 20 // 1 MiB plaintext per sealed frame
)

var (
	// ErrBadPassphrase is returned when decryption fails — wrong passphrase or
	// a corrupted/tampered file (GCM cannot distinguish the two, by design).
	ErrBadPassphrase = errors.New("backup: wrong passphrase or corrupted file")
	// ErrNotBackup is returned when the input is not a VayuPress backup.
	ErrNotBackup = errors.New("backup: not a VayuPress encrypted backup (bad magic)")
)

// deriveKey stretches the passphrase into the AES-256 key.
func deriveKey(passphrase string, salt []byte) []byte {
	return argon2.IDKey([]byte(passphrase), salt, 3, 64*1024, 2, 32)
}

func frameNonce(counter uint64) []byte {
	n := make([]byte, 12)
	binary.BigEndian.PutUint64(n[4:], counter)
	return n
}

// encWriter seals fixed-size chunks into frames on the underlying writer.
type encWriter struct {
	w       io.Writer
	gcm     cipher.AEAD
	buf     []byte
	counter uint64
}

func (e *encWriter) flush() error {
	if len(e.buf) == 0 {
		return nil
	}
	ct := e.gcm.Seal(nil, frameNonce(e.counter), e.buf, nil)
	e.counter++
	var lenb [4]byte
	binary.BigEndian.PutUint32(lenb[:], uint32(len(ct)))
	if _, err := e.w.Write(lenb[:]); err != nil {
		return err
	}
	_, err := e.w.Write(ct)
	e.buf = e.buf[:0]
	return err
}

func (e *encWriter) Write(p []byte) (int, error) {
	total := len(p)
	for len(p) > 0 {
		room := chunkSize - len(e.buf)
		if room == 0 {
			if err := e.flush(); err != nil {
				return total - len(p), err
			}
			room = chunkSize
		}
		if room > len(p) {
			room = len(p)
		}
		e.buf = append(e.buf, p[:room]...)
		p = p[room:]
	}
	return total, nil
}

// Create writes an encrypted backup of srcDir to w.
func Create(w io.Writer, passphrase, srcDir string) error {
	if strings.TrimSpace(passphrase) == "" {
		return errors.New("backup: a passphrase is required — it is the only key to this backup")
	}
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return err
	}
	block, err := aes.NewCipher(deriveKey(passphrase, salt))
	if err != nil {
		return err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return err
	}
	if _, err := io.WriteString(w, magic); err != nil {
		return err
	}
	if _, err := w.Write(salt); err != nil {
		return err
	}

	enc := &encWriter{w: w, gcm: gcm, buf: make([]byte, 0, chunkSize)}
	gz := gzip.NewWriter(enc)
	tw := tar.NewWriter(gz)

	srcDir = filepath.Clean(srcDir)
	err = filepath.Walk(srcDir, func(path string, info os.FileInfo, werr error) error {
		if werr != nil {
			return werr
		}
		rel, rerr := filepath.Rel(srcDir, path)
		if rerr != nil || rel == "." {
			return nil
		}
		hdr, herr := tar.FileInfoHeader(info, "")
		if herr != nil {
			return herr
		}
		hdr.Name = filepath.ToSlash(rel)
		if info.IsDir() {
			hdr.Name += "/"
			return tw.WriteHeader(hdr)
		}
		if !info.Mode().IsRegular() {
			return nil // sockets, symlinks, devices: skipped
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		f, ferr := os.Open(path)
		if ferr != nil {
			return ferr
		}
		defer f.Close()
		_, cerr := io.Copy(tw, f)
		return cerr
	})
	if err != nil {
		return err
	}
	if err := tw.Close(); err != nil {
		return err
	}
	if err := gz.Close(); err != nil {
		return err
	}
	return enc.flush()
}

// decReader opens frames from r and yields the plaintext stream.
type decReader struct {
	r       io.Reader
	gcm     cipher.AEAD
	plain   []byte
	counter uint64
	eof     bool
}

func (d *decReader) Read(p []byte) (int, error) {
	for len(d.plain) == 0 {
		if d.eof {
			return 0, io.EOF
		}
		var lenb [4]byte
		if _, err := io.ReadFull(d.r, lenb[:]); err != nil {
			if errors.Is(err, io.EOF) {
				d.eof = true
				return 0, io.EOF
			}
			return 0, err
		}
		n := binary.BigEndian.Uint32(lenb[:])
		if n == 0 || n > chunkSize+uint32(d.gcm.Overhead()) {
			return 0, ErrBadPassphrase
		}
		ct := make([]byte, n)
		if _, err := io.ReadFull(d.r, ct); err != nil {
			return 0, ErrBadPassphrase
		}
		pt, err := d.gcm.Open(nil, frameNonce(d.counter), ct, nil)
		if err != nil {
			return 0, ErrBadPassphrase
		}
		d.counter++
		d.plain = pt
	}
	n := copy(p, d.plain)
	d.plain = d.plain[n:]
	return n, nil
}

// safeJoin resolves an archive entry name against destDir and returns the
// target path only if it stays strictly inside destDir. Checking the *joined*
// path (rather than the raw name) defeats "..", absolute paths and traversal
// alike — the canonical Zip-Slip defence. destDir must already be cleaned.
func safeJoin(destDir, name string) (string, bool) {
	target := filepath.Join(destDir, filepath.FromSlash(name))
	if target == destDir || strings.HasPrefix(target, destDir+string(os.PathSeparator)) {
		return target, true
	}
	return "", false
}

// Extract restores an encrypted backup from r into destDir (created if
// missing). Paths are sanitised so a crafted archive can never escape destDir.
func Extract(r io.Reader, passphrase, destDir string) error {
	head := make([]byte, len(magic))
	if _, err := io.ReadFull(r, head); err != nil || string(head) != magic {
		return ErrNotBackup
	}
	salt := make([]byte, saltLen)
	if _, err := io.ReadFull(r, salt); err != nil {
		return ErrNotBackup
	}
	block, err := aes.NewCipher(deriveKey(passphrase, salt))
	if err != nil {
		return err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return err
	}
	gz, err := gzip.NewReader(&decReader{r: r, gcm: gcm})
	if err != nil {
		return ErrBadPassphrase
	}
	tr := tar.NewReader(gz)
	destDir = filepath.Clean(destDir)
	if err := os.MkdirAll(destDir, 0o750); err != nil {
		return err
	}
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("backup: archive read: %w", err)
		}
		// Zip-Slip guard: resolve the entry against destDir and require the
		// result to stay strictly inside destDir.
		target, ok := safeJoin(destDir, hdr.Name)
		if !ok {
			return fmt.Errorf("backup: unsafe path %q in archive", hdr.Name)
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o750); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
				return err
			}
			f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(hdr.Mode)&0o777)
			if err != nil {
				return err
			}
			if _, err := io.Copy(f, tr); err != nil {
				f.Close()
				return err
			}
			if err := f.Close(); err != nil {
				return err
			}
		}
	}
}
