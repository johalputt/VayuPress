// SPDX-License-Identifier: Apache-2.0

package backup

// Package backup produces and restores fully-encrypted VayuPress backups, and
// provides the sealed-stream primitive VayuKeep replication reuses (ADR-0145).
//
// Threat model: a copied backup file must be USELESS to anyone but its creator,
// and a file that is not byte-for-byte the one we wrote must never restore
// *partially*. The archive (tar+gzip of the data directory: SQLite DB,
// settings, media, VayuMail maildirs, PGP key store) is streamed through
// AES-256-GCM under a random per-archive data key, which is itself wrapped by an
// Argon2id key derived from the operator's passphrase.
//
// Format v2 (VPBK2), all integers big-endian:
//
//	magic "VPBK2\n"
//	salt[16] · wrapNonce[12] · wrappedDEK[48] · genID[4]
//	frame…  = hdr(uint32) · AES-256-GCM ciphertext
//	          hdr bit31 = final flag, bits 0..30 = ciphertext length
//	          nonce = genID[4] ‖ seq[8]
//	          AAD   = headerHash[32] ‖ seq[8] ‖ prevTag[16] ‖ final[1]
//	last frame carries an empty plaintext and final = 1 (the terminator)
//
// Four properties follow from that AAD, and each closes a defect the v1 format
// had (ADR-0145):
//
//   - Truncation is detectable. The stream ends with an explicit terminator
//     frame whose "this is the end" claim is authenticated. Cutting the file at
//     any frame boundary produces ErrTruncated instead of a clean io.EOF. v1
//     caught this only as a side effect of gzip's CRC trailer — a layer with no
//     idea it was acting as a security control.
//   - Deletion and reordering break the chain. Each frame commits to the
//     previous frame's authentication tag, so removing frame k makes frame k+1
//     fail to open.
//   - The header is authenticated. headerHash covers the magic, salt, wrapped
//     key and generation id, so tampering with any of them fails every frame
//     rather than merely producing a wrong key.
//   - Passphrase rotation is O(1). The passphrase guards the wrapped DEK, not
//     the data, so re-keying an archive rewrites 48 bytes instead of terabytes.
//
// Argon2id (t=3, m=64MiB, p=2) runs ONCE per archive to unwrap the data key,
// never per frame — the cost is unchanged from v1 but is now amortised across a
// stream rather than being tied to a single file.
//
// v1 archives (VPBK1) stay readable so no existing backup is stranded.

import (
	"archive/tar"
	"compress/gzip"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/crypto/argon2"
)

const (
	magic        = "VPBK1\n" // legacy, read-only
	magicV2      = "VPBK2\n"
	saltLen      = 16
	dekLen       = 32
	wrapNonceLen = 12
	genIDLen     = 4
	tagLen       = 16
	chunkSize    = 1 << 20 // 1 MiB plaintext per sealed frame
	finalBit     = uint32(1) << 31
	maxFrameLen  = chunkSize + tagLen
)

var (
	// ErrBadPassphrase is returned when decryption fails — wrong passphrase or
	// a corrupted/tampered file (GCM cannot distinguish the two, by design).
	ErrBadPassphrase = errors.New("backup: wrong passphrase or corrupted file")
	// ErrNotBackup is returned when the input is not a VayuPress backup.
	ErrNotBackup = errors.New("backup: not a VayuPress encrypted backup (bad magic)")
	// ErrTruncated is returned when the stream ends without its authenticated
	// terminator frame. This is the signature of a cut-short upload, a full disk
	// during write, or a deliberate truncation — none of which the v1 format
	// could distinguish from a clean end of file.
	ErrTruncated = errors.New("backup: archive ends without its final marker — it is incomplete or was truncated")
)

// deriveKey stretches the passphrase into the AES-256 key-encryption key.
func deriveKey(passphrase string, salt []byte) []byte {
	return argon2.IDKey([]byte(passphrase), salt, 3, 64*1024, 2, 32)
}

// frameNonce builds the v1 positional nonce (counter only).
func frameNonce(counter uint64) []byte {
	n := make([]byte, 12)
	binary.BigEndian.PutUint64(n[4:], counter)
	return n
}

