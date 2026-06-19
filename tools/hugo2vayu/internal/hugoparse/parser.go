// Package hugoparse parses Hugo Markdown files with YAML or TOML frontmatter into Documents.
package hugoparse

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

// Document represents a parsed Hugo Markdown file ready for insertion into VayuPress.
type Document struct {
	Title      string
	Slug       string
	Tags       []string
	Date       time.Time
	Draft      bool
	HTML       string
	SourcePath string
}

type yamlFrontmatter struct {
	Title       string   `yaml:"title"`
	Slug        string   `yaml:"slug"`
	Date        string   `yaml:"date"`
	Tags        []string `yaml:"tags"`
	Categories  []string `yaml:"categories"`
	Draft       bool     `yaml:"draft"`
	Description string   `yaml:"description"`
}

// Parse reads a Hugo Markdown file at path and returns a Document.
func Parse(path string) (*Document, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}

	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("stat %s: %w", path, err)
	}
	mtime := info.ModTime()

	var (
		title, slug, dateStr string
		draft                bool
		tags, categories     []string
		body                 []byte
	)

	strData := string(data)

	if strings.HasPrefix(strData, "---") {
		// YAML frontmatter
		rest := strData[3:]
		end := strings.Index(rest, "\n---")
		if end == -1 {
			body = data
		} else {
			block := rest[:end]
			body = []byte(rest[end+4:])
			if len(body) > 0 && body[0] == '\n' {
				body = body[1:]
			}
			var fm yamlFrontmatter
			if err := yaml.Unmarshal([]byte(block), &fm); err != nil {
				return nil, fmt.Errorf("yaml parse %s: %w", path, err)
			}
			title = fm.Title
			slug = fm.Slug
			dateStr = fm.Date
			draft = fm.Draft
			tags = fm.Tags
			categories = fm.Categories
		}
	} else if strings.HasPrefix(strData, "+++") {
		// TOML frontmatter
		rest := strData[3:]
		end := strings.Index(rest, "\n+++")
		if end == -1 {
			body = data
		} else {
			block := rest[:end]
			body = []byte(rest[end+4:])
			if len(body) > 0 && body[0] == '\n' {
				body = body[1:]
			}
			title, slug, dateStr, _, draft, tags, categories = parseTomlFrontmatter([]byte(block))
		}
	} else {
		body = data
	}

	// Merge categories into tags (deduplicated)
	tags = mergeDeduplicate(tags, categories)

	// Slug fallback
	if slug == "" {
		base := filepath.Base(path)
		base = strings.TrimSuffix(base, filepath.Ext(base))
		// Strip date prefix YYYY-MM-DD-
		re := regexp.MustCompile(`^\d{4}-\d{2}-\d{2}-`)
		base = re.ReplaceAllString(base, "")
		slug = slugify(base)
	}

	// Title fallback: first H1 in body, then slug
	if title == "" {
		title = extractH1(body)
	}
	if title == "" {
		title = slug
	}

	// Date parsing
	var date time.Time
	if dateStr != "" {
		date, err = parseDate(dateStr)
		if err != nil {
			date = mtime
		}
	} else {
		date = mtime
	}

	// Render Markdown to HTML
	md := goldmark.New(
		goldmark.WithExtensions(extension.GFM, extension.Table),
		goldmark.WithParserOptions(parser.WithAutoHeadingID()),
		goldmark.WithRendererOptions(html.WithUnsafe()),
	)
	var buf bytes.Buffer
	if err := md.Convert(body, &buf); err != nil {
		return nil, fmt.Errorf("markdown render %s: %w", path, err)
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

// parseTomlFrontmatter parses a minimal subset of TOML used in Hugo frontmatter.
func parseTomlFrontmatter(block []byte) (title, slug, date, description string, draft bool, tags, categories []string) {
	lines := strings.Split(string(block), "\n")

	reString := regexp.MustCompile(`^(\w+)\s*=\s*"(.*)"`)
	reBool := regexp.MustCompile(`^(\w+)\s*=\s*(true|false)`)
	reArray := regexp.MustCompile(`^(\w+)\s*=\s*\[([^\]]*)\]`)
	reArrayItem := regexp.MustCompile(`"([^"]*)"`)

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		if m := reArray.FindStringSubmatch(line); m != nil {
			key := strings.ToLower(m[1])
			items := reArrayItem.FindAllStringSubmatch(m[2], -1)
			var vals []string
			for _, item := range items {
				vals = append(vals, item[1])
			}
			switch key {
			case "tags":
				tags = vals
			case "categories":
				categories = vals
			}
			continue
		}

		if m := reBool.FindStringSubmatch(line); m != nil {
			key := strings.ToLower(m[1])
			if key == "draft" {
				draft = m[2] == "true"
			}
			continue
		}

		if m := reString.FindStringSubmatch(line); m != nil {
			key := strings.ToLower(m[1])
			val := m[2]
			switch key {
			case "title":
				title = val
			case "slug":
				slug = val
			case "date":
				date = val
			case "description":
				description = val
			}
			continue
		}
	}

	return
}

// slugify converts a string to a URL-friendly slug.
func slugify(s string) string {
	s = strings.ToLower(s)
	var b strings.Builder
	prevDash := false
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			prevDash = false
		} else if !prevDash && b.Len() > 0 {
			b.WriteByte('-')
			prevDash = true
		}
	}
	return strings.TrimRight(b.String(), "-")
}

// extractH1 finds the first H1 heading in Markdown body.
func extractH1(body []byte) string {
	re := regexp.MustCompile(`(?m)^#\s+(.+)$`)
	m := re.FindSubmatch(body)
	if m == nil {
		return ""
	}
	return strings.TrimSpace(string(m[1]))
}

// parseDate tries multiple date formats used in Hugo frontmatter.
func parseDate(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	formats := []string{
		time.RFC3339,
		"2006-01-02T15:04:05",
		"2006-01-02 15:04:05",
		"2006-01-02",
	}
	for _, f := range formats {
		t, err := time.Parse(f, s)
		if err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("cannot parse date %q", s)
}

// mergeDeduplicate merges two slices and removes duplicates (case-insensitive key, preserves first occurrence).
func mergeDeduplicate(a, b []string) []string {
	seen := make(map[string]struct{})
	var result []string
	for _, v := range append(a, b...) {
		key := strings.ToLower(v)
		if _, ok := seen[key]; !ok {
			seen[key] = struct{}{}
			result = append(result, v)
		}
	}
	return result
}
