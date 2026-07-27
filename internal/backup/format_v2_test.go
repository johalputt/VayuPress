// SPDX-License-Identifier: Apache-2.0

package backup

// format_v2_test.go — the adversarial suite for the VPBK2 sealed stream.
//
// Every test here corresponds to a way an attacker (or a half-finished upload,
// or a full disk) can hand us bytes that are not the ones we wrote. The v1
// format survived some of these by accident and failed others silently; the
// point of the chained AAD and the authenticated terminator is that each one
// now fails loudly, and — critically — fails BEFORE anything is written to the
// operator's data directory.

import (
	"bytes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

// cipherAEAD aliases the AEAD type so the chain helpers read cleanly.
type cipherAEAD = cipher.AEAD

// v2HeaderLen is the fixed size of the VPBK2 header.
const v2HeaderLen = len(magicV2) + saltLen + wrapNonceLen + dekLen + tagLen + genIDLen

// splitV2 breaks an archive into its header and its wire frames (each frame
// including its 4-byte length/flag word), so tests can delete, reorder and
// splice at exactly the granularity an attacker would.
func splitV2(t *testing.T, archive []byte) (header []byte, frames [][]byte) {
	t.Helper()
	if len(archive) < v2HeaderLen || string(archive[:len(magicV2)]) != magicV2 {
		t.Fatalf("not a VPBK2 archive")
	}
	header = archive[:v2HeaderLen]
	for i := v2HeaderLen; i < len(archive); {
		if i+4 > len(archive) {
			t.Fatalf("ragged frame header at %d", i)
		}
		n := int(binary.BigEndian.Uint32(archive[i:i+4]) &^ finalBit)
		end := i + 4 + n
		if end > len(archive) {
			t.Fatalf("frame at %d runs past end", i)
		}
		frames = append(frames, archive[i:end])
		i = end
	}
	return header, frames
}

func joinV2(header []byte, frames [][]byte) []byte {
	out := append([]byte{}, header...)
	for _, f := range frames {
		out = append(out, f...)
	}
	return out
}

// bigTree builds a source tree large enough to span several 1 MiB sealed frames,
// so truncation and frame surgery have somewhere to bite.
func bigTree(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	// Genuinely random payload. An arithmetic pattern looks incompressible but
	// is not — gzip collapsed 6 MiB of it into a single frame, leaving the
	// multi-frame attacks below with nothing to bite on.
	for i := 0; i < 6; i++ {
		blob := make([]byte, 1<<20)
		if _, err := rand.Read(blob); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "blob"+string(rune('a'+i))+".bin"), blob, 0o640); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "canary.txt"), []byte("RESTORED"), 0o640); err != nil {
		t.Fatal(err)
	}
	return dir
}

func archiveOf(t *testing.T, dir, pw string) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := Create(&buf, pw, dir); err != nil {
		t.Fatalf("create: %v", err)
	}
	return buf.Bytes()
}

// TestTruncationIsDetected is the defect this format exists to close. Cutting an
// archive at any frame boundary used to yield a clean io.EOF from the sealed
// layer; it was only caught downstream by gzip's CRC trailer, a control that
// does not know it is a control. The terminator frame makes "this is the end" an
// authenticated claim, so its absence is an error.
func TestTruncationIsDetected(t *testing.T) {
	const pw = "pw"
	full := archiveOf(t, bigTree(t), pw)
	header, frames := splitV2(t, full)
	if len(frames) < 3 {
		t.Fatalf("need a multi-frame archive to test truncation, got %d frames", len(frames))
	}
	for cut := 1; cut < len(frames); cut++ {
		short := joinV2(header, frames[:cut])
		err := Verify(bytes.NewReader(short), pw)
		if err == nil {
			t.Errorf("archive truncated to %d/%d frames verified as complete", cut, len(frames))
			continue
		}
		if !errors.Is(err, ErrTruncated) {
			t.Errorf("truncation at %d/%d reported as %v, want ErrTruncated", cut, len(frames), err)
		}
	}
}