// seqNonce builds the v2 nonce: the archive's generation id followed by the
// frame sequence. The DEK is random per archive, so seq alone already makes the
// nonce unique; the genID prefix keeps that true when one key is reused across
// the generations of a VayuKeep replication stream.
func seqNonce(genID []byte, seq uint64) []byte {
	n := make([]byte, 12)
	copy(n[:genIDLen], genID)
	binary.BigEndian.PutUint64(n[genIDLen:], seq)
	return n
}

// frameAAD binds a frame to its archive, its position, its predecessor and its
// terminal status. Any edit to the header, any reorder, any deletion and any
// truncation changes what the reader computes here, so the frame fails to open.
func frameAAD(headerHash []byte, seq uint64, prevTag []byte, final bool) []byte {
	aad := make([]byte, 0, len(headerHash)+8+tagLen+1)
	aad = append(aad, headerHash...)
	var s [8]byte
	binary.BigEndian.PutUint64(s[:], seq)
	aad = append(aad, s[:]...)
	aad = append(aad, prevTag...)
	if final {
		return append(aad, 1)
	}
	return append(aad, 0)
}

// sealedWriter seals fixed-size chunks into chained frames.
type sealedWriter struct {
	w          io.Writer
	gcm        cipher.AEAD
	genID      []byte
	headerHash []byte
	buf        []byte
	seq        uint64
	prevTag    []byte
	closed     bool
}

func newSealedWriter(w io.Writer, gcm cipher.AEAD, genID, headerHash []byte) *sealedWriter {
	return &sealedWriter{
		w: w, gcm: gcm, genID: genID, headerHash: headerHash,
		buf:     make([]byte, 0, chunkSize),
		prevTag: make([]byte, tagLen), // zeros for the first frame
	}
}

// emit seals one frame. A final frame is the authenticated end-of-stream marker
// and carries no payload.
func (e *sealedWriter) emit(final bool) error {
	ct := e.gcm.Seal(nil, seqNonce(e.genID, e.seq), e.buf, frameAAD(e.headerHash, e.seq, e.prevTag, final))
	if len(ct) > maxFrameLen {
		return errors.New("backup: internal frame overflow")
	}
	hdr := uint32(len(ct))
	if final {
		hdr |= finalBit
	}
	var hb [4]byte
	binary.BigEndian.PutUint32(hb[:], hdr)
	if _, err := e.w.Write(hb[:]); err != nil {
		return err
	}
	if _, err := e.w.Write(ct); err != nil {
		return err
	}
	e.prevTag = ct[len(ct)-tagLen:]
	e.seq++
	e.buf = e.buf[:0]
	return nil
}

func (e *sealedWriter) flush() error {
	if len(e.buf) == 0 {
		return nil
	}
	return e.emit(false)
}

// Close flushes any buffered plaintext and writes the terminator frame. A
// stream that was never closed cannot be read back — which is the point.
func (e *sealedWriter) Close() error {
	if e.closed {
		return nil
	}
	if err := e.flush(); err != nil {
		return err
	}
	e.closed = true
	return e.emit(true)
}

