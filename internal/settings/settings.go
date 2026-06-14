// Package settings provides a key/value store for site and theme configuration,
// backed by the site_settings SQLite table (migration 006). Values are cached
// in-process for 30 s to avoid hitting the DB on every render.
package settings

import (
	"context"
	"database/sql"
	"sync"
	"time"
)

// Known setting keys — exhaustive list; unknown keys are rejected on write.
const (
	KeySiteName        = "site.name"
	KeySiteTagline     = "site.tagline"
	KeySiteDescription = "site.description"
	KeySiteAuthor      = "site.author"

	KeyThemePrimaryLight = "theme.primary_light"
	KeyThemePrimaryDark  = "theme.primary_dark"
	KeyThemeAccentLight  = "theme.accent_light"
	KeyThemeAccentDark   = "theme.accent_dark"
	KeyThemeCustomCSS    = "theme.custom_css"

	// Declarative <head> capabilities. These replace the former raw-HTML
	// `theme.custom_head` field (removed): arbitrary head HTML allowed
	// meta-refresh redirects, external beacons, and <base> hijacks that the
	// CSP does not fully cover. Each capability below renders to a single
	// escaped, allowlisted <meta> tag — no arbitrary markup reaches the page.
	KeyHeadKeywords     = "head.keywords"      // <meta name="keywords">
	KeyHeadThemeColor   = "head.theme_color"   // <meta name="theme-color"> (hex)
	KeyHeadRobots       = "head.robots"        // <meta name="robots"> (allowlisted)
	KeyHeadVerifyGoogle = "head.verify_google" // google-site-verification token
	KeyHeadVerifyBing   = "head.verify_bing"   // msvalidate.01 token
)

// RobotsOptions is the allowlist of accepted <meta name="robots"> directives.
var RobotsOptions = map[string]bool{
	"":                 true, // unset — omit the tag
	"index,follow":     true,
	"noindex,nofollow": true,
	"noindex,follow":   true,
	"index,nofollow":   true,
}

// AllKeys is the canonical set of settings keys accepted by Set/SetMany.
var AllKeys = map[string]bool{
	KeySiteName:          true,
	KeySiteTagline:       true,
	KeySiteDescription:   true,
	KeySiteAuthor:        true,
	KeyThemePrimaryLight: true,
	KeyThemePrimaryDark:  true,
	KeyThemeAccentLight:  true,
	KeyThemeAccentDark:   true,
	KeyThemeCustomCSS:    true,
	KeyHeadKeywords:      true,
	KeyHeadThemeColor:    true,
	KeyHeadRobots:        true,
	KeyHeadVerifyGoogle:  true,
	KeyHeadVerifyBing:    true,
}

// Defaults are returned when no DB value exists for a key.
var Defaults = map[string]string{
	KeySiteName:          "VayuPress",
	KeySiteTagline:       "Publishing as an adaptive runtime.",
	KeySiteDescription:   "Durable by design, observable end to end.",
	KeySiteAuthor:        "Ankush Choudhary Johal",
	KeyThemePrimaryLight: "#0f766e", // teal-700 — clears WCAG AA on the light bg
	KeyThemePrimaryDark:  "#2dd4bf",
	KeyThemeAccentLight:  "#f59e0b",
	KeyThemeAccentDark:   "#fbbf24",
	KeyThemeCustomCSS:    "",
	KeyHeadKeywords:      "",
	KeyHeadThemeColor:    "",
	KeyHeadRobots:        "",
	KeyHeadVerifyGoogle:  "",
	KeyHeadVerifyBing:    "",
}

// Store is a thread-safe settings store with an in-process read cache.
type Store struct {
	db    *sql.DB
	mu    sync.RWMutex
	cache map[string]string
	ttl   time.Time
}

// New creates a Store backed by db.
func New(db *sql.DB) *Store {
	return &Store{db: db, cache: make(map[string]string)}
}

// GetAll returns all known settings, merging DB values over Defaults.
func (s *Store) GetAll(ctx context.Context) (map[string]string, error) {
	s.mu.RLock()
	if time.Now().Before(s.ttl) {
		cp := make(map[string]string, len(s.cache))
		for k, v := range s.cache {
			cp[k] = v
		}
		s.mu.RUnlock()
		return cp, nil
	}
	s.mu.RUnlock()

	rows, err := s.db.QueryContext(ctx, `SELECT key, value FROM site_settings`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	m := make(map[string]string, len(Defaults))
	for k, v := range Defaults {
		m[k] = v
	}
	for rows.Next() {
		var k, v string
		if rows.Scan(&k, &v) == nil {
			m[k] = v
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	s.mu.Lock()
	s.cache = m
	s.ttl = time.Now().Add(30 * time.Second)
	s.mu.Unlock()
	return m, nil
}

// Get returns a single setting value (falls back to default on any error).
func (s *Store) Get(ctx context.Context, key string) string {
	all, _ := s.GetAll(ctx)
	if v, ok := all[key]; ok {
		return v
	}
	return Defaults[key]
}

// SetMany upserts multiple settings in one transaction and invalidates the cache.
// Unknown keys are silently ignored.
func (s *Store) SetMany(ctx context.Context, kv map[string]string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck
	for k, v := range kv {
		if !AllKeys[k] {
			continue
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO site_settings(key, value, updated_at)
			 VALUES(?,?,CURRENT_TIMESTAMP)
			 ON CONFLICT(key) DO UPDATE
			   SET value=excluded.value, updated_at=excluded.updated_at`,
			k, v,
		); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	s.invalidate()
	return nil
}

func (s *Store) invalidate() {
	s.mu.Lock()
	s.ttl = time.Time{}
	s.mu.Unlock()
}
