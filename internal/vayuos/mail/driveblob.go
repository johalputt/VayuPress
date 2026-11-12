// SPDX-License-Identifier: Apache-2.0

package mail

// driveblob.go — how a Drive file's bytes are kept on the disk.
//
// Each file has its own random 256-bit key, and its bytes are kept as
// chunks of driveChunk sealed with AES-256-GCM under it: a header (magic,
// chunk size, a random nonce prefix) and then the chunks, each with its
// tag. A chunk's nonce is the prefix and its number, and its additional
// data whether it is the last, so a chunk moved, swapped, dropped or cut
// off is refused, not read; the header is checked as it is read. Chunks make a
// read at any point cheap (a range request, a download resumed) and keep
// a file of any size out of memory, sealed or not.
//
// What this protects: a copy of the disk, or of a backup of it, without
// the install's secrets store, reads as noise. The file's key is kept in
// its row, wrapped (drivekeys.go) by the install's Drive key, which the
// secrets store keeps under its own key outside the database, or, in a
// private mailbox, sealed to the holder's OpenPGP key, which only the
// holder's passphrase opens. What it does not protect: a running server
// whose secrets store is read, which opens every file that is not
// private, as it must to serve them.

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"io"
	"os"
)

const (
	blobMagic   = "VDB1"
	driveChunk  = 64 << 10
	blobHeader  = 4 + 4 + 8 // magic, chunk size, nonce prefix
	blobTagSize = 16
)

// errBlobDamaged is a stored file whose bytes are not what was kept: a
// chunk that does not open, or a file cut short.
var errBlobDamaged = errors.New("vayumail: this file's stored bytes are damaged")