func (e *sealedWriter) Write(p []byte) (int, error) {
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

// buildHeader mints a fresh archive header: random salt, random data key wrapped
// under the passphrase-derived key, and a random generation id. It returns the
// serialised header, the AEAD keyed by the data key, the generation id and the
// header hash that every frame will authenticate.
func buildHeader(passphrase string) (hdr []byte, gcm cipher.AEAD, genID, headerHash []byte, err error) {
	salt := make([]byte, saltLen)
	if _, err = rand.Read(salt); err != nil {
		return nil, nil, nil, nil, err
	}
	dek := make([]byte, dekLen)
	if _, err = rand.Read(dek); err != nil {
		return nil, nil, nil, nil, err
	}
	wrapNonce := make([]byte, wrapNonceLen)
	if _, err = rand.Read(wrapNonce); err != nil {
		return nil, nil, nil, nil, err
	}
	genID = make([]byte, genIDLen)
	if _, err = rand.Read(genID); err != nil {
		return nil, nil, nil, nil, err
	}
	kekBlock, err := aes.NewCipher(deriveKey(passphrase, salt))
	if err != nil {
		return nil, nil, nil, nil, err
	}
	kek, err := cipher.NewGCM(kekBlock)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	wrapped := kek.Seal(nil, wrapNonce, dek, nil)

	dekBlock, err := aes.NewCipher(dek)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	if gcm, err = cipher.NewGCM(dekBlock); err != nil {
		return nil, nil, nil, nil, err
	}

	hdr = make([]byte, 0, len(magicV2)+saltLen+wrapNonceLen+len(wrapped)+genIDLen)
	hdr = append(hdr, magicV2...)
	hdr = append(hdr, salt...)
	hdr = append(hdr, wrapNonce...)
	hdr = append(hdr, wrapped...)
	hdr = append(hdr, genID...)
	sum := sha256.Sum256(hdr)
	return hdr, gcm, genID, sum[:], nil
}

// openHeader parses and authenticates a v2 header, returning the data-key AEAD.
func openHeader(r io.Reader, passphrase string) (gcm cipher.AEAD, genID, headerHash []byte, err error) {
	wrappedLen := dekLen + tagLen
	rest := make([]byte, saltLen+wrapNonceLen+wrappedLen+genIDLen)
	if _, err = io.ReadFull(r, rest); err != nil {
		return nil, nil, nil, ErrNotBackup
	}
	salt := rest[:saltLen]
	wrapNonce := rest[saltLen : saltLen+wrapNonceLen]
	wrapped := rest[saltLen+wrapNonceLen : saltLen+wrapNonceLen+wrappedLen]
	genID = rest[saltLen+wrapNonceLen+wrappedLen:]

	kekBlock, err := aes.NewCipher(deriveKey(passphrase, salt))
	if err != nil {
		return nil, nil, nil, err
	}
	kek, err := cipher.NewGCM(kekBlock)
	if err != nil {
		return nil, nil, nil, err
	}
	dek, err := kek.Open(nil, wrapNonce, wrapped, nil)
	if err != nil {
		return nil, nil, nil, ErrBadPassphrase
	}
	dekBlock, err := aes.NewCipher(dek)
	if err != nil {
		return nil, nil, nil, err
	}
	if gcm, err = cipher.NewGCM(dekBlock); err != nil {
		return nil, nil, nil, err
	}
	full := append([]byte(magicV2), rest...)
	sum := sha256.Sum256(full)
	return gcm, genID, sum[:], nil
}

// sealedReader opens chained frames and yields the plaintext stream. It returns
// ErrTruncated rather than io.EOF when the terminator frame is missing.
type sealedReader struct {
	r          io.Reader
	gcm        cipher.AEAD
	genID      []byte
	headerHash []byte
	plain      []byte
	seq        uint64
	prevTag    []byte
	sawFinal   bool
}

func (d *sealedReader) Read(p []byte) (int, error) {
	for len(d.plain) == 0 {
		if d.sawFinal {
			return 0, io.EOF
		}
		var hb [4]byte
		if _, err := io.ReadFull(d.r, hb[:]); err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				// The stream stopped without ever claiming to be finished.
				return 0, ErrTruncated
			}
			return 0, err
		}
		hdr := binary.BigEndian.Uint32(hb[:])
		final := hdr&finalBit != 0
		n := hdr &^ finalBit
		if n < tagLen || n > maxFrameLen {
			return 0, ErrBadPassphrase
		}
		ct := make([]byte, n)
		if _, err := io.ReadFull(d.r, ct); err != nil {
			return 0, ErrTruncated
		}
		pt, err := d.gcm.Open(nil, seqNonce(d.genID, d.seq),
			ct, frameAAD(d.headerHash, d.seq, d.prevTag, final))
		if err != nil {
			return 0, ErrBadPassphrase
		}
		d.prevTag = ct[len(ct)-tagLen:]
		d.seq++
		if final {
			d.sawFinal = true
			if len(pt) != 0 {
				return 0, ErrBadPassphrase
			}
			return 0, io.EOF
		}
		d.plain = pt
	}
	n := copy(p, d.plain)
	d.plain = d.plain[n:]
	return n, nil
}

// finish drains any frames the tar/gzip layers did not bother to read and
// reports whether the stream reached its authenticated terminator.
//
// This call is not optional. gzip and tar stop reading at their own logical end
// of data, which is BEFORE the sealed terminator frame — so an attacker who
// strips only the terminator produces an archive that unpacks perfectly and is
// nonetheless incomplete. Detection has to be demanded explicitly.
func (d *sealedReader) finish() error {
	if !d.sawFinal {
		if _, err := io.Copy(io.Discard, d); err != nil {
			return err
		}
	}
	if !d.sawFinal {
		return ErrTruncated
	}
	return nil
}

