// Package mdparse parses Markdown files with optional YAML frontmatter into Documents.
//
// TODO: TOML frontmatter is not supported, only YAML (between ---).
package mdparse

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

// Document represents a parsed Markdown file ready for insertion into VayuPress.
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
	Title string   `yaml:"title"`
	Slug  string   `yaml:"slug"`
	Date  string   `yaml:"date"`
	Tags  []string `yaml:"tags"`
	Draft bool     `yaml:"draft"`
}

// Parse reads the Markdown file at path, extracts optional YAML frontmatter,
// converts the body to HTML, and returns a populated Document.
func Parse(path string) (*Document, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %q: %w", path, err)
	}

	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("stat %q: %w", path, err)
	}

	var fm frontmatter
	body := data

	// Detect YAML frontmatter between --- delimiters at the start of the file.
	if bytes.HasPrefix(data, []byte("---")) {
		rest := data[3:]
		// Skip optional newline after opening ---
		if len(rest) > 0 && rest[0] == '\n' {
			rest = rest[1:]
		} else if len(rest) > 1 && rest[0] == '\r' && rest[1] == '\n' {
			rest = rest[2:]
		}
		end := bytes.Index(rest, []byte("\n---"))
		if end != -1 {
			yamlBlock := rest[:end]
			if err := yaml.Unmarshal(yamlBlock, &fm); err != nil {
				return nil, fmt.Errorf("frontmatter yaml %q: %w", path, err)
			}
			// Advance past the closing --- line
			after := rest[end+4:]
			if len(after) > 0 && after[0] == '\n' {
				after = after[1:]
			} else if len(after) > 1 && after[0] == '\r' && after[1] == '\n' {
				after = after[2:]
			}
			body = after
		}
	}

	// Convert Markdown body to HTML using goldmark with GFM extensions.
	md := goldmark.New(
		goldmark.WithExtensions(extension.GFM),
		goldmark.WithParserOptions(
			parser.WithAutoHeadingID(),
		),
		goldmark.WithRendererOptions(
			html.WithUnsafe(),
		),
	)
	var htmlBuf bytes.Buffer
	if err := md.Convert(body, &htmlBuf); err != nil {
		return nil, fmt.Errorf("convert markdown %q: %w", path, err)
	}

	doc := &Document{
		Title:      fm.Title,
		Slug:       fm.Slug,
		Tags:       fm.Tags,
		Draft:      fm.Draft,
		HTML:       htmlBuf.String(),
		SourcePath: path,
	}

	// Fallback: slug derived from filename.
	if doc.Slug == "" {
		base := filepath.Base(path)
		name := strings.TrimSuffix(base, filepath.Ext(base))
		doc.Slug = slugify(name)
	}

	// Fallback: title from first H1 heading in Markdown body.
	if doc.Title == "" {
		doc.Title = extractH1(body)
	}

	// Fallback: title from slug if still empty.
	if doc.Title == "" {
		doc.Title = doc.Slug
	}

	// Parse date from frontmatter or fallback to file mtime.
	if fm.Date != "" {
		t, err := parseDate(fm.Date)
		if err == nil {
			doc.Date = t
		} else {
			doc.Date = info.ModTime()
		}
	} else {
		doc.Date = info.ModTime()
	}

	if doc.Tags == nil {
		doc.Tags = []string{}
	}

	return doc, nil
}

// slugify converts a string to a URL-safe slug.
func slugify(s string) string {
	s = strings.ToLower(s)
	// Replace spaces and underscores with hyphens.
	s = strings.Map(func(r rune) rune {
		if r == ' ' || r == '_' {
			return '-'
		}
		return r
	}, s)
	// Strip non-alphanumeric except hyphens.
	var b strings.Builder
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' {
			b.WriteRune(r)
		}
	}
	// Collapse multiple hyphens.
	re := regexp.MustCompile(`-{2,}`)
	result := re.ReplaceAllString(b.String(), "-")
	return strings.Trim(result, "-")
}

// extractH1 finds the first H1 heading in raw Markdown bytes.
func extractH1(body []byte) string {
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.HasPrefix(line, "# ") {
			return strings.TrimPrefix(line, "# ")
		}
	}
	return ""
}

// parseDate attempts to parse a date string in common formats.
func parseDate(s string) (time.Time, error) {
	formats := []string{
		time.RFC3339,
		"2006-01-02T15:04:05",
		"2006-01-02 15:04:05",
		"2006-01-02",
	}
	for _, f := range formats {
		if t, err := time.Parse(f, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("cannot parse date %q", s)
}
