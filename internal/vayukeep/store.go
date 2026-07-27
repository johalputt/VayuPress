// SPDX-License-Identifier: Apache-2.0

package vayukeep

// store.go — writing, listing, pruning and reading generations on the target.
//
// A generation is one sealed VPBK2 archive named `vk-<UTC timestamp>.vpbk`. The
// name carries the ordering so listing needs no index file that could disagree
// with the directory it describes — the files are the record.
//
// Writes are atomic: the archive is built under a temporary name in the same
// directory and renamed into place only after it has been closed, so a crash or
// a full disk leaves a discarded partial rather than a plausible-looking
// generation. Together with the format's terminator frame that means a file
// bearing a generation name is always a complete one.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/johalputt/vayupress/internal/backup"
)

const (
	genPrefix     = "vk-"
	genSuffix     = ".vpbk"
	genTimestamp  = "20060102-150405"
	tmpPrefix     = ".vk-partial-"
	scratchPrefix = ".vk-"
)

// Generation is one sealed archive on the target.
type Generation struct {
	Name  string
	Path  string
	Taken time.Time
	Bytes int64
}

// generationName builds the filename for a point in time.
func generationName(t time.Time) string {
	return genPrefix + t.UTC().Format(genTimestamp) + genSuffix
}

// parseGenerationName returns the instant encoded in a generation filename.
func parseGenerationName(name string) (time.Time, bool) {
	if !strings.HasPrefix(name, genPrefix) || !strings.HasSuffix(name, genSuffix) {
		return time.Time{}, false
	}
	stamp := strings.TrimSuffix(strings.TrimPrefix(name, genPrefix), genSuffix)
	t, err := time.ParseInLocation(genTimestamp, stamp, time.UTC)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

// List returns every generation on the target, newest first.
func (e *Engine) List() ([]Generation, error) {
	entries, err := os.ReadDir(e.cfg.TargetDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var gens []Generation
	for _, ent := range entries {
		if ent.IsDir() {
			continue
		}
		taken, ok := parseGenerationName(ent.Name())
		if !ok {
			continue
		}
		info, err := ent.Info()
		if err != nil {
			continue
		}
		gens = append(gens, Generation{
			Name:  ent.Name(),
			Path:  filepath.Join(e.cfg.TargetDir, ent.Name()),
			Taken: taken,
			Bytes: info.Size(),
		})
	}
	sort.Slice(gens, func(i, j int) bool { return gens[i].Taken.After(gens[j].Taken) })
	return gens, nil
}

// Newest returns the most recent generation, or false when there is none.
func (e *Engine) Newest() (Generation, bool) {
	gens, err := e.List()
	if err != nil || len(gens) == 0 {
		return Generation{}, false
	}
	return gens[0], true
}

// At returns the newest generation taken at or before t — the point-in-time
// restore selector. Restoring "as of" a moment means the last state that
// existed before it, not the nearest one in either direction: rolling FORWARD
// past the moment an operator asked for would hand back data they were trying
// to escape.
func (e *Engine) At(t time.Time) (Generation, bool) {
	gens, err := e.List()
	if err != nil {
		return Generation{}, false
	}
	for _, g := range gens { // newest first
		if !g.Taken.After(t) {
			return g, true
		}
	}
	return Generation{}, false
}

// writeGeneration snapshots the database, seals the data directory around it and
// renames the result into place. It returns the archive size.
func (e *Engine) writeGeneration(ctx context.Context) (int64, error) {
	if err := os.MkdirAll(e.cfg.TargetDir, 0o700); err != nil {
		return 0, err
	}
	// The snapshot lands outside the data directory so the walk cannot pick it up
	// as a file to archive — and on the TARGET's filesystem, not in the default
	// temporary directory. /tmp is a tmpfs on most modern distributions, so
	// vacuuming a multi-gigabyte database into it writes the whole thing to RAM
	// and takes the machine down. The target already has room for generations.
	tmpDir, err := os.MkdirTemp(e.cfg.TargetDir, ".vk-snap-")
	if err != nil {
		return 0, err
	}
	defer os.RemoveAll(tmpDir)

	opts := backup.Options{}
	if e.cfg.DBPath != "" {
		if _, statErr := os.Stat(e.cfg.DBPath); statErr == nil {
			rel, relErr := filepath.Rel(filepath.Clean(e.cfg.DataDir), filepath.Clean(e.cfg.DBPath))
			if relErr != nil {
				return 0, relErr
			}
			snap := filepath.Join(tmpDir, "snapshot.db")
			if err := e.cfg.Snapshot(ctx, e.cfg.DBPath, snap); err != nil {
				return 0, fmt.Errorf("snapshot: %w", err)
			}
			slash := filepath.ToSlash(rel)
			opts.Substitute = map[string]string{slash: snap}
			opts.Skip = map[string]bool{slash + "-wal": true, slash + "-shm": true}
		}
	}

	name := generationName(e.cfg.Now())
	final := filepath.Join(e.cfg.TargetDir, name)
	tmp, err := os.CreateTemp(e.cfg.TargetDir, tmpPrefix)
	if err != nil {
		return 0, err
	}
	tmpPath := tmp.Name()
	committed := false
	defer func() {
		tmp.Close()
		if !committed {
			_ = os.Remove(tmpPath)
		}
	}()
	if err := tmp.Chmod(0o600); err != nil {
		return 0, err
	}
	if err := backup.CreateWithOptions(tmp, e.cfg.Passphrase, e.cfg.DataDir, opts); err != nil {
		return 0, err
	}
	// fsync before the rename: without it a crash can leave a correctly-named
	// generation whose bytes never reached the disk.
	if err := tmp.Sync(); err != nil {
		return 0, err
	}
	size, err := tmp.Seek(0, io.SeekCurrent)
	if err != nil {
		return 0, err
	}
	if err := tmp.Close(); err != nil {
		return 0, err
	}
	if err := os.Rename(tmpPath, final); err != nil {
		return 0, err
	}
	committed = true
	e.cfg.Log("info", fmt.Sprintf("generation %s written (%d bytes)", name, size))
	return size, nil
}

// prune enforces retention. A generation survives if it is within EITHER bound —
// the newest N, or the last D days — so a site that was quiet for a month cannot
// age out the only copy it has.
func (e *Engine) prune() error {
	gens, err := e.List()
	if err != nil {
		return err
	}
	cutoff := e.cfg.Now().Add(-time.Duration(e.cfg.RetainDays) * 24 * time.Hour)
	var removed int
	for i, g := range gens { // newest first
		if i < e.cfg.RetainGenerations {
			continue
		}
		if g.Taken.After(cutoff) {
			continue
		}
		if err := os.Remove(g.Path); err != nil {
			return err
		}
		removed++
	}
	if removed > 0 {
		e.cfg.Log("info", fmt.Sprintf("retention removed %d generation(s)", removed))
	}
	// Sweep abandoned partials from an interrupted write. They are never valid
	// generations, so leaving them only consumes disk.
	entries, err := os.ReadDir(e.cfg.TargetDir)
	if err != nil {
		return nil //nolint:nilerr // retention already succeeded; a sweep failure is not fatal
	}
	for _, ent := range entries {
		if !strings.HasPrefix(ent.Name(), tmpPrefix) && !strings.HasPrefix(ent.Name(), scratchPrefix) {
			continue
		}
		info, err := ent.Info()
		if err != nil || e.cfg.Now().Sub(info.ModTime()) <= time.Hour {
			continue
		}
		// Directories here are abandoned snapshot/drill scratch space, files are
		// abandoned partial writes. Both only consume disk.
		_ = os.RemoveAll(filepath.Join(e.cfg.TargetDir, ent.Name()))
	}
	return nil
}

// refreshFromTarget recomputes the observable generation state by reading the
// target directory. The files are the source of truth, so a restarted process —
// or one whose target was pruned externally — reports what is actually there.
func (e *Engine) refreshFromTarget() {
	gens, err := e.List()
	if err != nil {
		e.setStatus(func(s *Status) { s.LastError = err.Error() })
		return
	}
	var total int64
	for _, g := range gens {
		total += g.Bytes
	}
	e.setStatus(func(s *Status) {
		s.Generations = len(gens)
		s.TotalBytes = total
		if len(gens) > 0 {
			s.NewestGen = gens[0].Taken
		} else {
			s.NewestGen = time.Time{}
		}
	})
}

// Delete removes one generation from the target permanently.
func (e *Engine) Delete(gen Generation) error {
	if err := os.Remove(gen.Path); err != nil {
		return err
	}
	e.cfg.Log("info", "generation "+gen.Name+" deleted by the operator")
	e.refreshFromTarget()
	return nil
}

// Prune applies retention now rather than at the next cycle.
func (e *Engine) Prune() error {
	if err := e.prune(); err != nil {
		return err
	}
	e.refreshFromTarget()
	return nil
}