// archiveReader is a decrypted archive stream that can be asked whether it
// actually ended, rather than merely stopped.
type archiveReader interface {
	io.Reader
	finish() error
}

// legacyReader opens v1 frames (positional nonce, no AAD, no terminator).
type legacyReader struct {
	r       io.Reader
	gcm     cipher.AEAD
	plain   []byte
	counter uint64
	eof     bool
}

func (d *legacyReader) Read(p []byte) (int, error) {
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

// finish is a no-op for v1: the format carries no terminator, so a v1 archive
// cannot distinguish a clean end from a truncation. That weaker guarantee is
// exactly why VPBK2 exists; v1 stays readable only so existing backups are not
// stranded.
func (d *legacyReader) finish() error { return nil }

// Options tunes what Create puts into the archive. The zero value backs up the
// source directory verbatim, which is what a cold copy wants.
type Options struct {
	// Substitute maps a path relative to srcDir onto a file elsewhere on disk
	// that should be archived under that name instead. It exists so a live
	// SQLite database is archived from a consistent `VACUUM INTO` snapshot while
	// still appearing at its normal path inside the archive.
	Substitute map[string]string
	// Skip lists paths relative to srcDir to leave out entirely — the `-wal` and
	// `-shm` sidecars of a substituted database, whose bytes belong to a
	// checkpoint state the snapshot has already folded in.
	Skip map[string]bool
}

// CreateWithOptions writes an encrypted backup of srcDir to w. Pass the zero
// Options to archive the directory verbatim.
func CreateWithOptions(w io.Writer, passphrase, srcDir string, opts Options) error {
	if strings.TrimSpace(passphrase) == "" {
		return errors.New("backup: a passphrase is required — it is the only key to this backup")
	}
	hdr, gcm, genID, headerHash, err := buildHeader(passphrase)
	if err != nil {
		return err
	}
	if _, err := w.Write(hdr); err != nil {
		return err
	}

	enc := newSealedWriter(w, gcm, genID, headerHash)
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
		slashRel := filepath.ToSlash(rel)
		if opts.Skip[slashRel] {
			return nil
		}
		hdr, herr := tar.FileInfoHeader(info, "")
		if herr != nil {
			return herr
		}
		hdr.Name = slashRel
		if info.IsDir() {
			hdr.Name += "/"
			return tw.WriteHeader(hdr)
		}
		if !info.Mode().IsRegular() {
			return nil // sockets, symlinks, devices: skipped
		}
		src := path
		if sub, ok := opts.Substitute[slashRel]; ok {
			si, serr := os.Stat(sub)
			if serr != nil {
				return serr
			}
			src = sub
			hdr.Size = si.Size()
			hdr.ModTime = si.ModTime()
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		f, ferr := os.Open(src) //nolint:gosec // operator-selected data directory
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
	// The terminator frame is what makes a complete archive distinguishable from
	// a truncated one. Everything before this point could also be produced by a
	// writer that died halfway.
	return enc.Close()
}

// openArchive reads the magic and returns a plaintext reader for either format.
func openArchive(r io.Reader, passphrase string) (archiveReader, error) {
	head := make([]byte, len(magicV2))
	if _, err := io.ReadFull(r, head); err != nil {
		return nil, ErrNotBackup
	}
	switch string(head) {
	case magicV2:
		gcm, genID, headerHash, err := openHeader(r, passphrase)
		if err != nil {
			return nil, err
		}
		return &sealedReader{r: r, gcm: gcm, genID: genID, headerHash: headerHash,
			prevTag: make([]byte, tagLen)}, nil
	case magic:
		salt := make([]byte, saltLen)
		if _, err := io.ReadFull(r, salt); err != nil {
			return nil, ErrNotBackup
		}
		block, err := aes.NewCipher(deriveKey(passphrase, salt))
		if err != nil {
			return nil, err
		}
		gcm, err := cipher.NewGCM(block)
		if err != nil {
			return nil, err
		}
		return &legacyReader{r: r, gcm: gcm}, nil
	}
	return nil, ErrNotBackup
}

// ExtractStaged restores an encrypted backup from r into destDir, returning the
// path the previous destDir was moved to (empty when there was nothing to
// displace).
//
// The restore is staged: every entry is written into a sibling directory and
// the archive must reach its authenticated terminator frame before anything is
// moved into place. A tampered, truncated or wrong-passphrase archive therefore
// leaves destDir untouched — which is what this package has always promised and
// what streaming entries straight to their final path could not deliver.
//
// When destDir already exists it is moved aside rather than deleted. A restore
// is rare, deliberate and high-stakes; silently destroying the previous data
// directory because the new one *looked* fine is not a trade this package
// makes.
func ExtractStaged(r io.Reader, passphrase, destDir string) (displaced string, err error) {
	pr, err := openArchive(r, passphrase)
	if err != nil {
		return "", err
	}
	destDir = filepath.Clean(destDir)
	parent := filepath.Dir(destDir)
	if err := os.MkdirAll(parent, 0o750); err != nil {
		return "", err
	}
	// The staging directory is a SIBLING so the final rename stays on one
	// filesystem — a cross-device rename is not atomic and would defeat the
	// whole point of staging.
	staging, err := os.MkdirTemp(parent, "."+filepath.Base(destDir)+".restore-")
	if err != nil {
		return "", err
	}
	ok := false
	defer func() {
		if !ok {
			_ = os.RemoveAll(staging)
		}
	}()

	if err := extractInto(pr, staging); err != nil {
		return "", err
	}

	if _, statErr := os.Stat(destDir); statErr == nil {
		displaced = destDir + ".replaced-" + time.Now().UTC().Format("20060102-150405")
		if err := os.Rename(destDir, displaced); err != nil {
			return "", fmt.Errorf("backup: could not move the existing data directory aside: %w", err)
		}
	}
	if err := os.Rename(staging, destDir); err != nil {
		return "", fmt.Errorf("backup: restore staged successfully but could not be moved into place: %w", err)
	}
	ok = true
	return displaced, nil
}

// extractInto unpacks a verified plaintext stream into dir.
func extractInto(pr archiveReader, dir string) error {
	gz, err := gzip.NewReader(pr)
	if err != nil {
		if errors.Is(err, ErrTruncated) || errors.Is(err, ErrBadPassphrase) {
			return err
		}
		return ErrBadPassphrase
	}
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			// gzip verified its CRC; now demand the sealed terminator too, which
			// tar never reads far enough to reach.
			return pr.finish()
		}
		if err != nil {
			if errors.Is(err, ErrTruncated) || errors.Is(err, ErrBadPassphrase) {
				return err
			}
			return fmt.Errorf("backup: archive read: %w", err)
		}
		// Skip degenerate root entries so the containment guard below can be a
		// single, unconditional check on the resolved path.
		name := filepath.Clean(filepath.FromSlash(hdr.Name))
		if name == "." || name == string(os.PathSeparator) {
			continue
		}
		// Zip-Slip guard (inline, next to the file ops it protects, in the exact
		// canonical form): the resolved path MUST live under dir. Validating the
		// *joined* result — not the raw name — defeats "..", absolute paths and
		// crafted names alike.
		destPrefix := dir + string(os.PathSeparator)
		target := filepath.Join(dir, name)
		if !strings.HasPrefix(target, destPrefix) {
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
			f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(hdr.Mode)&0o777) //nolint:gosec // path is containment-checked above
			if err != nil {
				return err
			}
			if _, err := io.Copy(f, tr); err != nil { //nolint:gosec // size bounded by the sealed frame cap
				f.Close()
				return err
			}
			if err := f.Close(); err != nil {
				return err
			}
		}
	}
}

// Verify reads an archive end to end without writing anything, confirming the
// passphrase, every frame's authentication tag, the chain between them and the
// terminator. It is what the VayuKeep restore drill calls, and what makes
// "verified restorable" a measurement rather than a claim.
func Verify(r io.Reader, passphrase string) error {
	pr, err := openArchive(r, passphrase)
	if err != nil {
		return err
	}
	gz, err := gzip.NewReader(pr)
	if err != nil {
		if errors.Is(err, ErrTruncated) || errors.Is(err, ErrBadPassphrase) {
			return err
		}
		return ErrBadPassphrase
	}
	tr := tar.NewReader(gz)
	for {
		_, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return pr.finish()
		}
		if err != nil {
			return err
		}
		if _, err := io.Copy(io.Discard, tr); err != nil {
			return err
		}
	}
}