// TestTruncatedArchiveLeavesDestinationUntouched is the operational half of the
// same defect. The package doc has always promised detection "before a single
// byte is written on restore"; streaming tar entries straight to their final
// path could not deliver that. Staging can.
func TestTruncatedArchiveLeavesDestinationUntouched(t *testing.T) {
	const pw = "pw"
	full := archiveOf(t, bigTree(t), pw)
	header, frames := splitV2(t, full)
	short := joinV2(header, frames[:len(frames)-1]) // drop only the terminator

	dest := t.TempDir()
	live := filepath.Join(dest, "do-not-touch.txt")
	if err := os.WriteFile(live, []byte("LIVE DATA"), 0o640); err != nil {
		t.Fatal(err)
	}

	if err := Extract(bytes.NewReader(short), pw, dest); err == nil {
		t.Fatal("truncated archive restored without error")
	}
	got, err := os.ReadFile(live)
	if err != nil || string(got) != "LIVE DATA" {
		t.Fatalf("a failed restore damaged the live data directory: %q err=%v", got, err)
	}
	// And nothing from the archive may have landed either.
	if _, err := os.Stat(filepath.Join(dest, "canary.txt")); err == nil {
		t.Error("a failed restore left archive contents in the destination")
	}
}

// TestFrameDeletionIsDetected covers removing a frame from the middle. Each
// frame commits to its predecessor's tag, so the survivor after the hole cannot
// open.
func TestFrameDeletionIsDetected(t *testing.T) {
	const pw = "pw"
	header, frames := splitV2(t, archiveOf(t, bigTree(t), pw))
	if len(frames) < 4 {
		t.Fatalf("need >=4 frames, got %d", len(frames))
	}
	spliced := append(append([][]byte{}, frames[:1]...), frames[2:]...)
	if err := Verify(bytes.NewReader(joinV2(header, spliced)), pw); err == nil {
		t.Fatal("an archive with a deleted frame verified successfully")
	}
}

// TestFrameReorderIsDetected covers swapping two frames.
func TestFrameReorderIsDetected(t *testing.T) {
	const pw = "pw"
	header, frames := splitV2(t, archiveOf(t, bigTree(t), pw))
	if len(frames) < 4 {
		t.Fatalf("need >=4 frames, got %d", len(frames))
	}
	swapped := append([][]byte{}, frames...)
	swapped[0], swapped[1] = swapped[1], swapped[0]
	if err := Verify(bytes.NewReader(joinV2(header, swapped)), pw); err == nil {
		t.Fatal("an archive with reordered frames verified successfully")
	}
}

// TestCrossArchiveSpliceIsDetected takes a frame from one archive and drops it
// into another made with the SAME passphrase. Distinct salts mean distinct
// key-encryption keys and distinct data keys, so this cannot open — but the test
// exists because "same passphrase" is exactly the case an operator creates by
// making nightly backups.
func TestCrossArchiveSpliceIsDetected(t *testing.T) {
	const pw = "same passphrase for both"
	headerA, framesA := splitV2(t, archiveOf(t, bigTree(t), pw))
	_, framesB := splitV2(t, archiveOf(t, bigTree(t), pw))
	if len(framesA) < 3 || len(framesB) < 3 {
		t.Fatalf("need multi-frame archives")
	}
	mixed := append([][]byte{}, framesA...)
	mixed[1] = framesB[1]
	if err := Verify(bytes.NewReader(joinV2(headerA, mixed)), pw); err == nil {
		t.Fatal("a frame spliced from another archive verified successfully")
	}
}

// TestHeaderTamperIsDetected flips bits across the whole header. The v1 format
// left magic and salt outside the AEAD, so tampering merely produced a wrong
// key; headerHash now puts every header byte inside every frame's AAD.
func TestHeaderTamperIsDetected(t *testing.T) {
	const pw = "pw"
	full := archiveOf(t, bigTree(t), pw)
	for off := 0; off < v2HeaderLen; off++ {
		bad := append([]byte{}, full...)
		bad[off] ^= 0x01
		if err := Verify(bytes.NewReader(bad), pw); err == nil {
			t.Fatalf("header byte %d could be flipped without detection", off)
		}
	}
}

