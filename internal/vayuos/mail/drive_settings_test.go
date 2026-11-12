// SPDX-License-Identifier: Apache-2.0

package mail

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"
)

// TestAHoldersDriveSettingsAreKept — versions kept and days in Trash, in
// their ranges; fewer versions take effect at once; each mailbox's Trash
// keeps things as long as its holder chose.
func TestAHoldersDriveSettingsAreKept(t *testing.T) {
	e, s := driveEngine(t)
	if ds, _ := e.DriveSettingsOf(ankush); ds != (DriveSettings{Versions: 5, TrashDays: 30}) {
		t.Fatalf("the defaults: %+v", ds)
	}
	for _, bad := range []DriveSettings{{0, 30}, {21, 30}, {5, 6}, {5, 91}} {
		if err := e.SetDriveSettings(ankush, bad); !errors.Is(err, ErrDriveSettings) {
			t.Errorf("%+v: %v", bad, err)
		}
	}
	id := put(t, e, 0, "plan.txt", "v1")
	for _, body := range []string{"v2", "v3", "v4", "v5"} {
		put(t, e, 0, "plan.txt", body)
	}
	if vs, _ := e.DriveVersions(ankush, id); len(vs) != 4 {
		t.Fatalf("%d versions before", len(vs))
	}
	if err := e.SetDriveSettings(ankush, DriveSettings{Versions: 2, TrashDays: 7}); err != nil {
		t.Fatal(err)
	}
	// Counted on the disk's side, not through the list, which shows the
	// number chosen whatever is kept.
	kept := func() int {
		var n int
		_ = s.db.QueryRow(`SELECT COUNT(1) FROM vayumail_drive_versions WHERE file_id=?`, id).Scan(&n)
		return n
	}
	if n := kept(); n != 2 {
		t.Fatalf("%d versions kept after choosing two", n)
	}
	put(t, e, 0, "plan.txt", "v6")
	if n := kept(); n != 2 {
		t.Fatalf("%d versions kept after the next save", n)
	}
	vs, _ := e.DriveVersions(ankush, id)
	if got := readVersion(t, e, ankush, id, vs[1].ID); got != "v4" {
		t.Errorf("the versions kept are not the latest: the older reads %q", got)
	}
	// Trash: ankush keeps seven days, dana the thirty of the default.
	if err := s.Create(context.Background(), "dana@example.com", "h", "Dana", "mailbox"); err != nil {
		t.Fatal(err)
	}
	dana := ReadAsOwner("dana")
	mine := put(t, e, 0, "old.txt", "x")
	theirs, err := e.DrivePut(dana, 0, "old.txt", "", nopReader{})
	if err != nil {
		t.Fatal(err)
	}
	for who, fid := range map[Reader]int64{ankush: mine, dana: theirs} {
		if err := e.DriveTrash(who, fid, false); err != nil {
			t.Fatal(err)
		}
	}
	e.emptyOldTrash(time.Now().Add(8 * 24 * time.Hour))
	if got, _ := e.DriveTrashed(ankush); len(got) != 0 {
		t.Errorf("ankush's Trash kept %d past seven days", len(got))
	}
	if got, _ := e.DriveTrashed(dana); len(got) != 1 {
		t.Errorf("dana's Trash kept %d at eight days, of thirty", len(got))
	}
}

type nopReader struct{}

func (nopReader) Read([]byte) (int, error) { return 0, io.EOF }

func readVersion(t *testing.T, e *Engine, rd Reader, id, version int64) string {
	t.Helper()
	_, f, err := e.DriveVersionOpen(rd, id, version)
	if err != nil {
		t.Fatalf("version %d: %v", version, err)
	}
	defer f.Close()
	b, _ := io.ReadAll(f)
	return string(b)
}

