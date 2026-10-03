// SPDX-License-Identifier: Apache-2.0

package main

// backup_coverage.go — what a backup holds, from where this install actually
// keeps its data.
//
// A backup is one folder, the database's (VayuKeep's DataDir), walked without
// following links. Everything else an install keeps lives at paths derived
// from DB_PATH and MEDIA_DIR, so whether a backup holds it is a fact of the
// configuration, not of the documentation: the Backups page states it from
// these paths, and the test restore counts each of them in the restored copy.

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/johalputt/vayupress/internal/config"
	"github.com/johalputt/vayupress/internal/ui"
)

// backupPlace is one place this install keeps data.
type backupPlace struct {
	Label string
	// What names it in a sentence.
	What string
	Path string
	// Noun is what the test restore reports counting there ("" where it only
	// checks the place is present); a mailbox's messages are counted rather
	// than its index files.
	Noun     string
	Messages bool
	// State is how a backup stands to it: placeHeld, placeEmpty (nothing
	// there yet), placeLinked or placeOutside. Rel is where a held place
	// sits inside the data directory, and so inside a restored copy of it.
	State, Rel string
}

const (
	placeHeld    = "held"
	placeEmpty   = "empty"
	placeLinked  = "linked"
	placeOutside = "outside"
)

// backupPlaces lists where this install keeps data beyond the database, with
// how a backup of dataDir stands to each.
func backupPlaces(dataDir string) []backupPlace {
	places := []backupPlace{
		{Label: "Media library", What: "media", Path: config.Cfg.MediaDir, Noun: "media file"},
		{Label: "Website files", What: "website files", Path: customSiteRoot(), Noun: "website file"},
		{Label: "Mail", What: "mail", Path: filepath.Join(dataDir, "vayudata", "mail"), Noun: "mail message", Messages: true},
		{Label: "PGP keys", What: "PGP keys", Path: filepath.Join(dataDir, "vayudata", "pgp")},
		{Label: "Onion address keys", What: "onion address keys", Path: config.EnvOr("VAYUOS_TOR_DIR", filepath.Join(filepath.Dir(config.Cfg.MediaDir), "tor"))},
		{Label: "Secrets key file", What: "secrets key file", Path: secretKEKFilePath()},
		{Label: "PGP key file", What: "PGP key file", Path: filepath.Join(dataDir, ".vayupgp-kek")},
	}
	for i := range places {
		places[i].State, places[i].Rel = placeState(dataDir, places[i].Path)
	}
	return places
}

// placeState reports how a backup of dataDir stands to path. A path outside
// dataDir is not archived; one reached through a link inside it is skipped by
// the walk; dataDir itself is resolved by the archive, so a link there is
// not one. Either is reported only when something is there to lose.
func placeState(dataDir, path string) (state, rel string) {
	if path == "" {
		return placeEmpty, ""
	}
	root, err := filepath.EvalSymlinks(dataDir)
	if err != nil {
		root = filepath.Clean(dataDir)
	}
	// Under the data directory as configured, or as resolved: an install
	// whose data folder is a link may name its media by either.
	rel, ok := under(filepath.Clean(dataDir), path)
	if !ok {
		if rel, ok = under(root, path); !ok {
			return lost(path, placeOutside), ""
		}
	}
	at := root
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		at = filepath.Join(at, part)
		fi, err := os.Lstat(at)
		if err != nil {
			return placeEmpty, ""
		}
		if fi.Mode()&os.ModeSymlink != 0 {
			return lost(path, placeLinked), ""
		}
	}
	if !holdsAnything(at) {
		return placeEmpty, ""
	}
	return placeHeld, rel
}

// lost is why a backup leaves path out, or placeEmpty when nothing is there.
func lost(path, why string) string {
	if real, err := filepath.EvalSymlinks(path); err == nil && holdsAnything(real) {
		return why
	}
	return placeEmpty
}

// under returns path relative to dir, and whether it lies within it.
func under(dir, path string) (string, bool) {
	rel, err := filepath.Rel(dir, filepath.Clean(path))
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	return rel, true
}

