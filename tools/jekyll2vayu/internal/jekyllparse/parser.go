// Package jekyllparse parses Jekyll Markdown files into Documents.
package jekyllparse

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer/html"
	"gopkg.in/yaml.v3"
)

// Document represents a parsed Jekyll post ready for insertion into VayuPress.
type Document struct {
	Title      string
	Slug       string
	Tags       []string
	Date       time.Time
	Draft      bool
	HTML       string
	SourcePath string
}

type frontmatter struct {
	Title      string   `yaml:"title"`
	Date       string   `yaml:"date"`
	Tags       []string `yaml:"tags"`
	Categories []string `yaml:"categories"`
	Layout     string   `yaml:"layout"`
	Published  *bool    `yaml:"published"` // nil means not set (default true)
}

// postFilenameRe matches Jekyll post filenames: YYYY-MM-DD-slug.md
var postFilenameRe = regexp.MustCompile(`^(\d{4}-\d{2}-\d{2})-(.+)$`)

// Parse reads the Jekyll Markdown file at path and returns a Document.
func Parse(path string) (*Document, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %q: %w", path, err)
	}

	// Extract YAML frontmatter.
	fm, body, err := splitFrontmatter(data)
	if err != nil {
		return nil, fmt.Errorf("frontmatter %q: %w", path, err)
	}

	// Determine draft status.
	draft := false
	if fm.Published != nil && !*fm.Published {
		draft = true
	}

	// Determine date and slug from filename.
	base := filepath.Base(path)
	ext := filepath.Ext(base)
	nameNoExt := strings.TrimSuffix(base, ext)

	var fileDate time.Time
	var slug string

	if m := postFilenameRe.FindStringSubmatch(nameNoExt); m != nil {
		fileDate, _ = time.Parse("2006-01-02", m[1])
		slug = slugify(m[2])
	} else {
		slug = slugify(nameNoExt)
	}

	// Override date from frontmatter if present.
	date := fileDate
	if fm.Date != "" {
		if d, err := parseDate(fm.Date); err == nil {
			date = d
		}
	}

	// Merge tags and categories (deduplicated).
	tags := mergeTags(fm.Tags, fm.Categories)

	// Title: frontmatter, then first H1 in body, then slug.
	title := fm.Title
	if title == "" {
		title = extractH1(body)
	}
	if title == "" {
		title = slug
	}

	// Convert Markdown body to HTML.
	md := goldmark.New(
		goldmark.WithExtensions(extension.GFM),
		goldmark.WithParserOptions(parser.WithAutoHeadingID()),
		goldmark.WithRendererOptions(html.WithUnsafe()),
	)
	var buf bytes.Buffer
	if err := md.Convert(body, &buf); err != nil {
		return nil, fmt.Errorf("markdown convert %q: %w", path, err)
	}

	return &Document{
		Title:      title,
		Slug:       slug,
		Tags:       tags,
		Date:       date,
		Draft:      draft,
		HTML:       buf.String(),
		SourcePath: path,
	}, nil
}

// splitFrontmatter splits YAML frontmatter from body. Frontmatter is between
// leading "---" delimiters.
func splitFrontmatter(data []byte) (frontmatter, []byte, error) {
	var fm frontmatter
	data = bytes.TrimLeft(data, "\r\n")
	if !bytes.HasPrefix(data, []byte("---")) {
		return fm, data, nil
	}
	// Find closing ---
	rest := data[3:]
	// Skip optional newline after opening ---
	if len(rest) > 0 && rest[0] == '\n' {
		rest = rest[1:]
	} else if len(rest) > 1 && rest[0] == '\r' && rest[1] == '\n' {
		rest = rest[2:]
	}
	idx := bytes.Index(rest, []byte("\n---"))
	if idx < 0 {
		return fm, data, fmt.Errorf("no closing --- found")
	}
	yamlBytes := rest[:idx]
	body := rest[idx+4:] // skip \n---
	// Skip optional newline after closing ---
	if len(body) > 0 && body[0] == '\n' {
		body = body[1:]
	} else if len(body) > 1 && body[0] == '\r' && body[1] == '\n' {
		body = body[2:]
	}

	if err := yaml.Unmarshal(yamlBytes, &fm); err != nil {
		return fm, body, fmt.Errorf("yaml unmarshal: %w", err)
	}
	return fm, body, nil
}

// slugify converts a string into a URL-friendly slug.
func slugify(s string) string {
	s = strings.ToLower(s)
	// Replace spaces and underscores with hyphens.
	var b strings.Builder
	for _, r := range s {
		if r == ' ' || r == '_' {
			b.WriteRune('-')
		} else if r == '-' || unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
		// Strip other non-alphanumeric characters.
	}
	// Collapse multiple hyphens.
	result := regexp.MustCompile(`-{2,}`).ReplaceAllString(b.String(), "-")
	result = strings.Trim(result, "-")
	return result
}

// extractH1 returns the text of the first H1 heading in body, or "".
func extractH1(body []byte) string {
	lines := bytes.Split(body, []byte("\n"))
	for _, line := range lines {
		line = bytes.TrimSpace(line)
		if bytes.HasPrefix(line, []byte("# ")) {
			return string(bytes.TrimSpace(line[2:]))
		}
	}
	return ""
}

// parseDate tries common date formats.
func parseDate(s string) (time.Time, error) {
	formats := []string{
		"2006-01-02 15:04:05 -0700",
		"2006-01-02 15:04:05",
		"2006-01-02T15:04:05Z07:00",
		"2006-01-02",
	}
	s = strings.TrimSpace(s)
	for _, f := range formats {
		if t, err := time.Parse(f, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("unrecognized date format: %q", s)
}

// mergeTags merges tags and categories, deduplicating.
func mergeTags(tags, categories []string) []string {
	seen := make(map[string]bool)
	var result []string
	for _, t := range tags {
		if !seen[t] {
			seen[t] = true
			result = append(result, t)
		}
	}
	for _, c := range categories {
		if !seen[c] {
			seen[c] = true
			result = append(result, c)
		}
	}
	return result
}
