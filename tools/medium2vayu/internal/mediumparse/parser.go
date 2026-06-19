// Package mediumparse parses Medium HTML export archives into articles.
// Medium exports a ZIP file containing individual HTML files, one per post.
// Each file has the post's title in <h1>, publication date in a <time> element,
// tags in <a rel="tag"> links, and the article body as the main <section> content.
package mediumparse

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

// Article represents a parsed Medium post.
type Article struct {
	ID        string
	Title     string
	Slug      string
	Content   string
	Tags      []string
	CreatedAt time.Time
	IsDraft   bool
}

var (
	reTitleTag    = regexp.MustCompile(`(?i)<title[^>]*>(.*?)</title>`)
	reH1          = regexp.MustCompile(`(?i)<h1[^>]*>(.*?)</h1>`)
	reTimeElem    = regexp.MustCompile(`(?i)<time[^>]*datetime="([^"]+)"`)
	reRelTag      = regexp.MustCompile(`(?i)<a[^>]+rel="tag"[^>]*>(.*?)</a>`)
	reHTMLComment = regexp.MustCompile(`(?s)<!--.*?-->`)
	reBlankLines  = regexp.MustCompile(`\n{3,}`)
	reTagStrip    = regexp.MustCompile(`<[^>]+>`)
	reMultiHyphen = regexp.MustCompile(`-+`)

	// section holds the main article body in Medium HTML exports.
	reSectionBody = regexp.MustCompile(`(?is)<section[^>]*>(.*?)</section>`)
	// article element wraps the full post in newer Medium exports.
	reArticleBody = regexp.MustCompile(`(?is)<article[^>]*>(.*?)</article>`)
	// div.section-content is used in some Medium exports.
	reDivBody = regexp.MustCompile(`(?is)<div[^>]+class="[^"]*section-content[^"]*"[^>]*>(.*?)</div>`)
)

var dateLayouts = []string{
	time.RFC3339,
	"2006-01-02T15:04:05.000Z",
	"2006-01-02T15:04:05Z",
	"2006-01-02",
}

// ParseZIP parses all HTML files from a Medium export ZIP archive.
func ParseZIP(path string, skipDrafts bool) ([]Article, error) {
	r, err := zip.OpenReader(path)
	if err != nil {
		return nil, fmt.Errorf("open zip: %w", err)
	}
	defer r.Close()

	var articles []Article
	for _, f := range r.File {
		if filepath.Ext(f.Name) != ".html" {
			continue
		}
		// Skip index/about pages — Medium posts have dates in their filenames.
		base := filepath.Base(f.Name)
		if base == "index.html" || base == "about.html" {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, fmt.Errorf("open %s: %w", f.Name, err)
		}
		data, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", f.Name, err)
		}

		a, err := ParseHTML(string(data), base)
		if err != nil {
			continue // skip malformed files
		}
		if skipDrafts && a.IsDraft {
			continue
		}
		articles = append(articles, a)
	}
	return articles, nil
}

// ParseDir parses all HTML files from a Medium export directory.
func ParseDir(dir string, skipDrafts bool) ([]Article, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read dir: %w", err)
	}

	var articles []Article
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".html" {
			continue
		}
		base := e.Name()
		if base == "index.html" || base == "about.html" {
			continue
		}

		data, err := os.ReadFile(filepath.Join(dir, base))
		if err != nil {
			continue
		}
		a, err := ParseHTML(string(data), base)
		if err != nil {
			continue
		}
		if skipDrafts && a.IsDraft {
			continue
		}
		articles = append(articles, a)
	}
	return articles, nil
}

// ParseHTML parses a single Medium HTML export file.
// filename is used to derive the slug when metadata is absent.
func ParseHTML(html, filename string) (Article, error) {
	a := Article{}

	// --- Title ---
	if m := reH1.FindStringSubmatch(html); m != nil {
		a.Title = stripTags(m[1])
	}
	if a.Title == "" {
		if m := reTitleTag.FindStringSubmatch(html); m != nil {
			a.Title = stripTags(m[1])
		}
	}
	if a.Title == "" {
		a.Title = filenameToTitle(filename)
	}

	// --- Slug ---
	a.Slug = filenameToSlug(filename)
	if a.Slug == "" {
		a.Slug = Slugify(a.Title)
	}

	// --- Date ---
	if m := reTimeElem.FindStringSubmatch(html); m != nil {
		a.CreatedAt = parseDate(m[1])
	}
	if a.CreatedAt.IsZero() {
		a.CreatedAt = time.Now()
	}

	// --- Tags ---
	for _, m := range reRelTag.FindAllStringSubmatch(html, -1) {
		tag := strings.TrimSpace(stripTags(m[1]))
		if tag != "" {
			a.Tags = append(a.Tags, Slugify(tag))
		}
	}

	// --- Draft detection ---
	a.IsDraft = strings.Contains(strings.ToLower(html), "class=\"draft\"") ||
		strings.Contains(strings.ToLower(filename), "draft")

	// --- Content ---
	// Try article > section > div.section-content (Medium export format evolution).
	content := ""
	if m := reArticleBody.FindStringSubmatch(html); m != nil {
		content = m[1]
	} else if m := reSectionBody.FindStringSubmatch(html); m != nil {
		content = m[1]
	} else if m := reDivBody.FindStringSubmatch(html); m != nil {
		content = m[1]
	}

	if content == "" {
		return Article{}, fmt.Errorf("no article body found in %s", filename)
	}

	// Strip HTML comments and collapse blank lines.
	content = reHTMLComment.ReplaceAllString(content, "")
	content = reBlankLines.ReplaceAllString(content, "\n\n")
	content = strings.TrimSpace(content)
	a.Content = content

	return a, nil
}

// Slugify converts text to a URL-safe slug.
func Slugify(s string) string {
	s = strings.ToLower(s)
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else {
			b.WriteRune('-')
		}
	}
	result := reMultiHyphen.ReplaceAllString(b.String(), "-")
	return strings.Trim(result, "-")
}

// filenameToSlug converts a Medium export filename like
// "2024-01-15_my-post-title-abc123def456.html" to "my-post-title".
func filenameToSlug(filename string) string {
	s := strings.TrimSuffix(filename, ".html")
	// Strip leading date prefix (YYYY-MM-DD_).
	if len(s) > 11 && s[4] == '-' && s[7] == '-' && s[10] == '_' {
		s = s[11:]
	}
	// Strip trailing hex ID (Medium appends a hash at the end).
	if idx := strings.LastIndex(s, "_"); idx != -1 {
		s = s[:idx]
	}
	return Slugify(s)
}

func filenameToTitle(filename string) string {
	s := strings.TrimSuffix(filename, ".html")
	if len(s) > 11 && s[4] == '-' && s[7] == '-' && s[10] == '_' {
		s = s[11:]
	}
	if idx := strings.LastIndex(s, "_"); idx != -1 {
		s = s[:idx]
	}
	s = strings.ReplaceAll(s, "-", " ")
	if len(s) > 0 {
		s = strings.ToUpper(s[:1]) + s[1:]
	}
	return s
}

func parseDate(s string) time.Time {
	for _, layout := range dateLayouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}

func stripTags(s string) string {
	return strings.TrimSpace(reTagStrip.ReplaceAllString(s, ""))
}