// holdsAnything reports whether path is a regular file or a folder with at
// least one regular file somewhere under it. It stops at the first.
func holdsAnything(path string) bool {
	found := errors.New("found")
	err := filepath.WalkDir(path, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil //nolint:nilerr // an unreadable branch holds nothing this can see
		}
		if d.Type().IsRegular() {
			return found
		}
		return nil
	})
	return errors.Is(err, found)
}

// backupCensus is the test restore's check of everything beyond the database:
// each place the live install has data in, and a backup holds, must be in the
// restored copy too. It returns what it counted, as the page says it.
func backupCensus(dataDir string) func(ctx context.Context, restored string) (string, error) {
	return func(ctx context.Context, restored string) (string, error) {
		var found []string
		for _, p := range backupPlaces(dataDir) {
			if p.State != placeHeld {
				continue
			}
			files, messages, err := countRestored(ctx, filepath.Join(restored, p.Rel))
			if err != nil {
				return "", err
			}
			// Present is judged on files, as the live side is: a mailbox
			// set up with no message yet still has its keys to restore.
			if files == 0 {
				return "", fmt.Errorf("it holds no %s, though this server has some; Back up now takes a copy that does", p.What)
			}
			if n := files; p.Noun != "" {
				if p.Messages {
					n = messages
				}
				found = append(found, groupThousands(n)+" "+p.Noun+plural(n))
			}
		}
		return strings.Join(found, ", "), nil
	}
}

// countRestored counts the regular files under path in the restored copy, and
// among them a mailbox's messages (the files in its cur and new folders).
func countRestored(ctx context.Context, path string) (files, messages int, err error) {
	err = filepath.WalkDir(path, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}
		if d.IsDir() {
			return ctx.Err()
		}
		if !d.Type().IsRegular() {
			return nil
		}
		files++
		if dir := filepath.Base(filepath.Dir(p)); dir == "cur" || dir == "new" {
			messages++
		}
		return nil
	})
	return files, messages, err
}

// groupThousands writes n with thousands separated, as the page writes counts.
func groupThousands(n int) string {
	s := strconv.Itoa(n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}

// leftOut names the places a backup leaves out though data is kept there.
func leftOut(places []backupPlace) []string {
	var out []string
	for _, p := range places {
		if p.State == placeLinked || p.State == placeOutside {
			out = append(out, p.What)
		}
	}
	return out
}

// keepHoldsSection is What a backup holds: the database and every place this
// install keeps data, each as a backup stands to it, then what no backup can
// hold and the operator must keep.
func keepHoldsSection(places []backupPlace) ui.HTML {
	dataDir := filepath.Dir(config.Cfg.DBPath)
	held := ui.State("ok", "In every backup")
	rows := []ui.Row{{Label: "Database", Hint: "Posts, pages, settings, members, comments and everything else kept in it", Control: held}}
	for _, p := range places {
		switch p.State {
		case placeHeld:
			rows = append(rows, ui.Row{Label: p.Label, Hint: p.Path, Control: held})
		case placeLinked:
			rows = append(rows, ui.Row{Label: p.Label, Control: ui.State("warn", "Not backed up"),
				Hint: p.Path + " is a link to another folder, and a backup does not follow links. Move the folder itself into " + dataDir + ", or keep your own copy."})
		case placeOutside:
			rows = append(rows, ui.Row{Label: p.Label, Control: ui.State("warn", "Not backed up"),
				Hint: p.Path + " is outside " + dataDir + ", the folder a backup holds."})
		}
	}
	keep := ui.Text("Keep it yourself")
	rows = append(rows,
		ui.Row{Label: "Start-up settings", Hint: "/etc/vayupress/env on a standard install: your API key and the settings the server starts with", Control: keep},
		ui.Row{Label: "Backup passphrase", Hint: "Without it no backup opens. Keep it somewhere other than this server.", Control: keep})
	if strings.TrimSpace(config.EnvOr("VAYU_SECRET", "")) != "" {
		rows = append(rows, ui.Row{Label: "VAYU_SECRET", Hint: "Set in the start-up settings. Without it, the saved secrets and PGP keys in a restored copy cannot be opened.", Control: keep})
	}
	rows = append(rows, ui.Row{Label: "Talk", Hint: "Talk keeps messages in memory only, by design, so there is nothing to back up.", Control: ui.Text("Never stored")})
	return ui.Section("What a backup holds", "", ui.Rows(rows...))
}