// TestFinalFlagCannotBeForged checks the terminator specifically: an attacker
// who truncates and then flips the final bit on the last surviving frame must
// still fail, because the flag is inside the AAD.
func TestFinalFlagCannotBeForged(t *testing.T) {
	const pw = "pw"
	header, frames := splitV2(t, archiveOf(t, bigTree(t), pw))
	if len(frames) < 3 {
		t.Fatalf("need >=3 frames, got %d", len(frames))
	}
	forged := append([][]byte{}, frames[:len(frames)-2]...)
	last := append([]byte{}, forged[len(forged)-1]...)
	hdr := binary.BigEndian.Uint32(last[:4]) | finalBit
	binary.BigEndian.PutUint32(last[:4], hdr)
	forged[len(forged)-1] = last
	if err := Verify(bytes.NewReader(joinV2(header, forged)), pw); err == nil {
		t.Fatal("forging the final flag turned a truncated archive into a complete one")
	}
}

// TestOversizedFrameLengthRejected guards the allocation bound: a crafted length
// word must not let an attacker make us allocate arbitrarily.
func TestOversizedFrameLengthRejected(t *testing.T) {
	const pw = "pw"
	header, frames := splitV2(t, archiveOf(t, bigTree(t), pw))
	bad := append([]byte{}, header...)
	f := append([]byte{}, frames[0]...)
	binary.BigEndian.PutUint32(f[:4], uint32(maxFrameLen)+1)
	bad = append(bad, f...)
	if err := Verify(bytes.NewReader(bad), pw); err == nil {
		t.Fatal("an oversized frame length was accepted")
	}
}

// TestLegacyV1ArchivesStillRestore is the compatibility guarantee: upgrading
// must never strand an operator's existing backups.
func TestLegacyV1ArchivesStillRestore(t *testing.T) {
	const pw = "legacy"
	archive := sealTarV1(t, pw, map[string]string{"old/file.txt": "V1 CONTENT"})
	dest := t.TempDir()
	if err := Extract(bytes.NewReader(archive), pw, dest); err != nil {
		t.Fatalf("a v1 archive failed to restore: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(dest, "old", "file.txt"))
	if err != nil || string(got) != "V1 CONTENT" {
		t.Fatalf("v1 content mismatch: %q err=%v", got, err)
	}
}

// TestRestoreDisplacesRatherThanDestroys confirms a successful restore moves the
// previous data directory aside instead of overwriting it. A restore is rare,
// deliberate and high-stakes; if the restored data turns out to be the wrong
// generation, the operator must still have the old one.
func TestRestoreDisplacesRatherThanDestroys(t *testing.T) {
	const pw = "pw"
	src := makeTree(t)
	archive := archiveOf(t, src, pw)

	dest := filepath.Join(t.TempDir(), "data")
	if err := os.MkdirAll(dest, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dest, "previous.txt"), []byte("OLD"), 0o640); err != nil {
		t.Fatal(err)
	}

	displaced, err := ExtractStaged(bytes.NewReader(archive), pw, dest)
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	if displaced == "" {
		t.Fatal("an existing data directory was replaced without being preserved")
	}
	if got, err := os.ReadFile(filepath.Join(displaced, "previous.txt")); err != nil || string(got) != "OLD" {
		t.Fatalf("displaced directory does not hold the previous data: %q err=%v", got, err)
	}
	if got, err := os.ReadFile(filepath.Join(dest, "vayupress.db")); err != nil || string(got) != "sqlite-bytes-here" {
		t.Fatalf("restored data not in place: %q err=%v", got, err)
	}
}

// TestVerifyRejectsWrongPassphrase keeps Verify honest — the restore drill calls
// it, so a Verify that passed on a key it could not actually decrypt would make
// "last verified restore" a lie.
func TestVerifyRejectsWrongPassphrase(t *testing.T) {
	archive := archiveOf(t, makeTree(t), "right")
	if err := Verify(bytes.NewReader(archive), "wrong"); !errors.Is(err, ErrBadPassphrase) {
		t.Fatalf("want ErrBadPassphrase, got %v", err)
	}
	if err := Verify(bytes.NewReader(archive), "right"); err != nil {
		t.Fatalf("a good archive failed verification: %v", err)
	}
}

// TestSubstituteAndSkip covers the option Create needs so a live SQLite file is
// archived from a consistent snapshot under its normal name, with the -wal and
// -shm sidecars left out.
func TestSubstituteAndSkip(t *testing.T) {
	const pw = "pw"
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "vayupress.db"), []byte("TORN LIVE BYTES"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "vayupress.db-wal"), []byte("WAL"), 0o640); err != nil {
		t.Fatal(err)
	}
	snap := filepath.Join(t.TempDir(), "snap.db")
	if err := os.WriteFile(snap, []byte("CONSISTENT SNAPSHOT"), 0o640); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	if err := CreateWithOptions(&buf, pw, src, Options{
		Substitute: map[string]string{"vayupress.db": snap},
		Skip:       map[string]bool{"vayupress.db-wal": true},
	}); err != nil {
		t.Fatal(err)
	}
	dest := t.TempDir()
	if err := Extract(bytes.NewReader(buf.Bytes()), pw, dest); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(dest, "vayupress.db"))
	if err != nil || string(got) != "CONSISTENT SNAPSHOT" {
		t.Fatalf("substitution did not take effect: %q err=%v", got, err)
	}
	if _, err := os.Stat(filepath.Join(dest, "vayupress.db-wal")); err == nil {
		t.Error("the -wal sidecar was archived despite being skipped")
	}
}

