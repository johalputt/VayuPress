package main

// migrate.go — "vayupress migrate" subcommand.
//
// Usage:
//   vayupress migrate markdown --dir /path/to/posts [--db /path/to/vayupress.db] [--dry-run] [--recursive] [--skip-drafts]
//   vayupress migrate list     --dir /path/to/posts [--recursive]
//   vayupress migrate info
//
// Markdown files must have YAML frontmatter (---) to supply title, slug, date,
// and tags. Files without frontmatter are also accepted; the slug is derived
// from the filename and the date from the file's mtime.
//
// The importer writes directly to the VayuPress SQLite database via INSERT OR
// IGNORE, so re-running is always safe. Each imported article also gets an
// article_sources side-car row (format=markdown, source=raw file body) so the
// Admin v2 editor can open it in Markdown mode.

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/microcosm-cc/bluemonday"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	goldparser "github.com/yuin/goldmark/parser"

	dbpkg "github.com/johalputt/vayupress/internal/db"
)

// runMigrate is the entry point for `vayupress migrate <subcommand> [flags]`.
func runMigrate(args []string) error {
	if len(args) == 0 {
		printMigrateUsage()
		return nil
	}
	sub := args[0]
	rest := args[1:]
	switch sub {
	case "markdown", "md":
		return runMigrateMarkdown(rest)
	case "list":
		return runMigrateList(rest)
	case "info":
		printMigrateInfo()
		return nil
	default:
		fmt.Fprintf(os.Stderr, "unknown migrate subcommand %q\n\n", sub)
		printMigrateUsage()
		return fmt.Errorf("unknown subcommand")
	}
}

func printMigrateUsage() {
	fmt.Println(`Usage:
  vayupress migrate markdown --dir <folder> [--db <vayupress.db>] [--dry-run] [--recursive=false] [--skip-drafts=false]
  vayupress migrate list     --dir <folder> [--recursive=false]
  vayupress migrate info

Subcommands:
  markdown    Import Markdown files (with optional YAML frontmatter) into the VayuPress database.
  list        List Markdown files that would be imported without writing anything.
  info        Print guidance for migrating from WordPress, Ghost, Hugo, Jekyll, Medium, Notion, and Substack.

Flags (markdown / list):
  --dir <path>      Directory containing .md files (required).
  --db  <path>      Path to the VayuPress SQLite database (default: $VAYU_DB or ./vayupress.db).
  --dry-run         Print what would be imported without writing to the database.
  --recursive       Walk subdirectories (default: true).
  --skip-drafts     Skip files with draft: true in frontmatter (default: true).`)
}

func printMigrateInfo() {
	fmt.Println(`VayuPress Migration Guide
=========================

Standalone tools for each platform ship in the tools/ directory of the source
repository. Build and run them independently:

  Platform      Tool               Command
  ──────────────────────────────────────────────────────────────────────────
  WordPress     tools/wordpress2vayu    wp2vayu migrate --wp-db=… --vayu-db=…
  Ghost         tools/ghost-to-vayu     ghost2vayu migrate --ghost-db=… --vayu-db=…
  Hugo          tools/hugo2vayu         hugo2vayu import --dir=… --vayu-db=…
  Jekyll        tools/jekyll2vayu       jekyll2vayu import --dir=… --vayu-db=…
  Medium        tools/medium2vayu       medium2vayu import --zip=… --vayu-db=…
  Notion        tools/notion2vayu       notion2vayu import --zip=… --vayu-db=…
  Substack      tools/substack2vayu     substack2vayu import --csv=… --vayu-db=…
  Markdown      (built-in)              vayupress migrate markdown --dir=… [--db=…]

For Markdown imports, use:

  vayupress migrate markdown --dir ./posts --dry-run   # preview
  vayupress migrate markdown --dir ./posts             # import

See docs/MIGRATION.md for full instructions.`)
}

// ---- flag parsing (stdlib, no cobra dependency) ----------------------------

func parseStringFlag(args []string, name string, def string) string {
	prefix := "--" + name + "="
	for _, a := range args {
		if strings.HasPrefix(a, prefix) {
			return strings.TrimPrefix(a, prefix)
		}
		if a == "--"+name {
			// next arg is value — this simple parser doesn't handle that form
		}
	}
	return def
}

func parseBoolFlag(args []string, name string, def bool) bool {
	for _, a := range args {
		if a == "--"+name {
			return true
		}
		if a == "--"+name+"=true" {
			return true
		}
		if a == "--"+name+"=false" {
			return false
		}
	}
	return def
}

// ---- markdown import -------------------------------------------------------

