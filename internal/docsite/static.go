// SPDX-License-Identifier: Apache-2.0

package docsite

import (
	"errors"
	"io/fs"
	"sort"
	"strings"

	"github.com/johalputt/vayupress/internal/customsite"
)

// Static is a site that is a folder of the repository, served as it is: each
// file under dir at its path below dir.
//
// A file a site cannot carry is left out and named in skipped rather than
// refused. The folder is part of a repository, and a README.md beside the
// pages is normal there; refusing it would stop every deploy of the site over
// a file nobody meant to publish (the deploy refuses a whole bundle for one
// such file). What is left out is named, so it is never a surprise.
func Static(repo fs.FS, dir string) (b Bundle, skipped []string, err error) {
	b = Bundle{}
	err = fs.WalkDir(repo, dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel := strings.TrimPrefix(p, dir+"/")
		if !customsite.ExtAllowed(rel) {
			skipped = append(skipped, rel)
			return nil
		}
		data, err := fs.ReadFile(repo, p)
		if err != nil {
			return err
		}
		b[rel] = data
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	if _, ok := b["index.html"]; !ok {
		return nil, nil, errors.New(dir + "/ has no index.html, so the site would have no front page")
	}
	sort.Strings(skipped)
	return b, skipped, nil
}
