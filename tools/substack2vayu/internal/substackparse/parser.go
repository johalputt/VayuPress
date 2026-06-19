// Package substackparse parses Substack CSV exports into articles.
package substackparse

import (
	"encoding/csv"
	"fmt"
	"io"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"
)

// Article represents a parsed Substack post.
type Article struct {
	ID        string
	Title     string
	Slug      string
	Content   string
	Tags      []string
	CreatedAt time.Time
	IsDraft   bool
}

var reMultiHyphen = regexp.MustCompile(`-+`)

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
	result := reMultiHyphen.ReplaceAllString(b.String(), "-")
	return strings.Trim(result, "-")
}

// extractSlugFromURL extracts the slug from a Substack URL like
// https://author.substack.com/p/my-slug
func extractSlugFromURL(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	for i, p := range parts {
		if p == "p" && i+1 < len(parts) {
			return parts[i+1]
		}
	}
	return ""
}

// ParseCSV parses a Substack posts.csv file and returns articles.
// skipDrafts=true skips rows where draft column is "true".
func ParseCSV(path string, skipDrafts bool) ([]Article, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open csv: %w", err)
	}
	defer f.Close()
	return ParseCSVReader(f, skipDrafts)
}

// ParseCSVReader parses Substack CSV from an io.Reader.
func ParseCSVReader(r io.Reader, skipDrafts bool) ([]Article, error) {
	cr := csv.NewReader(r)
	cr.LazyQuotes = true
	cr.TrimLeadingSpace = true

	header, err := cr.Read()
	if err != nil {
		return nil, fmt.Errorf("read header: %w", err)
	}

	// Build column index map
	colIdx := make(map[string]int)
	for i, h := range header {
		colIdx[strings.TrimSpace(h)] = i
	}

	required := []string{"title", "post_id", "url", "publish_date", "body_html"}
	for _, req := range required {
		if _, ok := colIdx[req]; !ok {
			// some columns may be optional
			_ = req
		}
	}

	var articles []Article
	for {
		row, err := cr.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read row: %w", err)
		}

		get := func(col string) string {
			if i, ok := colIdx[col]; ok && i < len(row) {
				return strings.TrimSpace(row[i])
			}
			return ""
		}

		isDraft := strings.EqualFold(get("draft"), "true")
		if skipDrafts && isDraft {
			continue
		}

		title := get("title")
		if title == "" {
			continue
		}

		postID := get("post_id")
		rawURL := get("url")
		slug := extractSlugFromURL(rawURL)
		if slug == "" {
			slug = Slugify(title)
		}

		publishDate := get("publish_date")
		var createdAt time.Time
		// Try RFC3339 first, then "2006-01-02 15:04:05"
		if t, err := time.Parse(time.RFC3339, publishDate); err == nil {
			createdAt = t
		} else if t, err := time.Parse("2006-01-02 15:04:05", publishDate); err == nil {
			createdAt = t
		} else if t, err := time.Parse("2006-01-02", publishDate); err == nil {
			createdAt = t
		} else {
			createdAt = time.Now()
		}

		content := get("body_html")

		articles = append(articles, Article{
			ID:        postID,
			Title:     title,
			Slug:      slug,
			Content:   content,
			Tags:      nil,
			CreatedAt: createdAt,
			IsDraft:   isDraft,
		})
	}
	return articles, nil
}
