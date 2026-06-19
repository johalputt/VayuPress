// Package validate checks VayuPress SQLite databases for content integrity issues.
// It reports: empty titles/slugs, invalid slug characters, duplicate slugs,
// empty content, malformed dates, and oversized articles.
package validate

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// Severity of a validation issue.
type Severity string

const (
	SeverityError   Severity = "ERROR"
	SeverityWarning Severity = "WARNING"
	SeverityInfo    Severity = "INFO"
)

// Issue is a single validation finding.
type Issue struct {
	Severity  Severity
	ArticleID string
	Slug      string
	Rule      string
	Detail    string
}

// Report summarises a database validation run.
type Report struct {
	Total    int
	Issues   []Issue
	Errors   int
	Warnings int
	Infos    int
}

// Summary returns a human-readable one-line summary.
func (r *Report) Summary() string {
	if r.Errors == 0 && r.Warnings == 0 {
		return fmt.Sprintf("✓ %d articles — no issues found", r.Total)
	}
	return fmt.Sprintf("%d articles — %d errors, %d warnings, %d infos",
		r.Total, r.Errors, r.Warnings, r.Infos)
}

var (
	// validSlug allows lowercase alphanumeric and hyphens only.
	validSlugRe    = regexp.MustCompile(`^[a-z0-9][a-z0-9\-]*[a-z0-9]$|^[a-z0-9]$`)
	maxContentSize = 5 * 1024 * 1024 // 5 MB — flag articles that may cause rendering issues
)

// Validate opens the VayuPress SQLite database at path and runs all checks.
func Validate(ctx context.Context, path string) (*Report, error) {
	dsn := "file:" + path + "?_busy_timeout=5000"
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}
	defer db.Close()

	rows, err := db.QueryContext(ctx,
		`SELECT id, title, slug, content, tags, created_at, updated_at FROM articles ORDER BY created_at`)
	if err != nil {
		return nil, fmt.Errorf("query articles: %w", err)
	}
	defer rows.Close()

	report := &Report{}
	slugSeen := map[string]string{} // slug → first article id

	for rows.Next() {
		var id, title, slug, content, tags, createdRaw, updatedRaw string
		if err := rows.Scan(&id, &title, &slug, &content, &tags, &createdRaw, &updatedRaw); err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}
		report.Total++

		add := func(sev Severity, rule, detail string) {
			report.Issues = append(report.Issues, Issue{
				Severity: sev, ArticleID: id, Slug: slug, Rule: rule, Detail: detail,
			})
			switch sev {
			case SeverityError:
				report.Errors++
			case SeverityWarning:
				report.Warnings++
			case SeverityInfo:
				report.Infos++
			}
		}

		// --- Title ---
		if strings.TrimSpace(title) == "" {
			add(SeverityError, "empty-title", "article has no title")
		}

		// --- Slug ---
		if strings.TrimSpace(slug) == "" {
			add(SeverityError, "empty-slug", "article has no slug")
		} else if !validSlugRe.MatchString(slug) {
			add(SeverityError, "invalid-slug", fmt.Sprintf("slug %q contains invalid characters (use lowercase, numbers, hyphens only)", slug))
		}
		if prev, seen := slugSeen[slug]; seen {
			add(SeverityError, "duplicate-slug", fmt.Sprintf("slug %q also used by article %s", slug, prev))
		} else {
			slugSeen[slug] = id
		}

		// --- Content ---
		trimmed := strings.TrimSpace(content)
		if trimmed == "" {
			add(SeverityError, "empty-content", "article has no content")
		} else if len(content) > maxContentSize {
			add(SeverityWarning, "oversized-content",
				fmt.Sprintf("content is %.1f MB (>5 MB may cause rendering issues)", float64(len(content))/(1024*1024)))
		}

		// --- Dates ---
		if t, err := parseDate(createdRaw); err != nil {
			add(SeverityError, "invalid-created-at", fmt.Sprintf("cannot parse created_at %q: %v", createdRaw, err))
		} else if t.Year() < 2000 {
			add(SeverityWarning, "suspicious-date", fmt.Sprintf("created_at %q is before year 2000 — likely a bad date that SQLite converted to zero", createdRaw))
		}
		if _, err := parseDate(updatedRaw); err != nil {
			add(SeverityError, "invalid-updated-at", fmt.Sprintf("cannot parse updated_at %q: %v", updatedRaw, err))
		}

		// --- Tags ---
		for _, tag := range splitTags(tags) {
			if tag != "" && !validSlugRe.MatchString(tag) {
				add(SeverityWarning, "invalid-tag",
					fmt.Sprintf("tag %q contains characters that may not render correctly", tag))
				break
			}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return report, nil
}

// Stats returns article statistics for the database.
type Stats struct {
	Total     int
	Published int // created_at <= now
	Tags      map[string]int
	AvgSize   int64
	OldestAt  time.Time
	NewestAt  time.Time
}

// CollectStats gathers statistics from the VayuPress database.
func CollectStats(ctx context.Context, path string) (*Stats, error) {
	dsn := "file:" + path + "?_busy_timeout=5000"
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}
	defer db.Close()

	s := &Stats{Tags: make(map[string]int)}
	rows, err := db.QueryContext(ctx, `SELECT tags, LENGTH(content), created_at FROM articles`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var totalSize int64
	for rows.Next() {
		var tags, createdRaw string
		var size int64
		if err := rows.Scan(&tags, &size, &createdRaw); err != nil {
			return nil, err
		}
		s.Total++
		totalSize += size
		for _, t := range splitTags(tags) {
			if t != "" {
				s.Tags[t]++
			}
		}
		if t, err := parseDate(createdRaw); err == nil {
			if s.OldestAt.IsZero() || t.Before(s.OldestAt) {
				s.OldestAt = t
			}
			if t.After(s.NewestAt) {
				s.NewestAt = t
			}
		}
	}
	if s.Total > 0 {
		s.AvgSize = totalSize / int64(s.Total)
	}
	return s, rows.Err()
}

var dateLayouts = []string{
	time.RFC3339,
	"2006-01-02 15:04:05",
	"2006-01-02T15:04:05Z",
	"2006-01-02",
}

func parseDate(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, fmt.Errorf("empty date")
	}
	for _, layout := range dateLayouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("unrecognised date format: %q", s)
}

func splitTags(tags string) []string {
	return strings.Split(tags, ",")
}
