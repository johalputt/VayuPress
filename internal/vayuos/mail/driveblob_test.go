// SPDX-License-Identifier: Apache-2.0

package mail

import (
	"bytes"
	"crypto/rand"
	"errors"
	"io"
	"testing"
)

func sealBlobBytes(t *testing.T, key, plain []byte, writes int) []byte {
	t.Helper()
	var out bytes.Buffer
	w, err := newBlobWriter(&out, key)
	if err != nil {
		t.Fatal(err)
	}
	// Written in pieces of every size, as a stream arrives.
	step := len(plain)/writes + 1
	for i := 0; i < len(plain); i += step {
		if _, err := w.Write(plain[i:min(i+step, len(plain))]); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func openBlobBytes(stored, key []byte, size int64) ([]byte, error) {
	r, err := newBlobReader(bytes.NewReader(stored), nil, key, size)
	if err != nil {
		return nil, err
	}
	return io.ReadAll(r)
}

func randomBytes(t *testing.T, n int) []byte {
	t.Helper()
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return b
}

// TestAFileIsKeptInSealedChunksAndReadBack — every size around a chunk's
// edge comes back as it went in, takes the room blobSize says, and is not
// itself on the disk.
func TestAFileIsKeptInSealedChunksAndReadBack(t *testing.T) {
	key := randomBytes(t, 32)
	for _, n := range []int{0, 1, driveChunk - 1, driveChunk, driveChunk + 1, 3 * driveChunk, 3*driveChunk + 7} {
		plain := randomBytes(t, n)
		stored := sealBlobBytes(t, key, plain, 5)
		if int64(len(stored)) != blobSize(int64(n)) {
			t.Errorf("%d bytes take %d on the disk, blobSize says %d", n, len(stored), blobSize(int64(n)))
		}
		if n >= 16 && bytes.Contains(stored, plain[:16]) {
			t.Errorf("%d bytes are on the disk as they are", n)
		}
		got, err := openBlobBytes(stored, key, int64(n))
		if err != nil || !bytes.Equal(got, plain) {
			t.Errorf("%d bytes came back as %d (%v)", n, len(got), err)
		}
	}
}

// TestAStoredFileIsReadFromAnyPoint — a seek into the third chunk reads
// from there without the chunks before.
func TestAStoredFileIsReadFromAnyPoint(t *testing.T) {
	key := randomBytes(t, 32)
	plain := randomBytes(t, 3*driveChunk+100)
	stored := sealBlobBytes(t, key, plain, 3)
	// The first chunk is spoiled: a read that starts past it never opens it.
	stored[blobHeader+5] ^= 1
	r, err := newBlobReader(bytes.NewReader(stored), nil, key, int64(len(plain)))
	if err != nil {
		t.Fatal(err)
	}
	at := int64(2*driveChunk + 50)
	if _, err := r.Seek(at, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(r)
	if err != nil || !bytes.Equal(got, plain[at:]) {
		t.Fatalf("from %d: %d bytes (%v)", at, len(got), err)
	}
	if _, err := r.Seek(-10, io.SeekEnd); err != nil {
		t.Fatal(err)
	}
	if got, _ := io.ReadAll(r); !bytes.Equal(got, plain[len(plain)-10:]) {
		t.Error("the last ten bytes")
	}
}

// TestAStoredFileThatWasChangedIsRefused — a byte changed, two chunks
// swapped, the last chunk dropped (even where the file ends on a chunk's
// edge), another file's key: none of them reads.
func TestAStoredFileThatWasChangedIsRefused(t *testing.T) {
	key := randomBytes(t, 32)
	plain := randomBytes(t, 2*driveChunk)
	stored := sealBlobBytes(t, key, plain, 2)
	sealedChunk := driveChunk + blobTagSize
	cases := map[string][]byte{}
	flip := append([]byte(nil), stored...)
	flip[blobHeader+sealedChunk+9] ^= 1
	cases["a byte changed"] = flip
	swap := append([]byte(nil), stored[:blobHeader]...)
	swap = append(swap, stored[blobHeader+sealedChunk:]...)
	swap = append(swap, stored[blobHeader:blobHeader+sealedChunk]...)
	cases["two chunks swapped"] = swap
	cases["the last chunk dropped"] = stored[:blobHeader+sealedChunk]
	header := append([]byte(nil), stored...)
	header[4] ^= 1
	cases["the header changed"] = header
	magic := append([]byte(nil), stored...)
	magic[0] ^= 1
	cases["the magic changed"] = magic
	// Two chunks that are neither the last, swapped: only their numbers
	// tell them apart.
	three := randomBytes(t, 3*driveChunk)
	three3 := sealBlobBytes(t, key, three, 3)
	mid := append([]byte(nil), three3[:blobHeader]...)
	mid = append(mid, three3[blobHeader+sealedChunk:blobHeader+2*sealedChunk]...)
	mid = append(mid, three3[blobHeader:blobHeader+sealedChunk]...)
	mid = append(mid, three3[blobHeader+2*sealedChunk:]...)
	if _, err := openBlobBytes(mid, key, int64(len(three))); !errors.Is(err, errBlobDamaged) {
		t.Errorf("two middle chunks swapped: %v", err)
	}
	for name, bad := range cases {
		if _, err := openBlobBytes(bad, key, int64(len(plain))); !errors.Is(err, errBlobDamaged) {
			t.Errorf("%s: %v", name, err)
		}
	}
	// A file cut at a chunk's edge and said to be the shorter size: the
	// chunk it ends on does not carry the last mark.
	if _, err := openBlobBytes(stored[:blobHeader+sealedChunk], key, driveChunk); !errors.Is(err, errBlobDamaged) {
		t.Errorf("a file cut at a chunk's edge: %v", err)
	}
	// An empty file whose one chunk, the one that says it ends, is gone.
	empty := sealBlobBytes(t, key, nil, 1)
	if _, err := openBlobBytes(empty[:blobHeader], key, 0); !errors.Is(err, errBlobDamaged) {
		t.Errorf("an empty file with its chunk dropped: %v", err)
	}
	if _, err := openBlobBytes(stored, randomBytes(t, 32), int64(len(plain))); !errors.Is(err, errBlobDamaged) {
		t.Errorf("another key: %v", err)
	}
}
