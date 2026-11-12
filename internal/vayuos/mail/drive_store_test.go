// SPDX-License-Identifier: Apache-2.0

package mail

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"time"
)

func hexSum(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// blobRow is how one of ankush's files is kept.
func blobRow(t *testing.T, s *AccountStore, id int64) (blob, fkey, sum, blobSum string) {
	t.Helper()
	if err := s.db.QueryRow(`SELECT blob,fkey,sum,blob_sum FROM vayumail_drive WHERE id=?`, id).Scan(&blob, &fkey, &sum, &blobSum); err != nil {
		t.Fatal(err)
	}
	return
}

// TestAFileIsKeptSealedWithItsSums — not readable on the disk; its content
// sum is the file's own SHA-256 and its stored sum that of the bytes on the
// disk; the page's entry carries the content sum.
func TestAFileIsKeptSealedWithItsSums(t *testing.T) {
	e, s := driveEngine(t)
	body := strings.Repeat("the quarterly figures ", 5000)
	id := put(t, e, 0, "figures.txt", body)
	blob, fkey, sum, blobSum := blobRow(t, s, id)
	path, _ := e.blobPath(calBox, blob)
	stored, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(stored, []byte("quarterly")) || !strings.HasPrefix(fkey, "i1:") {
		t.Fatalf("the file is in the clear on the disk, or its key is %.8q", fkey)
	}
	if sum != hexSum([]byte(body)) || blobSum != hexSum(stored) {
		t.Errorf("sums %s / %s", sum, blobSum)
	}
	if list, _ := e.DriveList(ankush, 0); len(list) != 1 || list[0].Sum != sum {
		t.Errorf("the entry's sum: %+v", list)
	}
	// Written over, the earlier version keeps its own sums.
	put(t, e, 0, "figures.txt", "revised")
	vs, _ := e.DriveVersions(ankush, id)
	if len(vs) != 1 || vs[0].Sum != sum {
		t.Errorf("the earlier version: %+v", vs)
	}
}

// TestAFilesKeyOpensOnlyInItsOwnMailbox — a row moved to another mailbox's
// files does not open; nor without the install's key; and with no key no
// file is kept at all.
func TestAFilesKeyOpensOnlyInItsOwnMailbox(t *testing.T) {
	e, s := driveEngine(t)
	id := put(t, e, 0, "mine.txt", "mine")
	_, fkey, _, _ := blobRow(t, s, id)
	if _, err := e.openFileKey("dana@example.com", fkey); !errors.Is(err, errBlobDamaged) {
		t.Errorf("another mailbox opened the key: %v", err)
	}
	e.UseDriveKey(bytes.Repeat([]byte{8}, 32))
	if _, _, err := e.DriveOpen(ankush, id); !errors.Is(err, errBlobDamaged) {
		t.Errorf("another install key opened the file: %v", err)
	}
	e.driveKey = nil
	if _, err := e.DrivePut(ankush, 0, "new.txt", "", strings.NewReader("x")); !errors.Is(err, ErrDriveNoKey) {
		t.Errorf("a file kept with no key: %v", err)
	}
}

// TestADamagedFileIsRefusedAndMarked — a byte changed on the disk: the read
// stops at it rather than hand out what is not the file, and the file is
// marked damaged.
func TestADamagedFileIsRefusedAndMarked(t *testing.T) {
	e, s := driveEngine(t)
	id := put(t, e, 0, "big.bin", strings.Repeat("x", 3*driveChunk))
	blob, _, _, _ := blobRow(t, s, id)
	path, _ := e.blobPath(calBox, blob)
	stored, _ := os.ReadFile(path)
	stored[blobHeader+driveChunk+blobTagSize+3] ^= 1 // in the second chunk
	if err := os.WriteFile(path, stored, 0o600); err != nil {
		t.Fatal(err)
	}
	_, f, err := e.DriveOpen(ankush, id)
	if err != nil {
		t.Fatal(err)
	}
	n, err := io.Copy(io.Discard, f)
	_ = f.Close()
	if !errors.Is(err, errBlobDamaged) || n != driveChunk {
		t.Errorf("read %d bytes, then %v; want the first chunk and a refusal", n, err)
	}
	if list, _ := e.DriveList(ankush, 0); !list[0].Damaged {
		t.Error("the file is not marked damaged")
	}
	if n, _ := e.DriveDamaged(ankush); n != 1 {
		t.Errorf("%d damaged", n)
	}
}

// TestDrivesUpkeepRewritesAndChecks — a file kept in the clear before is
// rewritten in chunks and reads as it did; a file sealed whole before gets
// its stored sum; a stored file changed on the disk is found at its next
// check, and a file not due is not read.
func TestDrivesUpkeepRewritesAndChecks(t *testing.T) {
	e, s := driveEngine(t)
	md, _ := sealedMaildir(t)
	e.maildir = md
	old := legacyFile(t, e, calBox, "old.txt", "kept in the clear before", false)
	fresh := put(t, e, 0, "fresh.txt", "kept today")
	now := time.Now()
	rewritten, _, damaged := e.careForDrive(now)
	if rewritten != 1 || damaged != 0 {
		t.Fatalf("rewrote %d, found %d damaged", rewritten, damaged)
	}
	for _, p := range diskFiles(t, e) {
		if b, _ := os.ReadFile(p); bytes.Contains(b, []byte("in the clear")) {
			t.Fatalf("the old file is still readable at %s", p)
		}
	}
	if got := read(t, e, ankush, old); got != "kept in the clear before" {
		t.Fatalf("the rewritten file reads %q", got)
	}
	_, fkey, sum, _ := blobRow(t, s, old)
	if !strings.HasPrefix(fkey, "i1:") || sum != hexSum([]byte("kept in the clear before")) {
		t.Errorf("the rewritten file is kept as %.8q with sum %s", fkey, sum)
	}
	// Both are fresh now: nothing is due for a week.
	if _, checked, _ := e.careForDrive(now.Add(time.Hour)); checked != 0 {
		t.Errorf("%d files checked before they were due", checked)
	}
	blob, _, _, _ := blobRow(t, s, fresh)
	path, _ := e.blobPath(calBox, blob)
	stored, _ := os.ReadFile(path)
	stored[len(stored)-1] ^= 1
	if err := os.WriteFile(path, stored, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, checked, damaged := e.careForDrive(now.Add(driveCheckEvery + time.Hour)); checked != 2 || damaged != 1 {
		t.Fatalf("a week on: checked %d, damaged %d", checked, damaged)
	}
	list, _ := e.DriveList(ankush, 0)
	for _, d := range list {
		if d.Damaged != (d.ID == fresh) {
			t.Errorf("%s: damaged is %v", d.Name, d.Damaged)
		}
	}
}

// TestUpkeepRecordsASumForAFileSealedWhole — no key is needed to read a
// private file's stored bytes against their sum.
func TestUpkeepRecordsASumForAFileSealedWhole(t *testing.T) {
	e, s := driveEngine(t)
	md, keys := sealedMaildir(t)
	e.maildir = md
	if err := s.Create(context.Background(), "dana@example.com", "h", "Dana", "mailbox"); err != nil {
		t.Fatal(err)
	}
	makePrivate(t, keys)
	id := legacyFile(t, e, "dana@example.com", "sealed.txt", "sealed whole", true)
	// A file in the clear in a private mailbox (one sealing missed) is
	// rewritten sealed to the holder, not under the install's key.
	clear := legacyFile(t, e, "dana@example.com", "missed.txt", "missed by sealing", false)
	keys.CloseHeld("dana@example.com")
	e.careForDrive(time.Now())
	var clearKey string
	_ = s.db.QueryRow(`SELECT fkey FROM vayumail_drive WHERE id=?`, clear).Scan(&clearKey)
	if !strings.HasPrefix(clearKey, "p1:") {
		t.Errorf("a private mailbox's clear file was rewritten with key %.8q", clearKey)
	}
	var blob, blobSum string
	_ = s.db.QueryRow(`SELECT blob,blob_sum FROM vayumail_drive WHERE id=?`, id).Scan(&blob, &blobSum)
	path, _ := e.blobPath("dana@example.com", blob)
	stored, _ := os.ReadFile(path)
	if blobSum != hexSum(stored) {
		t.Errorf("a file sealed whole has stored sum %q", blobSum)
	}
}

// TestUpkeepReadsAtMostItsShareAPass — past driveCareBytes a pass stops,
// and the rest wait for the next.
func TestUpkeepReadsAtMostItsShareAPass(t *testing.T) {
	e, s := driveEngine(t)
	for i := 0; i < 3; i++ {
		put(t, e, 0, "f"+string(rune('a'+i)), "x")
	}
	// Each kept at the size of a pass's whole share.
	if _, err := s.db.Exec(`UPDATE vayumail_drive SET checked=0, size=?`, driveCareBytes); err != nil {
		t.Fatal(err)
	}
	if _, checked, _ := e.careForDrive(time.Now()); checked != 1 {
		t.Errorf("a pass checked %d files past its share", checked)
	}
	if _, checked, _ := e.careForDrive(time.Now()); checked != 1 {
		t.Errorf("the next pass checked %d", checked)
	}
}
