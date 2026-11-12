// SPDX-License-Identifier: Apache-2.0

package main

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"strconv"
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/users"
)

// TestADrivesSheetShowsItsSumAndWhatIsDamaged — a file's sheet gives the
// SHA-256 of what was kept, to compare with a copy; a file found damaged
// says so on its sheet and on the page.
func TestADrivesSheetShowsItsSumAndWhatIsDamaged(t *testing.T) {
	a, db := appWithMailDB(t, nil)
	udb, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = udb.Close() })
	a.userStore = users.New(udb)
	body := "the minutes of the meeting"
	driveUpload(a, dana(), 0, "minutes.txt", body)
	id := driveIDs(t, a, "dana", 0)["minutes.txt"]
	sum := sha256.Sum256([]byte(body))
	sheet := calGet(a, a.handleVayuOSDriveEntry, "/os/vayumail/drive/entry?id="+strconv.FormatInt(id, 10), dana(), "").Body.String()
	if !strings.Contains(sheet, "SHA-256 <span class=\"mono drive-sum\">"+hex.EncodeToString(sum[:])+"</span>") || strings.Contains(sheet, "are not what was kept") {
		t.Fatalf("the sheet: %s", sheet)
	}
	// Found damaged by a read or by the upkeep's check.
	if _, err := db.Exec(`UPDATE vayumail_drive SET damaged=1 WHERE id=?`, id); err != nil {
		t.Fatal(err)
	}
	sheet = calGet(a, a.handleVayuOSDriveEntry, "/os/vayumail/drive/entry?id="+strconv.FormatInt(id, 10), dana(), "").Body.String()
	if !strings.Contains(sheet, "This file's bytes on the server are not what was kept") {
		t.Errorf("a damaged file's sheet: %s", sheet)
	}
	if page := calGet(a, a.handleVayuOSDrive, "/os/vayumail/drive", dana(), "").Body.String(); !strings.Contains(page, "1 file or version in this Drive no longer matches what was kept") {
		t.Errorf("the page does not say a file is damaged")
	}
}
