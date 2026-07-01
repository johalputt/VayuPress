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

	// KeyHomeHero toggles the big homepage hero block. Default OFF ("") for a
	// clean homepage that goes straight to the post list; set to "true" to show
	// the hero (tagline headline + description), styled by the Hero options.
	KeyHomeHero = "home.hero"

	// KeyAuthorBio is a short author bio shown in the article author box (with
	// the site author name). Plain text, optional.
	KeyAuthorBio = "site.author_bio"

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

	// Contact. The email address that public contact-form submissions are
	// delivered to over the built-in VayuMail SMTP sender. When unset, the
	// contact endpoint reports that contact is not configured. Set in the Pages
	// surface. The form itself is opt-in per page via the [[contact-form]] marker.
	KeyContactEmail = "contact.email"

	// KeyContactAutoReply toggles the confirmation email sent back to a visitor
	// after they submit the contact form ("thanks, we got your message"). Any
	// value other than "off" (including unset) counts as enabled, so auto-reply
	// is on by default once a recipient is configured.
	KeyContactAutoReply = "contact.autoreply"

	// KeyMediaAlt stores a JSON object mapping a content-addressed media filename
	// to its operator-authored alt text, edited in the Media library. Used as the
	// default alt when inserting that image. Absent keys simply have no default.
	KeyMediaAlt = "media.alt"

	// Feature flags — operator-toggleable platform modules surfaced in the
	// Tools & Plugins panel. Each value is "on" (default) or "off". Disabling a
	// flag turns the corresponding public surface off at the request boundary;
	// it never tears down the backing store, so re-enabling is instant and
	// lossless. Unset is treated as enabled (see FeatureEnabled).
	KeyFeatureComments    = "feature.comments"    // public comment submission
	KeyFeatureNewsletter  = "feature.newsletter"  // public newsletter subscribe
	KeyFeatureWebmentions = "feature.webmentions" // inbound webmention receiver

	// KeyFeatureSearch toggles VayuFind, the built-in site search (the nav search
	// box, the instant search modal, and the server-rendered /search page).
	// Default ON. When off, the search box/modal are hidden, /search returns 404,
	// and the search engine returns no results. This is the operator's single
	// "search on/off" switch — VayuPress has no external search dependency.
	KeyFeatureSearch = "feature.search"

	// KeyFeatureMeili is the legacy external-Meilisearch toggle.
	//
	// Deprecated: the external Meilisearch backend was removed in favour of the
	// built-in VayuFind engine (see KeyFeatureSearch / ADR-0101). The constant is
	// retained only so older stored values and references still resolve; it is no
	// longer a live toggle and is not shown in Tools & Plugins.
	KeyFeatureMeili = "feature.meili"

	// KeyFeatureTrending toggles the public "Trending & pinned posts" widget
	// shown on the homepage and at the bottom of every post. Default ON. When
	// off, the /api/trending endpoint reports disabled and the client-side widget
	// removes itself. Trending posts are the most-viewed in the last 7/30 days
	// per the built-in cookieless analytics; pinned posts are the operator's
	// featured posts (see the editor "Feature this post" toggle), capped at 4.
	KeyFeatureTrending = "feature.trending"

	// Monetization feature flags. Unlike the engagement flags above these
	// default OFF: a site only starts taking payments or showing advertising
	// once the operator explicitly switches the module on from Tools & Plugins,
	// so a fresh install never surprises readers with a checkout or an advert.
	KeyFeaturePayments  = "feature.payments"  // accept subscription payments (checkout)
	KeyFeatureAds       = "feature.ads"       // render ad slots on public pages
	KeyFeatureGoogleAds = "feature.googleads" // serve Google AdSense units in ad slots
	KeyFeatureAffiliate = "feature.affiliate" // show the affiliate-disclosure banner
	KeyFeatureSponsors  = "feature.sponsors"  // show the sponsor banner slot

	// Monetization configuration (non-toggle settings).
	//
	// KeyPayDirectInstructions is the operator-authored payment instructions
	// shown to a reader on the checkout page for the built-in direct/offline
	// gateway (e.g. bank transfer details, a UPI id, a PayPal.me link). Plain
	// multi-line text; rendered escaped.
	KeyPayDirectInstructions = "monetization.direct_instructions"
	// KeyPayCurrency is the ISO-4217 currency the checkout charges in (display
	// + order records). Defaults to USD.
	KeyPayCurrency = "monetization.currency"
	// KeyPaySupportEmail is the reply-to address printed on payment emails and
	// the checkout page so payers can reach a human. Falls back to SMTP From.
	KeyPaySupportEmail = "monetization.support_email"

	// Advertising configuration.
	//
	// KeyAdsenseClient is the Google AdSense publisher id ("ca-pub-…"); when set
	// and the Google Ads module is on, ad slots of type "adsense" render real
	// AdSense units and the page CSP is widened to admit Google's ad origins.
	KeyAdsenseClient = "ads.adsense_client"
	// KeyAffiliateDisclosure is the short disclosure text shown above content
	// when the affiliate module is enabled (FTC-style "contains affiliate links").
	KeyAffiliateDisclosure = "ads.affiliate_disclosure"
)

