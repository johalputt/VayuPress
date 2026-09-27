// SPDX-License-Identifier: Apache-2.0

package docsite

import (
	"bytes"
	"context"
	"crypto/sha1" //nolint:gosec // git names a file by the SHA-1 of its contents; this checks that name, it secures nothing
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Source is what a site follows: a repository's branch ("johalputt/vayupress",
// "main") and, for a site that is a folder of it, that folder.
type Source struct {
	Repo   string `json:"repo"`
	Branch string `json:"branch"`
	// Dir is the folder that is the site, served as it is ("site",
	// "docs/site/public"). Empty means the site is rendered from the
	// repository's docs/ and CHANGELOG.md.
	Dir string `json:"dir,omitempty"`
}

var (
	repoName   = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,100}/[A-Za-z0-9_.-]{1,100}$`)
	branchName = regexp.MustCompile(`^[A-Za-z0-9._/-]{1,200}$`)
)

// Valid reports why a source cannot be followed, or nil. Both names become
// parts of URLs, so they are held to what GitHub allows rather than escaped.
func (s Source) Valid() error {
	switch {
	case !repoName.MatchString(s.Repo) || strings.Contains(s.Repo, ".."):
		return errors.New("a repository is named owner/name")
	case !branchName.MatchString(s.Branch) || strings.Contains(s.Branch, "..") || strings.HasPrefix(s.Branch, "/"):
		return errors.New("that is not a branch name")
	case s.Dir != "" && (!fs.ValidPath(s.Dir) || s.Dir == "."):
		return errors.New("a folder is a path inside the repository, like site or docs/site")
	}
	return nil
}

// wants is whether the file at p is part of what the site is built from.
func (s Source) wants(p string) bool {
	if s.Dir == "" {
		return strings.HasPrefix(p, "docs/") || p == "CHANGELOG.md"
	}
	return strings.HasPrefix(p, s.Dir+"/")
}

// SyncState is what a sync leaves behind, and what the console shows.
type SyncState struct {
	Source  Source    `json:"source"`
	Commit  string    `json:"commit,omitempty"`  // the commit the live site was built from
	Synced  time.Time `json:"synced,omitempty"`  // when that build went live
	Checked time.Time `json:"checked,omitempty"` // when the branch was last looked at
	Err     string    `json:"err,omitempty"`     // why the last attempt did not go live
	// Files is the tree the live site was built from: each path's git blob name.
	Files   map[string]string `json:"files,omitempty"`
	Updates []Update          `json:"updates,omitempty"` // newest first
	// Skipped is what the folder holds that a site cannot carry (a README
	// beside the pages), left out of the live site and named here.
	Skipped []string `json:"skipped,omitempty"`
}

// Syncer keeps one domain's copy of the branch it follows: a working copy of
// the files the site is built from, and the state of the last sync.
type Syncer struct {
	Dir    string // this domain's own folder
	Client *http.Client
	// Where GitHub answers. Empty means GitHub itself; tests set their own.
	Git, API, Raw string
	// MaxFile bounds one file, so a mistaken commit of a disk image cannot fill
	// the server. Zero means 32 MiB.
	MaxFile int64
}

func (s *Syncer) base(v, def string) string {
	if v != "" {
		return strings.TrimSuffix(v, "/")
	}
	return def
}

// State reads the last sync's state; a domain never synced has none.
func (s *Syncer) State() SyncState {
	var st SyncState
	if b, err := os.ReadFile(filepath.Join(s.Dir, "state.json")); err == nil {
		_ = json.Unmarshal(b, &st)
	}
	return st
}

func (s *Syncer) save(st SyncState) error {
	b, err := json.MarshalIndent(st, "", " ")
	if err != nil {
		return err
	}
	tmp := filepath.Join(s.Dir, "state.json.tmp")
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(s.Dir, "state.json"))
}

// Sync looks at the branch and, when it has moved since the live site was
// built (or the last attempt failed), fetches what changed, verifies every
// file against the name git gave it, and hands the working copy to publish.
// The state records a success only once publish has returned; until then the
// live site is the last one that built, and the state says why the new one
// did not. publish may add to the state it is given (what it left out).
func (s *Syncer) Sync(ctx context.Context, src Source, publish func(repo fs.FS, st *SyncState) error) (SyncState, error) {
	if err := src.Valid(); err != nil {
		return SyncState{}, err
	}
	prev := s.State()
	if prev.Source != src {
		// Another source: nothing carried over, the working copy included. Its
		// files are not in the state's list any more, so they would never be
		// removed, and a file deleted from the new source would still build.
		prev = SyncState{}
		if err := os.RemoveAll(filepath.Join(s.Dir, "repo")); err != nil {
			return SyncState{}, err
		}
	}
	if err := os.MkdirAll(filepath.Join(s.Dir, "repo"), 0o750); err != nil {
		return SyncState{}, err
	}
	st := prev
	st.Source = src
	st.Checked = time.Now().UTC()
	fail := func(err error) (SyncState, error) {
		st.Err = err.Error()
		_ = s.save(st)
		return st, err
	}
	head, err := s.head(ctx, src)
	if err != nil {
		return fail(err)
	}
	if head == prev.Commit && prev.Err == "" {
		return st, s.save(st)
	}
	files, err := s.tree(ctx, src, head)
	if err != nil {
		return fail(err)
	}
	root, err := os.OpenRoot(filepath.Join(s.Dir, "repo"))
	if err != nil {
		return fail(err)
	}
	defer root.Close()
	var need []string
	for p, sha := range files {
		if prev.Files[p] != sha || !rootHas(root, p) {
			need = append(need, p)
		}
	}
	if err := s.fetchAll(ctx, root, src, head, need, files); err != nil {
		return fail(err)
	}
	for p := range prev.Files {
		if _, ok := files[p]; !ok {
			_ = root.Remove(filepath.FromSlash(p))
		}
	}
	next := st
	next.Commit, next.Files, next.Err, next.Skipped = head, files, "", nil
	next.Synced = time.Now().UTC()
	if prev.Files != nil && src.Dir == "" {
		next.Updates = append(describe(os.DirFS(filepath.Join(s.Dir, "repo")), prev.Files, files, next.Synced), prev.Updates...)
		if len(next.Updates) > 20 {
			next.Updates = next.Updates[:20]
		}
	}
	if err := publish(os.DirFS(filepath.Join(s.Dir, "repo")), &next); err != nil {
		return fail(err)
	}
	return next, s.save(next)
}

// head reads the branch's commit from the smart-HTTP ref list, which answers
// without spending the API's hourly allowance on a check that mostly finds
// nothing new.
func (s *Syncer) head(ctx context.Context, src Source) (string, error) {
	u := s.base(s.Git, "https://github.com") + "/" + src.Repo + ".git/info/refs?service=git-upload-pack"
	body, err := s.get(ctx, u, 4<<20)
	if err != nil {
		return "", fmt.Errorf("reading %s's branches: %w", src.Repo, err)
	}
	want := []byte(" refs/heads/" + src.Branch)
	for _, line := range bytes.Split(body, []byte("\n")) {
		line, _, _ = bytes.Cut(line, []byte{0}) // the first ref carries the capabilities
		if i := bytes.Index(line, want); i >= 40 && i+len(want) == len(line) {
			if sha := string(line[i-40 : i]); isSHA(sha) {
				return sha, nil
			}
		}
	}
	return "", fmt.Errorf("%s has no branch %q", src.Repo, src.Branch)
}

// tree lists the files at commit that a site is built from, with the name git
// gives each: the SHA-1 of its contents, which fetch checks.
func (s *Syncer) tree(ctx context.Context, src Source, commit string) (map[string]string, error) {
	u := s.base(s.API, "https://api.github.com") + "/repos/" + src.Repo + "/git/trees/" + commit + "?recursive=1"
	body, err := s.get(ctx, u, 64<<20)
	if err != nil {
		return nil, fmt.Errorf("listing %s at %s: %w", src.Repo, commit[:8], err)
	}
	var t struct {
		Truncated bool `json:"truncated"`
		Tree      []struct {
			Path string `json:"path"`
			Type string `json:"type"`
			SHA  string `json:"sha"`
		} `json:"tree"`
	}
	if err := json.Unmarshal(body, &t); err != nil {
		return nil, fmt.Errorf("listing %s: %w", src.Repo, err)
	}
	if t.Truncated {
		return nil, errors.New("the repository is too large to list in one answer")
	}
	files := map[string]string{}
	for _, e := range t.Tree {
		if e.Type == "blob" && src.wants(e.Path) && fs.ValidPath(e.Path) && isSHA(e.SHA) {
			files[e.Path] = e.SHA
		}
	}
	if len(files) == 0 {
		dir := src.Dir
		if dir == "" {
			dir = "docs"
		}
		return nil, fmt.Errorf("the branch has no %s/ folder", dir)
	}
	return files, nil
}

// fetchAll fetches the named files a few at a time. One at a time, the first
// sync of a documentation site is some 270 round trips end to end; all at
// once is a burst the raw host answers with refusals.
func (s *Syncer) fetchAll(ctx context.Context, root *os.Root, src Source, commit string, need []string, files map[string]string) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	work := make(chan string)
	errs := make(chan error, 1)
	var wg sync.WaitGroup
	for range min(6, len(need)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for p := range work {
				if err := s.fetch(ctx, root, src, commit, p, files[p]); err != nil {
					select {
					case errs <- err:
					default:
					}
					cancel()
				}
			}
		}()
	}
	for _, p := range need {
		select {
		case work <- p:
		case <-ctx.Done():
		}
		if ctx.Err() != nil {
			break
		}
	}
	close(work)
	wg.Wait()
	select {
	case err := <-errs:
		return err
	default:
		return ctx.Err()
	}
}

// fetch writes one file into the working copy, once its contents prove to be
// what the tree named. The raw host serves whatever it serves; the name is
// what the commit says.
func (s *Syncer) fetch(ctx context.Context, root *os.Root, src Source, commit, p, sha string) error {
	segs := strings.Split(p, "/")
	for i, seg := range segs {
		segs[i] = url.PathEscape(seg)
	}
	limit := s.MaxFile
	if limit <= 0 {
		limit = 32 << 20
	}
	data, err := s.get(ctx, s.base(s.Raw, "https://raw.githubusercontent.com")+"/"+src.Repo+"/"+commit+"/"+strings.Join(segs, "/"), limit)
	if err != nil {
		return fmt.Errorf("fetching %s: %w", p, err)
	}
	h := sha1.New() //nolint:gosec // see the import
	h.Write([]byte("blob " + strconv.Itoa(len(data)) + "\x00"))
	h.Write(data)
	if got := hex.EncodeToString(h.Sum(nil)); got != sha {
		return fmt.Errorf("%s is not the file the commit names (%s, not %s)", p, got[:8], sha[:8])
	}
	if dir := path.Dir(p); dir != "." {
		if err := root.MkdirAll(filepath.FromSlash(dir), 0o750); err != nil {
			return err
		}
	}
	f, err := root.OpenFile(filepath.FromSlash(p), os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o640)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

func (s *Syncer) get(ctx context.Context, u string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "VayuPress site sync")
	c := s.Client
	if c == nil {
		c = http.DefaultClient
	}
	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("answered %s", resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("larger than %d MiB", limit>>20)
	}
	return data, nil
}

func rootHas(root *os.Root, p string) bool {
	fi, err := root.Stat(filepath.FromSlash(p))
	return err == nil && fi.Mode().IsRegular()
}

func isSHA(s string) bool {
	if len(s) != 40 {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}

// describe says what a sync brought in, for the home page's feed: each guide
// added or changed by its title, and new screenshots as one line. Decisions and
// releases are left out: the feed has them from their own dates.
func describe(repo fs.FS, before, after map[string]string, at time.Time) []Update {
	var out []Update
	shots := 0
	var changed []string
	for p, sha := range after {
		if before[p] == sha {
			continue
		}
		switch {
		case strings.HasPrefix(p, "docs/screenshots/"):
			shots++
		case strings.HasPrefix(p, "docs/adr/"), strings.HasPrefix(p, siteDir+"/"):
		case strings.HasSuffix(p, ".md"):
			changed = append(changed, p)
		}
	}
	sort.Strings(changed)
	for _, p := range changed {
		slug := strings.TrimSuffix(strings.TrimPrefix(p, "docs/"), ".md")
		kind := "Doc"
		note := "Updated"
		if _, was := before[p]; !was {
			note = "New"
		}
		out = append(out, Update{At: at, Kind: kind, Title: guideTitle(firstHeading(repo, p), slug), Note: note, Href: "/docs/" + slug + "/"})
	}
	if shots > 0 {
		out = append(out, Update{At: at, Kind: "Screens", Title: strconv.Itoa(shots) + " screenshot" + map[bool]string{true: "", false: "s"}[shots == 1], Note: "Refreshed", Href: "/docs/"})
	}
	return out
}

// firstHeading is a Markdown file's first "# " line.
func firstHeading(repo fs.FS, p string) string {
	b, err := fs.ReadFile(repo, p)
	if err != nil {
		return ""
	}
	for _, line := range strings.SplitN(string(b), "\n", 60) {
		if t, ok := strings.CutPrefix(strings.TrimSpace(line), "# "); ok {
			return strings.TrimSpace(t)
		}
	}
	return ""
}
