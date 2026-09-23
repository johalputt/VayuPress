// SPDX-License-Identifier: Apache-2.0

package customsite

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"time"
)

// KeptGenerations is how many earlier deployments a site keeps for restoring.
//
// It was one — "previous" — so the second bad upload in a row destroyed the
// last good site: the bad one became "previous" and the good one was gone.
// Five covers a working session of attempts, and each costs its own size on
// the disk the upload budget is measured against, which is why it is not
// unlimited.
const KeptGenerations = 5

// Generation is one earlier deployment and what it was.
type Generation struct {
	ID       string   `json:"id"`
	Manifest Manifest `json:"manifest"`
}

// A generation id is a zero-padded Unix-nanosecond time, so the names sort
// in the order they were made and are safe to join into a path.
var genID = regexp.MustCompile(`^[0-9]{20}$`)

func historyDir(base string) string { return filepath.Join(base, "history") }

func newGenID() string { return fmt.Sprintf("%020d", time.Now().UnixNano()) }

// History lists the earlier deployments, newest first.
func History(base string) []Generation {
	entries, err := os.ReadDir(historyDir(base))
	if err != nil {
		return nil
	}
	var out []Generation
	for _, e := range entries {
		if !e.IsDir() || !genID.MatchString(e.Name()) {
			continue
		}
		g := Generation{ID: e.Name()}
		if b, err := os.ReadFile(filepath.Join(historyDir(base), e.Name()+".json")); err == nil {
			_ = json.Unmarshal(b, &g.Manifest)
		}
		out = append(out, g)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID > out[j].ID })
	return out
}

// archiveCurrent moves the live bundle into the history with its manifest,
// returning its id. The caller holds deployMu.
func archiveCurrent(base string) (string, error) {
	current, _, _ := dirs(base)
	if err := os.MkdirAll(historyDir(base), 0o755); err != nil {
		return "", err
	}
	id := newGenID()
	if err := os.Rename(current, filepath.Join(historyDir(base), id)); err != nil {
		return "", err
	}
	if b, err := json.Marshal(ReadManifest(base)); err == nil {
		_ = os.WriteFile(filepath.Join(historyDir(base), id+".json"), b, 0o644) //nolint:gosec // metadata the console shows
	}
	return id, nil
}

// unarchive puts generation id back as the live bundle. The caller holds
// deployMu; it is the undo of archiveCurrent when what follows fails.
func unarchive(base, id string) {
	current, _, _ := dirs(base)
	if os.Rename(filepath.Join(historyDir(base), id), current) == nil {
		_ = os.Remove(filepath.Join(historyDir(base), id+".json"))
	}
}

// migratePrevious folds the single "previous" directory of earlier versions
// into the history, so a site deployed before generations keeps the one
// earlier version it had. The caller holds deployMu.
func migratePrevious(base string) {
	_, previous, _ := dirs(base)
	fi, err := os.Stat(previous)
	if err != nil || !fi.IsDir() {
		return
	}
	if os.MkdirAll(historyDir(base), 0o755) != nil {
		return
	}
	id := fmt.Sprintf("%020d", fi.ModTime().UnixNano())
	if os.Rename(previous, filepath.Join(historyDir(base), id)) != nil {
		return
	}
	if b, err := json.Marshal(Manifest{DeployedAt: fi.ModTime().UTC(), Entry: "index.html"}); err == nil {
		_ = os.WriteFile(filepath.Join(historyDir(base), id+".json"), b, 0o644) //nolint:gosec // metadata the console shows
	}
}

// pruneHistory deletes the oldest generations beyond KeptGenerations.
func pruneHistory(base string) {
	gens := History(base)
	for _, g := range gens[min(len(gens), KeptGenerations):] {
		_ = os.RemoveAll(filepath.Join(historyDir(base), g.ID))
		_ = os.Remove(filepath.Join(historyDir(base), g.ID+".json"))
	}
}

// Restore makes generation id the live bundle. What was live joins the
// history, so a restore can itself be undone.
func Restore(base, id string) error {
	deployMu.Lock()
	defer deployMu.Unlock()
	return restoreLocked(base, id)
}

func restoreLocked(base, id string) error {
	if !genID.MatchString(id) || !dirExists(filepath.Join(historyDir(base), id)) {
		return errors.New("no such earlier deployment")
	}
	current, _, _ := dirs(base)
	var m Manifest
	if b, err := os.ReadFile(filepath.Join(historyDir(base), id+".json")); err == nil {
		_ = json.Unmarshal(b, &m)
	}
	archived := ""
	if dirExists(current) {
		a, err := archiveCurrent(base)
		if err != nil {
			return err
		}
		archived = a
	}
	if err := os.Rename(filepath.Join(historyDir(base), id), current); err != nil {
		if archived != "" {
			unarchive(base, archived)
		}
		return err
	}
	_ = os.Remove(filepath.Join(historyDir(base), id+".json"))
	pruneHistory(base)
	m.HasPrev = len(History(base)) > 0
	writeManifest(base, m)
	return nil
}
