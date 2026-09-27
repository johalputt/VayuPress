// SPDX-License-Identifier: Apache-2.0

// Command vayudocs builds the vayupress.com site from a checkout, with the same
// renderer an install uses when a domain follows the repository
// (internal/docsite). It is how the site is previewed before a push, and how a
// release carries a copy of it.
//
// Usage: vayudocs -repo . -commit "$(git rev-parse HEAD)" -out dist/site
//
//	-zip writes the bundle as one .zip, as the console's upload takes it.
package main

import (
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/johalputt/vayupress"
	"github.com/johalputt/vayupress/internal/docsite"
)

func main() {
	repo := flag.String("repo", ".", "the repository checkout to build from")
	commit := flag.String("commit", "", "the commit the checkout is at")
	source := flag.String("source", "https://github.com/johalputt/vayupress", "the repository's web address")
	out := flag.String("out", "dist/site", "directory to write the site into")
	zipOut := flag.String("zip", "", "write the bundle as this .zip instead of a directory")
	site := flag.String("site", "", `which site: the product site (empty) or "updates", the release mirror's page`)
	home := flag.String("home", "", "the product site's address, for a build that is not it")
	download := flag.String("download", "", "the mirror's page, where Download links go")
	flag.Parse()

	assets, err := fs.Sub(vayupress.StaticFS, "static")
	if err != nil {
		fail(err)
	}
	b, err := docsite.Build(docsite.Input{Repo: os.DirFS(*repo), Commit: *commit, Source: *source, Synced: time.Now().UTC(), Assets: assets,
		Site: *site, Home: *home, Download: *download})
	if err != nil {
		fail(err)
	}
	if *zipOut != "" {
		data, err := b.Zip()
		if err != nil {
			fail(err)
		}
		if err := os.WriteFile(*zipOut, data, 0o644); err != nil {
			fail(err)
		}
		fmt.Printf("vayudocs: %d files into %s\n", len(b), *zipOut)
		return
	}
	for name, data := range b {
		p := filepath.Join(*out, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			fail(err)
		}
		if err := os.WriteFile(p, data, 0o644); err != nil {
			fail(err)
		}
	}
	fmt.Printf("vayudocs: %d files into %s\n", len(b), *out)
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "vayudocs:", err)
	os.Exit(1)
}
