// SPDX-License-Identifier: Apache-2.0

package vayukeep

// record.go — the last drill's outcome, kept beside the generations.
//
// The drill outcome and the proven generation lived in memory only, so every
// restart (every update, and every settings change, which rebuilds the
// engine) read "never restored" over a generation that had passed, until a
// drill of the whole archive passed again. The record carries them across.
//
// It is believed only as far as it can be checked. The proof names a
// generation with its size and modification time, and is taken back only
// while that file is still there unchanged: a generation replaced under the
// same name is a different archive, proven by nothing. A failed drill is read
// back as failed, so a restart never turns a failure into a pass.

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// DrillRecordName is the record's file name in the target directory. It is
// neither a generation nor scratch, so listing and the abandoned-scratch sweep
// both leave it alone.
const DrillRecordName = "vayukeep-drill.json"

type drillRecord struct {
	At         time.Time `json:"at"`
	OK         bool      `json:"ok"`
	Error      string    `json:"error,omitempty"`
	Rows       int64     `json:"rows"`
	Contents   string    `json:"contents,omitempty"`
	Generation string    `json:"generation"`
	Proven     *provenOn `json:"proven,omitempty"`
}

// provenOn identifies the proven generation's file as it was when it passed.
type provenOn struct {
	Name     string `json:"name"`
	Bytes    int64  `json:"bytes"`
	Modified int64  `json:"modified"`
}

// saveDrillRecord writes the drill just recorded, atomically: a crash leaves
// the previous record or a partial the sweep removes, never a torn one. A
// failure to write costs only what this file exists to keep, so it is logged
// rather than failing the drill.
func (e *Engine) saveDrillRecord(res DrillResult) {
	rec := drillRecord{At: res.At, OK: res.OK, Error: res.Err, Rows: res.Rows, Contents: res.Contents, Generation: res.Generation}
	if name := e.ProvenGeneration(); name != "" {
		if fi, err := os.Stat(filepath.Join(e.cfg.TargetDir, name)); err == nil {
			rec.Proven = &provenOn{Name: name, Bytes: fi.Size(), Modified: fi.ModTime().UnixNano()}
		}
	}
	if err := e.writeRecord(rec); err != nil {
		e.cfg.Log("warn", "the test restore's outcome was not saved, and a restart will forget it: "+err.Error())
	}
}

func (e *Engine) writeRecord(rec drillRecord) error {
	b, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(e.cfg.TargetDir, tmpPrefix)
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), filepath.Join(e.cfg.TargetDir, DrillRecordName))
}

// loadDrillRecord takes back what the last process recorded, unless this one
// has drilled already.
func (e *Engine) loadDrillRecord() {
	b, err := os.ReadFile(filepath.Join(e.cfg.TargetDir, DrillRecordName)) //nolint:gosec // our own target directory
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			e.cfg.Log("warn", "the last test restore's outcome could not be read: "+err.Error())
		}
		return
	}
	var rec drillRecord
	if err := json.Unmarshal(b, &rec); err != nil {
		e.cfg.Log("warn", "the last test restore's outcome is unreadable and is ignored: "+err.Error())
		return
	}
	proven := ""
	if p := rec.Proven; p != nil && isGenerationName(p.Name) {
		if fi, err := os.Stat(filepath.Join(e.cfg.TargetDir, p.Name)); err == nil && fi.Size() == p.Bytes && fi.ModTime().UnixNano() == p.Modified {
			proven = p.Name
		}
	}
	// A pass is about the generation it drilled, which is the proof it left.
	// If that file is gone or changed, the pass says nothing about what is
	// on the target now.
	if rec.OK && rec.Generation != proven {
		rec = drillRecord{}
	}

	e.mu.Lock()
	if e.provenGen == "" {
		e.provenGen = proven
	}
	e.mu.Unlock()
	if rec.At.IsZero() {
		return
	}
	e.setStatus(func(s *Status) {
		if !s.LastDrill.IsZero() {
			return
		}
		s.LastDrill, s.LastDrillOK, s.LastDrillError, s.LastDrillRows = rec.At, rec.OK, rec.Error, rec.Rows
		s.LastDrillContents = rec.Contents
	})
}

// isGenerationName keeps the proof to a generation: retention compares the
// proven name with generation names, so anything else (another file in the
// target, of the right size and time) would move where it cuts.
func isGenerationName(name string) bool {
	_, ok := parseGenerationName(name)
	return ok
}
