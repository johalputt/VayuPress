package exporter

import (
	"bytes"
	"database/sql"
	"fmt"
	"html/template"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

var stripTagsRe = regexp.MustCompile(`<[^>]*>`)

// Article holds a single VayuPress article.
type Article struct {
	ID        string
	Title     string
	Slug      string
	Content   string
	Tags      string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Options configures an export operation.
type Options struct {
	DBPath   string
	OutDir   string
	BaseURL  string
	PageSize int
	Clean    bool
}

// Pagination holds page navigation data.
type Pagination struct {
	Current int
	Total   int
	Prev    string
	Next    string
}

// Export exports all articles to a static site.
func Export(opts Options) (int, error) {
	if opts.PageSize <= 0 {
		opts.PageSize = 20
	}
	if opts.OutDir == "" {
		opts.OutDir = "./vayu-site"
	}

	articles, err := loadArticles(opts.DBPath)
	if err != nil {
		return 0, fmt.Errorf("load articles: %w", err)
	}

	if opts.Clean {
		if err := os.RemoveAll(opts.OutDir); err != nil {
			return 0, fmt.Errorf("clean output dir: %w", err)
		}
	}

	if err := os.MkdirAll(opts.OutDir, 0755); err != nil {
		return 0, fmt.Errorf("create output dir: %w", err)
	}

	// Write individual article pages.
	articlesDir := filepath.Join(opts.OutDir, "articles")
	for _, a := range articles {
		slugDir := filepath.Join(articlesDir, a.Slug)
		if err := os.MkdirAll(slugDir, 0755); err != nil {
			return 0, fmt.Errorf("create article dir: %w", err)
		}
		if err := writeArticlePage(filepath.Join(slugDir, "index.html"), a); err != nil {
			return 0, fmt.Errorf("write article %s: %w", a.Slug, err)
		}
	}

	// Write paginated index pages.
	totalPages := (len(articles) + opts.PageSize - 1) / opts.PageSize
	if totalPages == 0 {
		totalPages = 1
	}
	for page := 1; page <= totalPages; page++ {
		start := (page - 1) * opts.PageSize
		end := start + opts.PageSize
		if end > len(articles) {
			end = len(articles)
		}
		pageArticles := articles[start:end]

		pag := buildPagination(page, totalPages)
		var outPath string
		if page == 1 {
			outPath = filepath.Join(opts.OutDir, "index.html")
		} else {
			pageDir := filepath.Join(opts.OutDir, "page", fmt.Sprintf("%d", page))
			if err := os.MkdirAll(pageDir, 0755); err != nil {
				return 0, err
			}
			outPath = filepath.Join(pageDir, "index.html")
		}
		if err := writeIndexPage(outPath, pageArticles, pag, "VayuPress"); err != nil {
			return 0, fmt.Errorf("write index page %d: %w", page, err)
		}
	}

	// Write sitemap.xml.
	if err := writeSitemap(filepath.Join(opts.OutDir, "sitemap.xml"), articles, opts.BaseURL); err != nil {
		return 0, err
	}

	// Write feed.xml.
	if err := writeFeed(filepath.Join(opts.OutDir, "feed.xml"), articles, opts.BaseURL, "VayuPress"); err != nil {
		return 0, err
	}

	// Write robots.txt.
	if err := writeRobots(filepath.Join(opts.OutDir, "robots.txt"), opts.BaseURL); err != nil {
		return 0, err
	}

	return len(articles), nil
}

// Count returns the number of articles in the database.
func Count(dbPath string) (int64, error) {
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return 0, err
	}
	defer db.Close()
	var count int64
	if err := db.QueryRow("SELECT COUNT(*) FROM articles").Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}

func loadArticles(dbPath string) ([]Article, error) {
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return nil, err
	}
	defer db.Close()

	rows, err := db.Query(`SELECT id, title, slug, content, tags, created_at, updated_at FROM articles ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var articles []Article
	for rows.Next() {
		var a Article
		var createdAt, updatedAt string
		if err := rows.Scan(&a.ID, &a.Title, &a.Slug, &a.Content, &a.Tags, &createdAt, &updatedAt); err != nil {
			return nil, err
		}
		a.CreatedAt = parseTime(createdAt)
		a.UpdatedAt = parseTime(updatedAt)
		articles = append(articles, a)
	}
	return articles, rows.Err()
}

func parseTime(s string) time.Time {
	formats := []string{
		time.RFC3339,
		"2006-01-02 15:04:05",
		"2006-01-02T15:04:05Z",
		"2006-01-02",
	}
	for _, f := range formats {
		if t, err := time.Parse(f, s); err == nil {
			return t
		}
	}
	return time.Time{}
}

func extractDescription(content string) string {
	plain := stripTagsRe.ReplaceAllString(content, "")
	plain = strings.TrimSpace(plain)
	if len(plain) > 160 {
		plain = plain[:160]
	}
	return plain
}

func writeArticlePage(path string, a Article) error {
	tmpl, err := template.New("article").Parse(articleTemplate)
	if err != nil {
		return err
	}

	data := struct {
		Title       string
		Description string
		Date        string
		Content     template.HTML
	}{
		Title:       a.Title,
		Description: extractDescription(a.Content),
		Date:        a.CreatedAt.Format("January 2, 2006"),
		Content:     template.HTML(a.Content),
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return err
	}
	return os.WriteFile(path, buf.Bytes(), 0644)
}

type indexArticle struct {
	Title string
	Slug  string
	Date  string
	Tags  string
}

func writeIndexPage(path string, articles []Article, pag *Pagination, siteTitle string) error {
	tmpl, err := template.New("index").Parse(indexTemplate)
	if err != nil {
		return err
	}

	var ia []indexArticle
	for _, a := range articles {
		ia = append(ia, indexArticle{
			Title: a.Title,
			Slug:  a.Slug,
			Date:  a.CreatedAt.Format("January 2, 2006"),
			Tags:  a.Tags,
		})
	}

	data := struct {
		SiteTitle  string
		Articles   []indexArticle
		Pagination *Pagination
	}{
		SiteTitle:  siteTitle,
		Articles:   ia,
		Pagination: pag,
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return err
	}
	return os.WriteFile(path, buf.Bytes(), 0644)
}

func buildPagination(page, total int) *Pagination {
	if total <= 1 {
		return nil
	}
	p := &Pagination{Current: page, Total: total}
	if page > 1 {
		if page == 2 {
			p.Prev = "/"
		} else {
			p.Prev = fmt.Sprintf("/page/%d/", page-1)
		}
	}
	if page < total {
		p.Next = fmt.Sprintf("/page/%d/", page+1)
	}
	return p
}

func writeSitemap(path string, articles []Article, baseURL string) error {
	tmpl, err := template.New("sitemap").Parse(sitemapTemplate)
	if err != nil {
		return err
	}

	type sitemapArticle struct {
		Slug      string
		UpdatedAt string
	}
	var sa []sitemapArticle
	for _, a := range articles {
		sa = append(sa, sitemapArticle{
			Slug:      a.Slug,
			UpdatedAt: a.UpdatedAt.Format("2006-01-02"),
		})
	}

	data := struct {
		BaseURL  string
		Articles []sitemapArticle
	}{BaseURL: baseURL, Articles: sa}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return err
	}
	return os.WriteFile(path, buf.Bytes(), 0644)
}

func writeFeed(path string, articles []Article, baseURL, siteTitle string) error {
	tmpl, err := template.New("feed").Parse(feedTemplate)
	if err != nil {
		return err
	}

	type feedArticle struct {
		Title       string
		Slug        string
		PubDate     string
		Description string
	}
	var fa []feedArticle
	for _, a := range articles {
		fa = append(fa, feedArticle{
			Title:       a.Title,
			Slug:        a.Slug,
			PubDate:     a.CreatedAt.Format(time.RFC1123Z),
			Description: extractDescription(a.Content),
		})
	}

	data := struct {
		SiteTitle string
		BaseURL   string
		Articles  []feedArticle
	}{SiteTitle: siteTitle, BaseURL: baseURL, Articles: fa}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return err
	}
	return os.WriteFile(path, buf.Bytes(), 0644)
}

func writeRobots(path, baseURL string) error {
	tmpl, err := template.New("robots").Parse(robotsTemplate)
	if err != nil {
		return err
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, struct{ BaseURL string }{BaseURL: baseURL}); err != nil {
		return err
	}
	return os.WriteFile(path, buf.Bytes(), 0644)
}