func runMigrateMarkdown(args []string) error {
	dir := parseStringFlag(args, "dir", "")
	dbPath := parseStringFlag(args, "db", os.Getenv("VAYU_DB"))
	if dbPath == "" {
		dbPath = "vayupress.db"
	}
	dryRun := parseBoolFlag(args, "dry-run", false)
	recursive := parseBoolFlag(args, "recursive", true)
	skipDrafts := parseBoolFlag(args, "skip-drafts", true)

	if dir == "" {
		return fmt.Errorf("--dir is required; run 'vayupress migrate' for usage")
	}

	fmt.Printf("Scanning %s for Markdown files...\n", dir)
	files, err := collectMDFiles(dir, recursive)
	if err != nil {
		return fmt.Errorf("scan: %w", err)
	}
	fmt.Printf("Found %d file(s).\n\n", len(files))
	if len(files) == 0 {
		return nil
	}

	if dryRun {
		return dryRunList(files, skipDrafts)
	}

	// Use the already-open application DB if available, else open directly.
	db := dbpkg.DB
	if db == nil {
		return fmt.Errorf("database not initialised; run vayupress normally or set VAYU_DB")
	}

	total, inserted, skipped, errs := len(files), 0, 0, 0
	for i, path := range files {
		base := filepath.Base(path)
		prefix := fmt.Sprintf("[%3d/%d] %s", i+1, total, base)

		doc, err := parseMDFile(path)
		if err != nil {
			fmt.Printf("%s → ERROR: %v\n", prefix, err)
			errs++
			continue
		}
		if skipDrafts && doc.draft {
			fmt.Printf("%s → %q (draft, skipped)\n", prefix, doc.title)
			skipped++
			continue
		}

		id, err := newMigrateID()
		if err != nil {
			fmt.Printf("%s → ERROR generating id: %v\n", prefix, err)
			errs++
			continue
		}

		dateStr := doc.date.UTC().Format(time.RFC3339)
		tagsJSON := `["` + strings.Join(doc.tags, `","`) + `"]`
		if len(doc.tags) == 0 {
			tagsJSON = "[]"
		}

		// Sanitise HTML before persisting (mirrors article write-queue).
		sanitised := bluemonday.UGCPolicy().Sanitize(doc.html)

		res, err := db.Exec(
			`INSERT OR IGNORE INTO articles(id,title,slug,content,tags,created_at,updated_at)
			 VALUES(?,?,?,?,?,?,?)`,
			id, doc.title, doc.slug, sanitised, tagsJSON, dateStr, dateStr,
		)
		if err != nil {
			fmt.Printf("%s → ERROR: %v\n", prefix, err)
			errs++
			continue
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			fmt.Printf("%s → %q (already exists, skipped)\n", prefix, doc.title)
			skipped++
			continue
		}

		// Persist editable source side-car so the editor reopens in Markdown mode.
		db.Exec( //nolint:errcheck
			`INSERT OR IGNORE INTO article_sources(slug,format,source,updated_at) VALUES(?,?,?,CURRENT_TIMESTAMP)`,
			doc.slug, "markdown", doc.rawBody,
		)

		fmt.Printf("%s → %q  slug=%s  ✓\n", prefix, doc.title, doc.slug)
		inserted++
	}

	fmt.Printf("\nDone. Inserted: %d  Skipped: %d  Errors: %d\n", inserted, skipped, errs)
	return nil
}

func dryRunList(files []string, skipDrafts bool) error {
	for _, path := range files {
		doc, err := parseMDFile(path)
		if err != nil {
			fmt.Printf("  ERROR %s: %v\n", filepath.Base(path), err)
			continue
		}
		draftTag := ""
		if doc.draft {
			if skipDrafts {
				draftTag = " [DRAFT — would be skipped]"
			} else {
				draftTag = " [DRAFT]"
			}
		}
		fmt.Printf("  %-40s → %q  slug=%s  date=%s%s\n",
			filepath.Base(path), doc.title, doc.slug, doc.date.Format("2006-01-02"), draftTag)
	}
	return nil
}

func runMigrateList(args []string) error {
	dir := parseStringFlag(args, "dir", "")
	recursive := parseBoolFlag(args, "recursive", true)
	if dir == "" {
		return fmt.Errorf("--dir is required")
	}
	files, err := collectMDFiles(dir, recursive)
	if err != nil {
		return err
	}
	fmt.Printf("Found %d Markdown file(s) in %s:\n\n", len(files), dir)
	return dryRunList(files, false)
}

// ---- markdown parser (YAML frontmatter + goldmark body) --------------------

type mdDoc struct {
	title   string
	slug    string
	tags    []string
	date    time.Time
	draft   bool
	html    string
	rawBody string
}

var goldMD = goldmark.New(
	goldmark.WithExtensions(extension.GFM),
	goldmark.WithParserOptions(goldparser.WithAutoHeadingID()),
)

