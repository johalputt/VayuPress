// SPDX-License-Identifier: Apache-2.0

package mail

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// reconcile_case.go — one-time repair for mail stranded by the address-case
// defect (Section 2 audit).
//
// safeSegment now folds case, so no NEW message can land in a directory its
// owner cannot read. That does nothing for what is already on disk: an install
// that received RCPT TO:<ALICE@example.com> before the fix has those messages in
// .../example.com/ALICE/, accepted with a 250 and unreachable. It also leaves
// one real regression open — a holder who both received mail as "Alice@…" and
// signed in as "Alice@…" could read it before the fold and would read the
// lowercase directory after.
//
// So the variants are merged into the canonical directory at startup. The
// design constraints, in order of importance, because this moves somebody's
// mail:
//
//   - Never lose a message. Files are MOVED with os.Rename (atomic within a
//     filesystem) and a source is removed only by the rename that succeeded.
//     A name clash gets a fresh unique name rather than an overwrite.
//   - Never merge two different people. Grouping is by the same fold
//     safeSegment applies, and only within one already-canonical domain.
//   - Be safe to run on every boot. Idempotent, and a no-op on a clean store.

// ReconcileCaseVariants merges every account directory whose name is not
// already its folded form into the canonical directory, and does the same for
// domain directories. It returns the number of directories merged.
//
// Errors moving an individual message are collected rather than fatal: a
// partially recovered mailbox is strictly better than an aborted repair, and the
// source file is left in place for the next boot to retry.
func (m *Maildir) ReconcileCaseVariants() (int, error) {
	domains, err := os.ReadDir(m.base)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil // no mail store yet
		}
		return 0, err
	}

	merged := 0
	var problems []string

	// Domains first, so the account pass below sees one canonical domain
	// directory rather than the same account under two spellings.
	for _, d := range domains {
		if !d.IsDir() || d.Name() == safeSegment(d.Name()) {
			continue
		}
		n, errs := m.mergeDir(filepath.Join(m.base, d.Name()), filepath.Join(m.base, safeSegment(d.Name())))
		merged += n
		problems = append(problems, errs...)
	}

	// Then accounts, within each canonical domain.
	domains, err = os.ReadDir(m.base)
	if err != nil {
		return merged, err
	}
	for _, d := range domains {
		if !d.IsDir() {
			continue
		}
		domainDir := filepath.Join(m.base, d.Name())
		accounts, err := os.ReadDir(domainDir)
		if err != nil {
			problems = append(problems, d.Name()+": "+err.Error())
			continue
		}
		for _, a := range accounts {
			if !a.IsDir() || a.Name() == safeSegment(a.Name()) {
				continue
			}
			n, errs := m.mergeDir(filepath.Join(domainDir, a.Name()), filepath.Join(domainDir, safeSegment(a.Name())))
			merged += n
			problems = append(problems, errs...)
		}
	}

	if len(problems) > 0 {
		return merged, fmt.Errorf("maildir case reconciliation: %s", strings.Join(problems, "; "))
	}
	return merged, nil
}

// mergeDir moves everything under src into dst and removes src once it is empty.
// When dst does not exist the whole directory is renamed, which is both cheaper
// and atomic.
func (m *Maildir) mergeDir(src, dst string) (int, []string) {
	if src == dst {
		return 0, nil
	}
	if _, err := os.Stat(dst); os.IsNotExist(err) {
		if err := os.Rename(src, dst); err == nil {
			return 1, nil
		}
		// Fall through to the file-by-file path: a rename can fail across
		// filesystems, and a partial recovery beats none.
	}

	var problems []string
	err := filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil // directories are recreated on demand below
		}
		rel, relErr := filepath.Rel(src, path)
		if relErr != nil {
			return nil
		}
		target := filepath.Join(dst, rel)
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			problems = append(problems, target+": "+err.Error())
			return nil
		}
		if err := moveNoClobber(path, target); err != nil {
			problems = append(problems, path+": "+err.Error())
		}
		return nil
	})
	if err != nil {
		problems = append(problems, src+": "+err.Error())
	}

	// Only ever remove what is now empty. RemoveAll here would delete messages
	// whose move failed above — the one outcome this whole file exists to avoid.
	pruneEmptyDirs(src)
	return 1, problems
}

// moveNoClobber renames src to dst, choosing a fresh name if dst is taken.
//
// Two Maildir files can legitimately carry the same name in different
// directories, and an overwrite here would rescue one message by destroying
// another.
func moveNoClobber(src, dst string) error {
	if _, err := os.Stat(dst); os.IsNotExist(err) {
		return os.Rename(src, dst)
	}
	for i := 1; i < 1000; i++ {
		alt := fmt.Sprintf("%s.merged%d", dst, i)
		if _, err := os.Stat(alt); os.IsNotExist(err) {
			return os.Rename(src, alt)
		}
	}
	return fmt.Errorf("no free name for %s", dst)
}

// pruneEmptyDirs removes dir and its empty descendants, deepest first, and stops
// at anything still holding a file.
func pruneEmptyDirs(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() {
			pruneEmptyDirs(filepath.Join(dir, e.Name()))
		}
	}
	// Fails harmlessly when anything survived, which is the intended guard.
	_ = os.Remove(dir)
}
