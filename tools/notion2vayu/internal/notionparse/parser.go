// Package notionparse parses Notion HTML export files into articles.
package notionparse

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Article represents a parsed Notion page.
type Article struct {
	Title     string
	Slug      string
	Content   string
	Tags      []string
	CreatedAt time.Time
	FilePath  string
}

var (
	reTitle      = regexp.MustCompile(`(?i)<title[^>]*>(.*?)</title>`)
	reH1         = regexp.MustCompile(`(?i)<h1[^>]*>(.*?)</h1>`)
	reArticle    = regexp.MustCompile(`(?is)<article[^>]*>(.*?)</article>`)
	rePageBody   = regexp.MustCompile(`(?is)<div[^>]*class="[^"]*page-body[^"]*"[^>]*>(.*?)</div>`)
	reUUIDSuffix = regexp.MustCompile(`\s+[a-f0-9]{32}$`)
	reTags       = regexp.MustCompile(`(?is)<table[^>]*class="[^"]*properties[^"]*"[^>]*>(.*?)</table>`)
	reTagRow     = regexp.MustCompile(`(?is)<tr[^>]*>.*?Multi-select.*?<td[^>]*>(.*?)</td>`)
	reTagValue   = regexp.MustCompile(`(?i)<span[^>]*>(.*?)</span>`)
	reHTMLTag    = regexp.MustCompile(`<[^>]+>`)
	reMultiSpace = regexp.MustCompile(`\s+`)
)

// Slugify converts a string to a URL-friendly slug.
func Slugify(s string) string {
	s = strings.ToLower(s)
	s = strings.ReplaceAll(s, "_", "-")
	s = strings.ReplaceAll(s, " ", "-")
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			b.WriteRune(r)
		}
	}
	// collapse multiple hyphens
	result := regexp.MustCompile(`-+`).ReplaceAllString(b.String(), "-")
	return strings.Trim(result, "-")
}

func stripHTML(s string) string {
	s = reHTMLTag.ReplaceAllString(s, "")
	s = reMultiSpace.ReplaceAllString(s, " ")
	return strings.TrimSpace(s)
}

// ParseDir parses all HTML files found recursively in dir.
func ParseDir(dir string) ([]Article, error) {
	var articles []Article
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || !strings.HasSuffix(strings.ToLower(path), ".html") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read %s: %w", path, err)
		}
		a, err := ParseHTML(string(data), path, info.ModTime())
		if err != nil {
			return nil // skip unparseable files
		}
		articles = append(articles, a)
		return nil
	})
	return articles, err
}

// ParseZip extracts a ZIP file to a temp directory and parses it.
func ParseZip(zipPath string) ([]Article, string, error) {
	tmpDir, err := os.MkdirTemp("", "notion2vayu-*")
	if err != nil {
		return nil, "", fmt.Errorf("mktemp: %w", err)
	}
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return nil, tmpDir, fmt.Errorf("open zip: %w", err)
	}
	defer r.Close()
	for _, f := range r.File {
		outPath := filepath.Join(tmpDir, f.Name)
		if f.FileInfo().IsDir() {
			os.MkdirAll(outPath, 0755)
			continue
		}
		os.MkdirAll(filepath.Dir(outPath), 0755)
		rc, err := f.Open()
		if err != nil {
			return nil, tmpDir, err
		}
		out, err := os.Create(outPath)
		if err != nil {
			rc.Close()
			return nil, tmpDir, err
		}
		io.Copy(out, rc)
		out.Close()
		rc.Close()
	}
	articles, err := ParseDir(tmpDir)
	return articles, tmpDir, err
}

// ParseHTML parses a single Notion HTML page.
func ParseHTML(html, filePath string, mtime time.Time) (Article, error) {
	// Extract title
	title := ""
	if m := reTitle.FindStringSubmatch(html); len(m) > 1 {
		title = stripHTML(m[1])
		title = strings.TrimSuffix(title, " | Notion")
		title = strings.TrimSuffix(title, "| Notion")
		title = strings.TrimSpace(title)
	}
	if title == "" {
		if m := reH1.FindStringSubmatch(html); len(m) > 1 {
			title = stripHTML(m[1])
		}
	}
	if title == "" {
		return Article{}, fmt.Errorf("no title found in %s", filePath)
	}

	// Derive slug from filename
	base := filepath.Base(filePath)
	base = strings.TrimSuffix(base, filepath.Ext(base))
	base = reUUIDSuffix.ReplaceAllString(base, "")
	slug := Slugify(base)
	if slug == "" {
		slug = Slugify(title)
	}

	// Extract body
	body := ""
	if m := reArticle.FindStringSubmatch(html); len(m) > 1 {
		body = strings.TrimSpace(m[1])
	} else if m := rePageBody.FindStringSubmatch(html); len(m) > 1 {
		body = strings.TrimSpace(m[1])
	}
	if body == "" {
		body = html // fallback to full HTML
	}

	// Extract tags from properties table
	var tags []string
	if m := reTags.FindStringSubmatch(html); len(m) > 1 {
		tableHTML := m[1]
		if tm := reTagRow.FindStringSubmatch(tableHTML); len(tm) > 1 {
			for _, sv := range reTagValue.FindAllStringSubmatch(tm[1], -1) {
				if len(sv) > 1 {
					t := stripHTML(sv[1])
					if t != "" {
						tags = append(tags, t)
					}
				}
			}
		}
	}

	return Article{
		Title:     title,
		Slug:      slug,
		Content:   body,
		Tags:      tags,
		CreatedAt: mtime,
		FilePath:  filePath,
	}, nil
}