// ── Precise chain tests ──────────────────────────────────────────────────────
//
// The archive-level attacks above are all caught, but mutation testing showed
// they are caught by the POSITIONAL NONCE, not by the chained AAD: in a
// sequential byte stream a frame's position implies its nonce, so any deletion
// or reorder desynchronises the counter and fails for that reason alone.
// Removing prevTag or headerHash from the AAD left every one of them passing.
//
// That is fine for an archive and useless for the VayuKeep record store, where
// records live in object storage and are fetched BY NAME — position implies
// nothing, one data key spans a whole generation, and the chain becomes the only
// thing standing between an attacker and a silently edited history.
//
// These two tests hold everything the nonce covers constant (same data key, same
// generation id, same sequence number) so that the chain is the only difference.
// They are the tests that fail when the AAD is weakened.

// sealFrames seals payloads into individual wire frames under a caller-supplied
// key, generation id and header hash, returning the frames and the terminator.
func sealFrames(t *testing.T, gcm cipherAEAD, genID, headerHash []byte, payloads ...string) [][]byte {
	t.Helper()
	var buf bytes.Buffer
	w := newSealedWriter(&buf, gcm, genID, headerHash)
	for _, p := range payloads {
		if _, err := w.Write([]byte(p)); err != nil {
			t.Fatal(err)
		}
		if err := w.flush(); err != nil { // force a frame boundary per payload
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	_, frames := splitFramesOnly(t, buf.Bytes())
	return frames
}

// splitFramesOnly parses a bare frame stream (no header prefix).
func splitFramesOnly(t *testing.T, b []byte) (int, [][]byte) {
	t.Helper()
	var frames [][]byte
	for i := 0; i < len(b); {
		n := int(binary.BigEndian.Uint32(b[i:i+4]) &^ finalBit)
		end := i + 4 + n
		if end > len(b) {
			t.Fatalf("ragged frame at %d", i)
		}
		frames = append(frames, b[i:end])
		i = end
	}
	return len(frames), frames
}

// readFrames feeds a frame stream through the sealed reader and reports the
// first error (nil when the stream verifies end to end).
func readFrames(gcm cipherAEAD, genID, headerHash []byte, frames [][]byte) error {
	var buf bytes.Buffer
	for _, f := range frames {
		buf.Write(f)
	}
	r := &sealedReader{r: &buf, gcm: gcm, genID: genID, headerHash: headerHash,
		prevTag: make([]byte, tagLen)}
	if _, err := io.Copy(io.Discard, r); err != nil {
		return err
	}
	return r.finish()
}

// TestChainBindsEachFrameToItsPredecessor swaps in a frame that shares the exact
// nonce (same key, same generation id, same sequence) but follows a different
// predecessor. Only prevTag in the AAD can reject it.
func TestChainBindsEachFrameToItsPredecessor(t *testing.T) {
	_, gcm, genID, headerHash, err := buildHeader("pw")
	if err != nil {
		t.Fatal(err)
	}
	a := sealFrames(t, gcm, genID, headerHash, "first-A", "second")
	b := sealFrames(t, gcm, genID, headerHash, "first-B-different", "second")
	if len(a) < 3 || len(b) < 3 {
		t.Fatalf("expected two data frames plus a terminator, got %d/%d", len(a), len(b))
	}
	if err := readFrames(gcm, genID, headerHash, a); err != nil {
		t.Fatalf("unmodified stream failed to verify: %v", err)
	}
	// Frame 1 of B occupies the same sequence number as frame 1 of A and was
	// sealed with the same key — it differs only in which frame preceded it.
	spliced := [][]byte{a[0], b[1], a[2]}
	if err := readFrames(gcm, genID, headerHash, spliced); err == nil {
		t.Fatal("a frame that followed a different predecessor was accepted — the AAD chain is not binding")
	}
}

// TestChainBindsFramesToTheirHeader swaps in a frame sealed under a different
// header hash, holding key, generation id and sequence identical. Only
// headerHash in the AAD can reject it.
func TestChainBindsFramesToTheirHeader(t *testing.T) {
	_, gcm, genID, headerHash, err := buildHeader("pw")
	if err != nil {
		t.Fatal(err)
	}
	other := make([]byte, len(headerHash))
	copy(other, headerHash)
	other[0] ^= 0xFF // a different archive header, same data key

	a := sealFrames(t, gcm, genID, headerHash, "payload")
	b := sealFrames(t, gcm, genID, other, "payload")
	if len(a) < 2 || len(b) < 2 {
		t.Fatalf("expected a data frame plus a terminator, got %d/%d", len(a), len(b))
	}
	if err := readFrames(gcm, genID, headerHash, a); err != nil {
		t.Fatalf("unmodified stream failed to verify: %v", err)
	}
	spliced := [][]byte{b[0], a[1]}
	if err := readFrames(gcm, genID, headerHash, spliced); err == nil {
		t.Fatal("a frame sealed under a different header was accepted — headerHash is not binding")
	}
}

// TestTerminatorFlagIsAuthenticated holds sequence and predecessor identical and
// changes only the final flag, so the AAD is the sole difference. The reader's
// separate "a terminator carries no payload" rule cannot mask the result here,
// because both frames are genuine terminators.
func TestTerminatorFlagIsAuthenticated(t *testing.T) {
	_, gcm, genID, headerHash, err := buildHeader("pw")
	if err != nil {
		t.Fatal(err)
	}
	// Seal an empty payload at seq 0 with final = FALSE, by hand.
	nonFinal := gcm.Seal(nil, seqNonce(genID, 0), nil,
		frameAAD(headerHash, 0, make([]byte, tagLen), false))
	frame := make([]byte, 4, 4+len(nonFinal))
	binary.BigEndian.PutUint32(frame[:4], uint32(len(nonFinal))|finalBit) // claim final
	frame = append(frame, nonFinal...)

	if err := readFrames(gcm, genID, headerHash, [][]byte{frame}); err == nil {
		t.Fatal("a non-final frame relabelled as the terminator was accepted — the final flag is not authenticated")
	}
}
