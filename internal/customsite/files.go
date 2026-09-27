// SPDX-License-Identifier: Apache-2.0

package customsite

// files.go — the live bundle one file at a time, so a site can be read and
// changed without sending the whole of it again. A twelve-word fix to one page
// of an 800-page site used to mean re-uploading the 800 pages.

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
)

// File is one file of the live bundle.
type File struct {
	Path string `json:"path"`
	Size int64  `json:"size"`
}

// Files lists the live bundle, sorted by path. A domain with no bundle has none.
func Files(base string) ([]File, error) {
	current, _, _ := dirs(base)
	root, err := os.OpenRoot(current)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer root.Close()
	var out []File
	err = fs.WalkDir(root.FS(), ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		fi, err := d.Info()
		if err != nil {
			return err
		}
		out = append(out, File{Path: p, Size: fi.Size()})
		return nil
	})
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, err
}

// ReadFile reads one file of the live bundle, through the same confined root
// Serve reads from.
func ReadFile(base, name string) ([]byte, error) {
	rel, err := safeRel(name)
	if err != nil {
		return nil, err
	}
	current, _, _ := dirs(base)
	root, err := os.OpenRoot(current)
	if err != nil {
		return nil, fmt.Errorf("no website is deployed here")
	}
	defer root.Close()
	b, err := root.ReadFile(filepath.FromSlash(rel))
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("the live site has no file %q", rel)
	}
	return b, err
}

// Edit changes some files of the live bundle and deploys the result: put
// writes each file (new or replacing), remove deletes each. With no live
// bundle, put is the whole site.
//
// The result goes live through deployLocked, exactly as an upload does: the
// same allowlist, the same confinement, the same index.html rule, and the
// bundle it replaces joins the history. Writing the changed files into
// current/ directly would have been quicker and a second deploy path, and a
// half-applied edit would have been live between one file and the next.
//
// deployMu is held from reading the live bundle to replacing it, so an upload
// finishing meanwhile is not silently undone by an edit made to the site
// before it.
func Edit(base string, put map[string][]byte, remove []string, budget int64) (Manifest, error) {
	if len(put) == 0 && len(remove) == 0 {
		return Manifest{}, errors.New("nothing to change: name files to write or to delete")
	}
	puts := make(map[string][]byte, len(put))
	for name, data := range put {
		rel, err := safeRel(name)
		if err != nil {
			return Manifest{}, err
		}
		// Refused here rather than dropped by the deploy: a file somebody wrote
		// on purpose and never appears is the gap Manifest.Skipped exists to
		// report for uploads, and for a single named file it is simply an error.
		if ignorableJunk(rel) || !ExtAllowed(rel) {
			return Manifest{}, fmt.Errorf("%q cannot be part of a site: only static web files can", rel)
		}
		puts[rel] = data
	}

	deployMu.Lock()
	defer deployMu.Unlock()
	live, err := Files(base)
	if err != nil {
		return Manifest{}, err
	}
	have := make(map[string]bool, len(live))
	for _, f := range live {
		have[f.Path] = true
	}
	drop := make(map[string]bool, len(remove))
	for _, name := range remove {
		rel, err := safeRel(name)
		if err != nil {
			return Manifest{}, err
		}
		// A delete that matches nothing is a mistyped path, and succeeding
		// would report a removal that did not happen.
		if !have[rel] {
			return Manifest{}, fmt.Errorf("the live site has no file %q to delete", rel)
		}
		if _, both := puts[rel]; both {
			return Manifest{}, fmt.Errorf("%q is both written and deleted", rel)
		}
		drop[rel] = true
	}

	tmp := filepath.Join(base, ".edit.zip")
	if err := os.MkdirAll(base, 0o755); err != nil {
		return Manifest{}, err
	}
	defer os.Remove(tmp) //nolint:errcheck // a leftover is replaced by the next edit
	if err := writeEdited(tmp, base, live, puts, drop); err != nil {
		return Manifest{}, err
	}
	zr, err := zip.OpenReader(tmp)
	if err != nil {
		return Manifest{}, err
	}
	defer zr.Close()
	return deployLocked(base, &zr.Reader, budget)
}

// writeEdited writes the edited site as a zip: the live files that are kept,
// then the new ones. Stored, not deflated: it is read once, straight back.
func writeEdited(dst, base string, live []File, puts map[string][]byte, drop map[string]bool) error {
	f, err := os.Create(dst)
	if err != nil {
		return err
	}
	zw := zip.NewWriter(f)
	err = func() error {
		current, _, _ := dirs(base)
		var root *os.Root
		if len(live) > 0 {
			if root, err = os.OpenRoot(current); err != nil {
				return err
			}
			defer root.Close()
		}
		for _, lf := range live {
			if drop[lf.Path] {
				continue
			}
			if _, replaced := puts[lf.Path]; replaced {
				continue
			}
			w, err := zw.CreateHeader(&zip.FileHeader{Name: lf.Path, Method: zip.Store})
			if err != nil {
				return err
			}
			src, err := root.Open(filepath.FromSlash(lf.Path))
			if err != nil {
				return err
			}
			_, err = io.Copy(w, src)
			src.Close()
			if err != nil {
				return err
			}
		}
		names := make([]string, 0, len(puts))
		for n := range puts {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			w, err := zw.CreateHeader(&zip.FileHeader{Name: n, Method: zip.Store})
			if err != nil {
				return err
			}
			if _, err := w.Write(puts[n]); err != nil {
				return err
			}
		}
		return zw.Close()
	}()
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}
