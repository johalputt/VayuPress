// SPDX-License-Identifier: Apache-2.0

// Package docsite renders a repository's documentation into a static website:
// its guides, its decision records, its changelog and a few pages of its own,
// in the design vayupress.com was drawn in. An install that follows a
// repository builds one on every change to the branch it follows and deploys it
// as that domain's bundle, so nothing on the site is copied there by hand.
//
// What is written lives in the repository and arrives with the sync: every
// document, decision, release note, page and picture. How it is drawn (the
// templates, the stylesheet, the script) lives here and ships with the binary,
// tested with it, so a change of design is a release and a change of words is
// a commit.
package docsite

import (
	"archive/zip"
	"bytes"
	"io/fs"
	"sort"
	"time"
)

// Input is everything one build reads.
type Input struct {
	// Repo is the repository at Commit. The build reads docs/ and CHANGELOG.md.
	Repo fs.FS
	// Commit is the commit Repo holds, in full.
	Commit string
	// Source is the repository's web address, where "View the source" and a
	// link to a file outside docs/ lead.
	Source string
	// Synced is when the sync that fetched Commit ran; the pages say how fresh
	// they are from it.
	Synced time.Time
	// Install is read from the install that serves the site.
	Install Install
	// Updates is what earlier syncs brought in, newest first.
	Updates []Update
	// Assets is the binary's own static tree, for the fonts and the brand mark.
	Assets fs.FS
	// Site is which of the two sites to draw: the product site (empty), or
	// SiteUpdates, the release mirror's own page. They share one layout and
	// one navigation, so they read as one site on two addresses.
	Site string
	// Home is the product site's address, for a build that is not it:
	// "https://vayupress.com". Empty means this build is the product site.
	Home string
	// Download is the mirror's page, which every Download link opens:
	// "https://updates.vayupress.com/". Empty means the forge's latest release.
	Download string
}

// SiteUpdates draws the release mirror's page.
const SiteUpdates = "updates"

// Install is what the serving install says about itself, read when the site is
// built rather than written into a page.
type Install struct {
	Host  string // the install's own domain: "johal.in"
	Posts int    // published posts
	Sites int    // domains it serves
}

// Update is one thing a sync brought in, as the home page's feed shows it.
type Update struct {
	At    time.Time `json:"at"`
	Kind  string    `json:"kind"` // Decision, Doc, Release or Screens
	Title string    `json:"title"`
	Note  string    `json:"note,omitempty"`
	Href  string    `json:"href"`
}

// Bundle is a built site: each file's path in the bundle, and its bytes.
type Bundle map[string][]byte

// Zip packs the bundle as customsite.Deploy reads it. The entries are sorted
// and carry no times, so the same site packs to the same bytes.
func (b Bundle) Zip() ([]byte, error) {
	names := make([]string, 0, len(b))
	for n := range b {
		names = append(names, n)
	}
	sort.Strings(names)
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, n := range names {
		w, err := zw.CreateHeader(&zip.FileHeader{Name: n, Method: zip.Deflate})
		if err != nil {
			return nil, err
		}
		if _, err := w.Write(b[n]); err != nil {
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