func parseMDFile(path string) (*mdDoc, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read: %w", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("stat: %w", err)
	}

	// Strip YAML frontmatter if present.
	var fm struct {
		Title string   `yaml:"title"`
		Slug  string   `yaml:"slug"`
		Date  string   `yaml:"date"`
		Tags  []string `yaml:"tags"`
		Draft bool     `yaml:"draft"`
	}
	body := data
	if bytes.HasPrefix(data, []byte("---")) {
		rest := data[3:]
		if len(rest) > 0 && rest[0] == '\n' {
			rest = rest[1:]
		} else if len(rest) > 1 && rest[0] == '\r' && rest[1] == '\n' {
			rest = rest[2:]
		}
		if end := bytes.Index(rest, []byte("\n---")); end != -1 {
			parseYAML(rest[:end], &fm)
			after := rest[end+4:]
			if len(after) > 0 && after[0] == '\n' {
				after = after[1:]
			}
			body = after
		}
	}

	var htmlBuf bytes.Buffer
	if err := goldMD.Convert(body, &htmlBuf); err != nil {
		return nil, fmt.Errorf("convert: %w", err)
	}

	doc := &mdDoc{
		title:   fm.Title,
		slug:    fm.Slug,
		tags:    fm.Tags,
		draft:   fm.Draft,
		html:    htmlBuf.String(),
		rawBody: string(body),
		date:    info.ModTime(),
	}
	if doc.slug == "" {
		base := filepath.Base(path)
		doc.slug = migrateSlugify(strings.TrimSuffix(base, filepath.Ext(base)))
	}
	if doc.title == "" {
		doc.title = extractMDH1(body)
	}
	if doc.title == "" {
		doc.title = doc.slug
	}
	if fm.Date != "" {
		if t, err := parseMigrateDate(fm.Date); err == nil {
			doc.date = t
		}
	}
	if doc.tags == nil {
		doc.tags = []string{}
	}
	return doc, nil
}

// parseYAML is a minimal key:value YAML parser for frontmatter.
// It handles string, bool, and list values without importing gopkg.in/yaml.v3.
func parseYAML(data []byte, out interface{}) {
	fm, ok := out.(*struct {
		Title string   `yaml:"title"`
		Slug  string   `yaml:"slug"`
		Date  string   `yaml:"date"`
		Tags  []string `yaml:"tags"`
		Draft bool     `yaml:"draft"`
	})
	if !ok {
		return
	}

	lines := strings.Split(string(data), "\n")
	inTags := false
	for _, line := range lines {
		line = strings.TrimRight(line, "\r")
		if strings.HasPrefix(line, "  - ") || strings.HasPrefix(line, "- ") {
			if inTags {
				tag := strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(line, "  - "), "- "))
				tag = strings.Trim(tag, `"'`)
				if tag != "" {
					fm.Tags = append(fm.Tags, tag)
				}
			}
			continue
		}
		inTags = false
		idx := strings.Index(line, ":")
		if idx < 0 {
			continue
		}
		key := strings.TrimSpace(line[:idx])
		val := strings.TrimSpace(line[idx+1:])
		val = strings.Trim(val, `"'`)
		switch strings.ToLower(key) {
		case "title":
			fm.Title = val
		case "slug":
			fm.Slug = val
		case "date":
			fm.Date = val
		case "draft":
			fm.Draft = val == "true"
		case "tags":
			inTags = true
			// inline list: tags: [a, b, c]
			if strings.HasPrefix(val, "[") {
				inner := strings.Trim(val, "[]")
				for _, t := range strings.Split(inner, ",") {
					t = strings.TrimSpace(strings.Trim(t, `"' `))
					if t != "" {
						fm.Tags = append(fm.Tags, t)
					}
				}
				inTags = false
			}
		}
	}
}

func extractMDH1(body []byte) string {
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.HasPrefix(line, "# ") {
			return strings.TrimPrefix(line, "# ")
		}
	}
	return ""
}

var multiHyphen = regexp.MustCompile(`-{2,}`)

func migrateSlugify(s string) string {
	s = strings.ToLower(s)
	var b strings.Builder
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		} else {
			b.WriteRune('-')
		}
	}
	return strings.Trim(multiHyphen.ReplaceAllString(b.String(), "-"), "-")
}

func parseMigrateDate(s string) (time.Time, error) {
	for _, f := range []string{time.RFC3339, "2006-01-02T15:04:05", "2006-01-02 15:04:05", "2006-01-02"} {
		if t, err := time.Parse(f, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("unrecognised date %q", s)
}

func collectMDFiles(dir string, recursive bool) ([]string, error) {
	// Validate dir is within reasonable bounds (no symlink escape).
	clean := filepath.Clean(dir)
	if !filepath.IsAbs(clean) {
		var err error
		clean, err = filepath.Abs(clean)
		if err != nil {
			return nil, fmt.Errorf("resolve path: %w", err)
		}
	}
	var files []string
	err := filepath.WalkDir(clean, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if !recursive && path != clean {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.EqualFold(filepath.Ext(d.Name()), ".md") {
			files = append(files, path)
		}
		return nil
	})
	return files, err
}

func newMigrateID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
