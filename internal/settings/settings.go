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
	// KeyMembershipButtons shows the public Sign in / Sign up buttons in the
	// homepage nav. Unlike feature flags it defaults OFF — only the string
	// "true" (as written by the settings toggle) enables it.
	KeyMembershipButtons = "site.membership_buttons"

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

	// Branding. A custom favicon/logo uploaded through the theme console is
	// stored base64-encoded in the DB (sovereign — survives in backups, no
	// extra file management) and overrides the embedded default marks at the
	// favicon serving routes. The type key records the validated MIME so the
	// serving handler sets the right Content-Type.
	KeyBrandFavicon     = "brand.favicon"      // base64-encoded PNG/ICO bytes
	KeyBrandFaviconType = "brand.favicon_type" // "image/png" | "image/x-icon"

	// KeyThemeHeroImage stores a base64-encoded hero/cover image (PNG/JPEG/WebP)
	// shown behind the homepage hero when the "Hero background" option is set to
	// Image. Served same-origin at /theme-assets/hero.
	KeyThemeHeroImage     = "theme.hero_image"
	KeyThemeHeroImageType = "theme.hero_image_type"

	// KeyThemeOGImage stores a base64-encoded social/share image (PNG/JPEG/WebP)
	// used as the og:image / twitter:image for the homepage and as the fallback
	// for articles without an inline image. Served at /theme-assets/og.
	KeyThemeOGImage     = "theme.og_image"
	KeyThemeOGImageType = "theme.og_image_type"

	// Navigation menu. A JSON array of {"label","href"} objects defining the
	// public nav links (top of every page). When unset, a sensible default
	// (Home / Feed / Console) is rendered. Operators add/remove items — internal
	// pages or external/redirect links — from Settings → Navigation.
	KeyNavItems = "nav.items"

	// Footer. A JSON object describing the premium public-site footer: tagline,
	// link columns, social links, legal links (Privacy/Terms…) and the copyright
	// line. Edited in Settings → Footer. When unset, a clean default copyright
	// bar is rendered.
	KeyFooterConfig = "footer.config"

	// Feature flags — operator-toggleable platform modules surfaced in the
	// Tools & Plugins panel. Each value is "on" (default) or "off". Disabling a
	// flag turns the corresponding public surface off at the request boundary;
	// it never tears down the backing store, so re-enabling is instant and
	// lossless. Unset is treated as enabled (see FeatureEnabled).
	KeyFeatureComments    = "feature.comments"    // public comment submission
	KeyFeatureNewsletter  = "feature.newsletter"  // public newsletter subscribe
	KeyFeatureWebmentions = "feature.webmentions" // inbound webmention receiver
)

// FeatureKeys is the set of operator-toggleable feature flags. Each maps to a
// public surface whose request handler consults FeatureEnabled before acting.
var FeatureKeys = map[string]bool{
	KeyFeatureComments:    true,
	KeyFeatureNewsletter:  true,
	KeyFeatureWebmentions: true,
}

// FeatureEnabled reports whether an operator-toggleable feature is on. An unset
// or any non-"off" value counts as enabled, so features default to available
// and only an explicit "off" disables them.
func (s *Store) FeatureEnabled(ctx context.Context, key string) bool {
	return s.Get(ctx, key) != "off"
}

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
	KeySiteName:           true,
	KeySiteTagline:        true,
	KeySiteDescription:    true,
	KeySiteAuthor:         true,
	KeyMembershipButtons:  true,
	KeyThemePrimaryLight:  true,
	KeyThemePrimaryDark:   true,
	KeyThemeAccentLight:   true,
	KeyThemeAccentDark:    true,
	KeyThemeCustomCSS:     true,
	KeyHeadKeywords:       true,
	KeyHeadThemeColor:     true,
	KeyHeadRobots:         true,
	KeyHeadVerifyGoogle:   true,
	KeyHeadVerifyBing:     true,
	KeyBrandFavicon:       true,
	KeyBrandFaviconType:   true,
	KeyThemeHeroImage:     true,
	KeyThemeHeroImageType: true,
	KeyThemeOGImage:       true,
	KeyThemeOGImageType:   true,
	KeyNavItems:           true,
	KeyFooterConfig:       true,
	KeyFeatureComments:    true,
	KeyFeatureNewsletter:  true,
	KeyFeatureWebmentions: true,
}

// Defaults are returned when no DB value exists for a key.
var Defaults = map[string]string{
	KeySiteName:           "VayuPress",
	KeySiteTagline:        "Publishing as an adaptive runtime.",
	KeySiteDescription:    "Durable by design, observable end to end.",
	KeySiteAuthor:         "Ankush Choudhary Johal",
	KeyThemePrimaryLight:  "#0f766e", // teal-700 — clears WCAG AA on the light bg
	KeyThemePrimaryDark:   "#2dd4bf",
	KeyThemeAccentLight:   "#f59e0b",
	KeyThemeAccentDark:    "#fbbf24",
	KeyThemeCustomCSS:     "",
	KeyHeadKeywords:       "",
	KeyHeadThemeColor:     "",
	KeyHeadRobots:         "",
	KeyHeadVerifyGoogle:   "",
	KeyHeadVerifyBing:     "",
	KeyBrandFavicon:       "",
	KeyBrandFaviconType:   "",
	KeyThemeHeroImage:     "",
	KeyThemeHeroImageType: "",
	KeyThemeOGImage:       "",
	KeyThemeOGImageType:   "",
	KeyFeatureComments:    "on",
	KeyFeatureNewsletter:  "on",
	KeyFeatureWebmentions: "on",
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