// TestAnEarlierVersionIsReadWithoutBringingItBack — the file stays as it
// is; another file's version, or another holder's, is not reached; a
// version kept before chunks reads too.
func TestAnEarlierVersionIsReadWithoutBringingItBack(t *testing.T) {
	e, s := driveEngine(t)
	id := put(t, e, 0, "plan.txt", "first")
	put(t, e, 0, "plan.txt", "second")
	other := put(t, e, 0, "other.txt", "o1")
	put(t, e, 0, "other.txt", "o2")
	vs, _ := e.DriveVersions(ankush, id)
	if got := readVersion(t, e, ankush, id, vs[0].ID); got != "first" {
		t.Fatalf("the version reads %q", got)
	}
	if got := read(t, e, ankush, id); got != "second" {
		t.Errorf("reading the version changed the file: %q", got)
	}
	ovs, _ := e.DriveVersions(ankush, other)
	if _, _, err := e.DriveVersionOpen(ankush, id, ovs[0].ID); !errors.Is(err, ErrDriveNotFound) {
		t.Errorf("another file's version through this one: %v", err)
	}
	if err := s.Create(context.Background(), "dana@example.com", "h", "Dana", "mailbox"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.DriveVersionOpen(ReadAsOwner("dana"), id, vs[0].ID); !errors.Is(err, ErrDriveNotFound) {
		t.Errorf("another holder read it: %v", err)
	}
	// A version kept before chunks: its row has no key.
	old := legacyFile(t, e, calBox, "old.txt", "kept before", false)
	put(t, e, 0, "old.txt", "kept after")
	olds, _ := e.DriveVersions(ankush, old)
	if got := readVersion(t, e, ankush, old, olds[0].ID); got != "kept before" {
		t.Errorf("an old version reads %q", got)
	}
}

// TestEmptyTrashDeletesWhatIsInIt — folders with what is in them, and
// nothing else.
func TestEmptyTrashDeletesWhatIsInIt(t *testing.T) {
	e, _ := driveEngine(t)
	keep := put(t, e, 0, "keep.txt", "k")
	gone := put(t, e, 0, "gone.txt", "g")
	dir, err := e.DriveMkdir(ankush, 0, "Old")
	if err != nil {
		t.Fatal(err)
	}
	put(t, e, dir, "inside.txt", "i")
	for _, id := range []int64{gone, dir} {
		if err := e.DriveTrash(ankush, id, false); err != nil {
			t.Fatal(err)
		}
	}
	n, err := e.DriveEmptyTrash(ankush)
	if err != nil || n != 2 {
		t.Fatalf("emptied %d: %v", n, err)
	}
	if got, _ := e.DriveTrashed(ankush); len(got) != 0 {
		t.Errorf("%d left in Trash", len(got))
	}
	if left := len(diskFiles(t, e)); left != 1 || read(t, e, ankush, keep) != "k" {
		t.Errorf("%d files left on the disk", left)
	}
	if _, err := e.DriveEmptyTrash(ReadAsOperator("ankush", "boss@example.com")); !errors.Is(err, ErrDriveHolderOnly) {
		t.Errorf("an administrator emptied it: %v", err)
	}
}

// TestALinksDownloadsAreRecorded — when, newest first, at most fifty, and
// gone with the link.
func TestALinksDownloadsAreRecorded(t *testing.T) {
	e, s := driveEngine(t)
	id := put(t, e, 0, "terms.txt", "the terms")
	token, err := e.DriveShareFile(ankush, id, "", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < maxShareRecord+5; i++ {
		if _, err := fetch(e, token, ""); err != nil {
			t.Fatal(err)
		}
	}
	links, _ := e.DriveShares(ankush, id)
	if len(links) != 1 || links[0].Downloads != maxShareRecord+5 || len(links[0].Recent) != maxShareRecord {
		t.Fatalf("the link: %d downloads, %d recorded", links[0].Downloads, len(links[0].Recent))
	}
	if time.Since(links[0].Recent[0]) > time.Minute {
		t.Errorf("the latest download is recorded at %v", links[0].Recent[0])
	}
	var n int
	_ = s.db.QueryRow(`SELECT COUNT(1) FROM vayumail_drive_share_downloads`).Scan(&n)
	if n != maxShareRecord {
		t.Errorf("%d records kept, not the latest %d", n, maxShareRecord)
	}
	if err := e.DriveUnshare(ankush, links[0].ID); err != nil {
		t.Fatal(err)
	}
	_ = s.db.QueryRow(`SELECT COUNT(1) FROM vayumail_drive_share_downloads`).Scan(&n)
	if n != 0 {
		t.Errorf("%d records left after the link was stopped", n)
	}
}