// FeatureKeys is the set of operator-toggleable feature flags. Each maps to a
// public surface whose request handler consults FeatureEnabled before acting.
var FeatureKeys = map[string]bool{
	KeyFeatureComments:    true,
	KeyFeatureNewsletter:  true,
	KeyFeatureWebmentions: true,
	KeyFeatureSearch:      true,
	KeyFeatureTrending:    true,
	KeyFeaturePayments:    true,
	KeyFeatureAds:         true,
	KeyFeatureGoogleAds:   true,
	KeyFeatureAffiliate:   true,
	KeyFeatureSponsors:    true,
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
	KeyContactEmail:          true,
	KeyContactAutoReply:      true,
	KeyMediaAlt:              true,
	KeySiteName:              true,
	KeySiteTagline:           true,
	KeySiteDescription:       true,
	KeySiteAuthor:            true,
	KeyMembershipButtons:     true,
	KeyThemePrimaryLight:     true,
	KeyThemePrimaryDark:      true,
	KeyThemeAccentLight:      true,
	KeyThemeAccentDark:       true,
	KeyThemeCustomCSS:        true,
	KeyHeadKeywords:          true,
	KeyHeadThemeColor:        true,
	KeyHeadRobots:            true,
	KeyHeadVerifyGoogle:      true,
	KeyHeadVerifyBing:        true,
	KeyBrandFavicon:          true,
	KeyBrandFaviconType:      true,
	KeyThemeHeroImage:        true,
	KeyThemeHeroImageType:    true,
	KeyThemeOGImage:          true,
	KeyThemeOGImageType:      true,
	KeyHomeHero:              true,
	KeyAuthorBio:             true,
	KeyNavItems:              true,
	KeyFooterConfig:          true,
	KeyFeatureComments:       true,
	KeyFeatureNewsletter:     true,
	KeyFeatureWebmentions:    true,
	KeyFeatureSearch:         true,
	KeyFeatureTrending:       true,
	KeyFeaturePayments:       true,
	KeyFeatureAds:            true,
	KeyFeatureGoogleAds:      true,
	KeyFeatureAffiliate:      true,
	KeyFeatureSponsors:       true,
	KeyPayDirectInstructions: true,
	KeyPayCurrency:           true,
	KeyPaySupportEmail:       true,
	KeyAdsenseClient:         true,
	KeyAffiliateDisclosure:   true,
	// admin.theme is the operator's VayuOS console colour theme (light/dark/auto),
	// persisted from the topbar theme toggle rather than the theme editor form.
	"admin.theme": true,
}

// Defaults are returned when no DB value exists for a key.
var Defaults = map[string]string{
	KeySiteName:              "VayuPress",
	KeySiteTagline:           "Publishing as an adaptive runtime.",
	KeySiteDescription:       "Durable by design, observable end to end.",
	KeySiteAuthor:            "Ankush Choudhary Johal",
	KeyThemePrimaryLight:     "#0f766e", // teal-700 — clears WCAG AA on the light bg
	KeyThemePrimaryDark:      "#2dd4bf",
	KeyThemeAccentLight:      "#f59e0b",
	KeyThemeAccentDark:       "#fbbf24",
	KeyThemeCustomCSS:        "",
	KeyHeadKeywords:          "",
	KeyHeadThemeColor:        "",
	KeyHeadRobots:            "",
	KeyHeadVerifyGoogle:      "",
	KeyHeadVerifyBing:        "",
	KeyBrandFavicon:          "",
	KeyBrandFaviconType:      "",
	KeyThemeHeroImage:        "",
	KeyThemeHeroImageType:    "",
	KeyThemeOGImage:          "",
	KeyThemeOGImageType:      "",
	KeyHomeHero:              "",
	KeyAuthorBio:             "",
	KeyFeatureComments:       "on",
	KeyFeatureNewsletter:     "on",
	KeyFeatureWebmentions:    "on",
	KeyFeatureSearch:         "on",
	KeyFeaturePayments:       "off",
	KeyFeatureAds:            "off",
	KeyFeatureGoogleAds:      "off",
	KeyFeatureAffiliate:      "off",
	KeyFeatureSponsors:       "off",
	KeyPayDirectInstructions: "",
	KeyPayCurrency:           "USD",
	KeyPaySupportEmail:       "",
	KeyAdsenseClient:         "",
	KeyAffiliateDisclosure:   "This post may contain affiliate links. We may earn a commission at no extra cost to you.",
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