func blobAEAD(key []byte) (cipher.AEAD, error) {
	if len(key) != 32 {
		return nil, errors.New("vayumail: a file key is 32 bytes")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// blobSize is the bytes a file of size bytes takes on the disk.
func blobSize(size int64) int64 {
	return blobHeader + blobChunks(size)*blobTagSize + size
}

// blobChunks is how many chunks a file of size bytes is: one at least, the
// last perhaps empty, so even an empty file has a chunk that says it ends.
func blobChunks(size int64) int64 {
	if size == 0 {
		return 1
	}
	return (size + driveChunk - 1) / driveChunk
}

// chunkAD is a chunk's additional data: whether it is the last. Its
// number is in its nonce, so a chunk read in another's place does not
// open; the mark is what says a file ends where it ends.
func chunkAD(last bool) []byte {
	if last {
		return []byte{1}
	}
	return []byte{0}
}

// chunkNonce is the file's nonce prefix and the chunk's number. Each file
// has its own key, so a nonce is never used twice under one key.
func chunkNonce(prefix []byte, n uint64) []byte {
	nonce := make([]byte, 12)
	copy(nonce, prefix[:8])
	binary.BigEndian.PutUint32(nonce[8:], uint32(n))
	return nonce
}

// blobWriter seals what is written to it into w, chunk by chunk. Close
// writes the last chunk; nothing is a whole file until it has.
type blobWriter struct {
	w      io.Writer
	aead   cipher.AEAD
	header []byte
	n      uint64
	buf    []byte
}

func newBlobWriter(w io.Writer, key []byte) (*blobWriter, error) {
	aead, err := blobAEAD(key)
	if err != nil {
		return nil, err
	}
	header := make([]byte, blobHeader)
	copy(header, blobMagic)
	binary.BigEndian.PutUint32(header[4:], driveChunk)
	if _, err := rand.Read(header[8:]); err != nil {
		return nil, err
	}
	if _, err := w.Write(header); err != nil {
		return nil, err
	}
	return &blobWriter{w: w, aead: aead, header: header, buf: make([]byte, 0, driveChunk)}, nil
}

func (b *blobWriter) seal(last bool) error {
	out := b.aead.Seal(nil, chunkNonce(b.header[8:], b.n), b.buf, chunkAD(last))
	b.n++
	b.buf = b.buf[:0]
	_, err := b.w.Write(out)
	return err
}

func (b *blobWriter) Write(p []byte) (int, error) {
	written := 0
	for len(p) > 0 {
		take := min(driveChunk-len(b.buf), len(p))
		b.buf = append(b.buf, p[:take]...)
		p = p[take:]
		written += take
		// A full chunk is sealed only once more follows it: the chunk that
		// turns out to be the last must carry the mark that says so.
		if len(b.buf) == driveChunk && len(p) > 0 {
			if err := b.seal(false); err != nil {
				return written, err
			}
		}
	}
	return written, nil
}

// Flush seals a full chunk held back: the next Write or Close decides
// whether the chunk after it is the last. It lets a writer that will be
// continued later (a resumed upload) put down every chunk it can.
func (b *blobWriter) Flush() error {
	if len(b.buf) == driveChunk {
		return b.seal(false)
	}
	return nil
}

func (b *blobWriter) Close() error { return b.seal(true) }

// blobReader reads a stored file of size bytes, opening each chunk as it
// is reached; it seeks without reading what it passes.
type blobReader struct {
	r      io.ReaderAt
	closer io.Closer
	aead   cipher.AEAD
	header []byte
	size   int64
	pos    int64
	chunk  int64 // the chunk in plain, -1 for none
	plain  []byte
	// onDamage is told, once, that a chunk did not open.
	onDamage func()
}

func newBlobReader(r io.ReaderAt, closer io.Closer, key []byte, size int64) (*blobReader, error) {
	aead, err := blobAEAD(key)
	if err != nil {
		return nil, err
	}
	header := make([]byte, blobHeader)
	if _, err := r.ReadAt(header, 0); err != nil || string(header[:4]) != blobMagic || binary.BigEndian.Uint32(header[4:]) != driveChunk {
		return nil, errBlobDamaged
	}
	return &blobReader{r: r, closer: closer, aead: aead, header: header, size: size, chunk: -1}, nil
}

func (b *blobReader) open(n int64) error {
	if b.chunk == n {
		return nil
	}
	last := n == blobChunks(b.size)-1
	plainLen := int64(driveChunk)
	if last {
		plainLen = b.size - n*driveChunk
	}
	sealed := make([]byte, plainLen+blobTagSize)
	off := blobHeader + n*(driveChunk+blobTagSize)
	if _, err := b.r.ReadAt(sealed, off); err != nil {
		if b.onDamage != nil {
			b.onDamage()
			b.onDamage = nil
		}
		return errBlobDamaged
	}
	plain, err := b.aead.Open(b.plain[:0], chunkNonce(b.header[8:], uint64(n)), sealed, chunkAD(last))
	if err != nil {
		if b.onDamage != nil {
			b.onDamage()
			b.onDamage = nil
		}
		return errBlobDamaged
	}
	b.plain, b.chunk = plain, n
	return nil
}

func (b *blobReader) Read(p []byte) (int, error) {
	if b.pos >= b.size {
		// The last chunk is opened even when it is empty or already read:
		// its mark is what says the file was not cut short.
		if err := b.open(blobChunks(b.size) - 1); err != nil {
			return 0, err
		}
		return 0, io.EOF
	}
	n := b.pos / driveChunk
	if err := b.open(n); err != nil {
		return 0, err
	}
	k := copy(p, b.plain[b.pos-n*driveChunk:])
	b.pos += int64(k)
	return k, nil
}

func (b *blobReader) Seek(offset int64, whence int) (int64, error) {
	switch whence {
	case io.SeekStart:
	case io.SeekCurrent:
		offset += b.pos
	case io.SeekEnd:
		offset += b.size
	default:
		return 0, errors.New("vayumail: no such seek")
	}
	if offset < 0 {
		return 0, errors.New("vayumail: a seek before the start")
	}
	b.pos = offset
	return offset, nil
}

func (b *blobReader) Close() error {
	if b.closer == nil {
		return nil
	}
	return b.closer.Close()
}

// openBlob opens a stored file kept in chunks under the key its row keeps;
// onDamage is told if a chunk does not open.
func (e *Engine) openBlob(mbox, blob, fkey string, size int64, onDamage func()) (io.ReadSeekCloser, error) {
	key, err := e.openFileKey(mbox, fkey)
	if err != nil {
		return nil, err
	}
	path, err := e.blobPath(mbox, blob)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(path) // #nosec G304 -- blobPath admits only this package's hex names under the mailbox's directory
	if err != nil {
		return nil, ErrDriveNotFound
	}
	r, err := newBlobReader(f, f, key, size)
	if err != nil {
		_ = f.Close()
		if onDamage != nil {
			onDamage()
		}
		return nil, err
	}
	r.onDamage = onDamage
	return r, nil
}

// markDamaged marks the file or version whose bytes are blob as damaged,
// so its sheet says so and the next check does not take it for sound.
func (e *Engine) markDamaged(mbox, blob string) {
	ctx := context.Background()
	for _, table := range []string{"vayumail_drive", "vayumail_drive_versions"} {
		_, _ = e.accounts.db.ExecContext(ctx, `UPDATE `+table+` SET damaged=1 WHERE mailbox=? AND blob=?`, mbox, blob)
	}
}
