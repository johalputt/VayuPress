// SPDX-License-Identifier: Apache-2.0

// Package settings provides a key/value store for site and theme configuration,
// backed by the site_settings SQLite table (migration 006). Values are cached
// in-process for 30 s to avoid hitting the DB on every render.
package settings

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"time"
)

// errNoScope is returned when a write arrives without a scope.
var errNoScope = errors.New("settings: write attempted without a scope")

// Known setting keys — exhaustive list; unknown keys are rejected on write.
const (
	KeySiteName        = "site.name"
	KeySiteTagline     = "site.tagline"
	KeySiteDescription = "site.description"
	KeySiteAuthor      = "site.author"
	// KeySiteTimezone is the IANA display timezone (e.g. "Asia/Kolkata"). Stored
	// timestamps stay UTC; this only controls what is rendered. Empty means UTC.
	KeySiteTimezone = "site.timezone"
	// KeyMembershipButtons shows the public Sign in / Sign up buttons in the
	// homepage nav. Unlike feature flags it defaults OFF — only the string
	// "true" (as written by the settings toggle) enables it.
	KeyMembershipButtons = "site.membership_buttons"

	// KeyMaintenanceMode, when "on", takes the PUBLIC site offline behind a
	// premium "under maintenance" page (a 503). The VayuOS admin console and the
	// operational endpoints stay reachable so the operator can turn it back off.
	// KeyMaintenanceMessage is an optional custom line shown on that page.
	KeyMaintenanceMode    = "site.maintenance"
	KeyMaintenanceMessage = "site.maintenance_message"

	// KeyFeedbackEmail is the mailbox that VayuOS's "Report a bug / suggest an
	// improvement" button composes to. Empty falls back to feedback@<domain>.
	KeyFeedbackEmail = "vayupress.feedback_email"

	// KeyBlockCrawlers, when "on", hard-blocks search-engine and AI crawlers from
	// the PUBLIC site: robots.txt disallows everything, known crawler user-agents
	// (Googlebot, Bingbot, GPTBot, ClaudeBot, PerplexityBot, …) get a 403, and
	// every public response carries X-Robots-Tag: noindex as a backstop. The
	// VayuOS console and operational endpoints are never affected. Defaults OFF.
	KeyBlockCrawlers = "site.block_crawlers"

	// VayuKeep — automatic backup, configured from VayuOS (ADR-0145). The target
	// is a setting rather than an env var so it can be changed from the console;
	// the passphrase is a sealed credential, never a setting.
	KeyVayuKeepTarget  = "vayukeep.target"
	KeyVayuKeepEnabled = "vayukeep.enabled"
	// Retention, settable from the console so "auto-delete old backups" is a
	// control an operator can see and change rather than an environment variable.
	KeyVayuKeepRetainGen  = "vayukeep.retain_generations"
	KeyVayuKeepRetainDays = "vayukeep.retain_days"

	// Business-website mode (VayuOS → Website). KeySiteMode selects what the
	// root domain serves: "" / "blog" keeps the blog at the root (the historic
	// behaviour — existing installs never change on update), "business" serves
	// the business site at the root with the blog at blog.<domain>.
	KeySiteMode    = "site.mode"
	KeyBizTemplate = "biz.template" // active business template key
	KeyBizContent  = "biz.content"  // business-site content (JSON)

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

	// KeyMailQueueRetentionDays is the operator-chosen auto-clear window (days) for
	// DELIVERED outbound-queue rows shown in the Outbox; "" / "0" = keep forever.
	// Only the delivery-status record is pruned — the Sent Maildir copy is kept.
	KeyMailQueueRetentionDays = "mail.queue_retention_days"

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
	// KeyPayPalSandbox routes PayPal to the sandbox host ("on") instead of live
	// ("off", default) — for testing with sandbox REST credentials.
	KeyPayPalSandbox = "monetization.paypal_sandbox"
	// KeyBTCPayURL / KeyBTCPayStoreID are the non-secret coordinates of the
	// operator's self-hosted BTCPay Server store (the Greenfield API key + webhook
	// secret live in the encrypted credential store). With all three present, the
	// crypto gateway accepts BTC/XMR/ETH/USDT via BTCPay's hosted checkout —
	// letting anonymous / Tor buyers pay without an account.
	KeyBTCPayURL     = "monetization.btcpay_url"
	KeyBTCPayStoreID = "monetization.btcpay_store_id"
	// KeyPremiumMailIDPriceCents is the price (in minor units of KeyPayCurrency)
	// the operator charges for a premium (vanity) VayuMail address in the mail-ID
	// marketplace. Surfaced in the member portal next to a premium name. Default
	// "500" (e.g. $5.00).
	KeyPremiumMailIDPriceCents = "monetization.premium_mailid_price_cents"
	// KeyMailIDTerms is the acceptable-use / terms text a member must accept
	// before a VayuMail address is provisioned to them. The member's acceptance
	// (address + a hash of this text + timestamp) is recorded so the operator has
	// proof of agreement and is protected from misuse of the address. Editable so
	// the operator can substitute their own legal copy.
	KeyMailIDTerms = "monetization.mailid_terms"
	// KeyAdSlotPriceCents is the price (minor units of KeyPayCurrency) a member
	// pays to submit a self-serve ("advertise here") ad for operator review.
	// Default "1000" (e.g. $10.00).
	KeyAdSlotPriceCents = "monetization.ad_slot_price_cents"

	// Advertising configuration.
	//
	// KeyAdsenseClient is the Google AdSense publisher id ("ca-pub-…"); when set
	// and the Google Ads module is on, ad slots of type "adsense" render real
	// AdSense units and the page CSP is widened to admit Google's ad origins.
	KeyAdsenseClient = "ads.adsense_client"
	// KeyAffiliateDisclosure is the short disclosure text shown above content
	// when the affiliate module is enabled (FTC-style "contains affiliate links").
	KeyAffiliateDisclosure = "ads.affiliate_disclosure"

	// VayuShield — bot protection + Tier-1 (in-binary) DDoS resilience. All of
	// these are operator-toggleable live from VayuOS → VayuShield
	// with no restart. Booleans are "on"/"off"; shield.enabled and every
	// resilience toggle default OFF so a fresh install never challenges or
	// throttles a real visitor until the operator explicitly opts in.
	KeyShieldEnabled        = "shield.enabled"         // bot classification + challenge ladder
	KeyShieldPoW            = "shield.pow_threshold"   // score >= this -> silent proof-of-work
	KeyShieldJS             = "shield.js_threshold"    // score >= this -> JS interstitial
	KeyShieldBlock          = "shield.block_threshold" // score >= this -> hard block
	KeyShieldTarpit         = "shield.tarpit"          // block -> tarpit the worst offenders
	KeyShieldRateLimit      = "shield.ratelimit"       // per-IP token-bucket rate limiting
	KeyShieldRateRPM        = "shield.rate_rpm"        // sustained per-IP requests/minute
	KeyShieldBurst          = "shield.burst"           // per-IP burst ceiling
	KeyShieldLoadShed       = "shield.loadshed"        // in-flight concurrency cap (503 when full)
	KeyShieldMaxInFlight    = "shield.max_inflight"    // max concurrent requests (0 = unlimited)
	KeyShieldAutoBlock      = "shield.autoblock"       // auto-jail IPs that keep breaching the limit
	KeyShieldJailMinutes    = "shield.jail_minutes"    // how long an auto-jailed IP stays blocked
	KeyShieldUnderAttack    = "shield.underattack"     // adaptive: tighten thresholds during a flood
	KeyShieldUnderAttackRPS = "shield.underattack_rps" // global RPS that trips attack mode
	KeyShieldSurge          = "shield.surge"           // Aegis L3: challenge all unproven visitors up front
	KeyShieldBehindCDN      = "shield.behind_cdn"      // trust Cloudflare/CDN CF-Connecting-IP for the real visitor IP
	KeyShieldGroupIPv4      = "shield.group_ipv4"      // extend prefix-keyed enforcement to IPv4 /24 (IPv6 /64 is unconditional)
	KeyShieldObserve        = "shield.observe"         // observe-only: count what every gate WOULD do, enforce nothing

	// The operator's own rules, as opposed to everything the shield infers. All
	// four are newline-separated free text so they can be pasted from a firewall
	// config, and all four default to empty: a shield that arrives with opinions
	// about which networks or countries an operator serves would be making a
	// political decision on their behalf.
	KeyShieldAllowCIDRs     = "shield.allow_cidrs"     // never challenged, never jailed (office, probe, CI runner)
	KeyShieldDenyCIDRs      = "shield.deny_cidrs"      // never served
	KeyShieldAllowCountries = "shield.allow_countries" // when non-empty, ONLY these are served
	KeyShieldDenyCountries  = "shield.deny_countries"  // ISO-3166-1 alpha-2, one per line
	// KeyShieldChallengeCountries is the middle setting: a solvable proof of work
	// rather than a refusal. Refusing a country says these people are not
	// customers; challenging one says the traffic is mostly automated.
	KeyShieldChallengeCountries = "shield.challenge_countries"
	KeyShieldRouteCosts         = "shield.route_costs" // "<path> <weight>" per line: what a route costs to serve

	// Multi-node verdict sharing. Peers are the base URLs of the OTHER nodes,
	// one per line; the node name identifies this one in a peer's accounting.
	// The gossip key is DERIVED from the install secret, never stored here — a
	// key in the settings table would be a key in every backup.
	KeyShieldClusterPeers = "shield.cluster_peers"
	KeyShieldClusterNode  = "shield.cluster_node"

	// KeyShieldIntelFeeds holds the IDs of the third-party network-intelligence
	// feeds the operator has switched on, comma-separated. Empty by default, and
	// empty means no feed is fetched and none is consulted.
	//
	// There is one setting rather than one per feed, and no separate "and
	// actually enforce it" switch, because the feed's KIND already carries what
	// it means: a datacenter list weighs a score, a hostile list refuses. Two
	// decisions where the operator made one is how a list ends up enabled,
	// fetching, displayed as healthy, and connected to nothing.
	KeyShieldIntelFeeds = "shield.intel_feeds"

	// KeyAnalyticsBeacon toggles the VayuAnalytics engagement beacon (time on
	// page / scroll depth) injected on public pages. Default ON — it is
	// cookieless and stores no PII, the same posture as the existing view
	// analytics, so it is safe to enable by default.
	KeyAnalyticsBeacon = "analytics.beacon"

	// KeyTorEnabled is the one-click VayuTor toggle: when "on" (and the env
	// master switch VAYUOS_TOR is not off, and a tor control port is reachable),
	// every hosted domain is published as a v3 onion service alongside its
	// clearnet URL. Default OFF — Tor onions are opt-in.
	KeyTorEnabled = "tor.enabled"
	// KeyTorVisits is the persisted aggregate count of onion pageviews — the
	// ENTIRE VayuTor analytic. No identifier, time, path, or any other datum is
	// ever stored (privacy by construction).
	KeyTorVisits = "tor.visits"
	// KeyTorBridges holds operator-supplied Tor bridge lines (newline/";"-
	// separated obfs4 or vanilla Bridge lines), configured entirely from the
	// VayuTor admin page — no server access needed. When set, VayuTor routes its
	// managed tor through these bridges, which defeats a network that blocks Tor
	// at the IP level (a VPS null-routing public relays, or DPI).
	KeyTorBridges = "tor.bridges"
	// KeyTorPageStats opts INTO per-page onion visit counts. Default OFF, which
	// preserves VayuTor's stricter "not even the path" promise. When "on", VayuTor
	// keeps an AGGREGATE cumulative count per page (host+path → total views) — and
	// still no identifier, no time, no session, no ordering, so individual visits
	// can never be correlated or a visitor deanonymised. Visitor geolocation is
	// deliberately absent: an onion service never sees the client's IP.
	KeyTorPageStats = "tor.page_stats"
	// KeyTorPageHits persists the per-page counts (a small JSON object of
	// "host path" → count) when KeyTorPageStats is on. Aggregate only.
	KeyTorPageHits = "tor.page_hits"
	// KeyTorOnionLocation controls whether clearnet responses advertise their
	// onion via the Onion-Location header (so Tor Browser can offer/auto-switch).
	// Default "on"; set "off" to stop advertising without deactivating onions.
	KeyTorOnionLocation = "tor.onion_location"
	// KeyTorSpaceEnabled is the one-click Anonymous Tor Space toggle (ADR-0141):
	// "on" spins up the isolated Tor-world child instance + its dedicated onion;
	// anything else keeps it stopped. Default off.
	KeyTorSpaceEnabled = "tor.space_enabled"
	// KeyTorSpaceAPIKey persists the child instance's DISTINCT API key so its
	// identity is stable across restarts and never shares the parent's key.
	KeyTorSpaceAPIKey = "tor.space_api_key"
	// KeyTalkAnonID is the local-part of the ANONYMOUS, rotatable VayuTalk identity
	// used in the Tor world (ADR-0141) — a random handle, not a mailbox address, so
	// chat is not linked to a mail account. "Rotate" replaces it with a fresh one.
	KeyTalkAnonID = "talk.anon_id"
	// KeyTalkOnionFederation opts into experimental onion-to-onion VayuTalk
	// delivery (ADR-0142): reaching a chat code hosted on a DIFFERENT .onion over
	// Tor. Off by default — while off, behaviour is unchanged (a message to a
	// remote onion code fails with an honest "not reachable"). Enabling it adds a
	// guard-railed, .onion-only outbound Tor lane; it has no effect outside the
	// Tor world.
	KeyTalkOnionFederation = "talk.onion_federation"
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
func (s *Store) FeatureEnabled(ctx context.Context, sc Scope, key string) bool {
	return s.Get(ctx, sc, key) != "off"
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
	KeyContactEmail:            true,
	KeyContactAutoReply:        true,
	KeyMediaAlt:                true,
	KeySiteName:                true,
	KeySiteTagline:             true,
	KeySiteDescription:         true,
	KeySiteAuthor:              true,
	KeySiteTimezone:            true,
	KeyMembershipButtons:       true,
	KeyMaintenanceMode:         true,
	KeyMaintenanceMessage:      true,
	KeyBlockCrawlers:           true,
	KeyFeedbackEmail:           true,
	KeyThemePrimaryLight:       true,
	KeyThemePrimaryDark:        true,
	KeyThemeAccentLight:        true,
	KeyThemeAccentDark:         true,
	KeyThemeCustomCSS:          true,
	KeyHeadKeywords:            true,
	KeyHeadThemeColor:          true,
	KeyHeadRobots:              true,
	KeyHeadVerifyGoogle:        true,
	KeyHeadVerifyBing:          true,
	KeyBrandFavicon:            true,
	KeyBrandFaviconType:        true,
	KeyThemeHeroImage:          true,
	KeyThemeHeroImageType:      true,
	KeyThemeOGImage:            true,
	KeyThemeOGImageType:        true,
	KeyHomeHero:                true,
	KeyAuthorBio:               true,
	KeyNavItems:                true,
	KeyFooterConfig:            true,
	KeyFeatureComments:         true,
	KeyFeatureNewsletter:       true,
	KeyFeatureWebmentions:      true,
	KeyFeatureSearch:           true,
	KeyFeatureTrending:         true,
	KeyFeaturePayments:         true,
	KeyFeatureAds:              true,
	KeyFeatureGoogleAds:        true,
	KeyFeatureAffiliate:        true,
	KeyFeatureSponsors:         true,
	KeyPayDirectInstructions:   true,
	KeyPayCurrency:             true,
	KeyPaySupportEmail:         true,
	KeyPayPalSandbox:           true,
	KeyBTCPayURL:               true,
	KeyBTCPayStoreID:           true,
	KeyPremiumMailIDPriceCents: true,
	KeyMailIDTerms:             true,
	KeyAdSlotPriceCents:        true,
	KeyAdsenseClient:           true,
	KeyAffiliateDisclosure:     true,
	KeySiteMode:                true,
	KeyBizTemplate:             true,
	KeyBizContent:              true,
	// admin.theme is the operator's VayuOS console colour theme (light/dark/auto),
	// persisted from the topbar theme toggle rather than the theme editor form.
	"admin.theme": true,
	// VayuShield + VayuAnalytics runtime toggles.
	KeyShieldEnabled:            true,
	KeyShieldPoW:                true,
	KeyShieldJS:                 true,
	KeyShieldBlock:              true,
	KeyShieldTarpit:             true,
	KeyShieldRateLimit:          true,
	KeyShieldRateRPM:            true,
	KeyShieldBurst:              true,
	KeyShieldLoadShed:           true,
	KeyShieldMaxInFlight:        true,
	KeyShieldAutoBlock:          true,
	KeyShieldJailMinutes:        true,
	KeyShieldUnderAttack:        true,
	KeyShieldUnderAttackRPS:     true,
	KeyShieldSurge:              true,
	KeyShieldBehindCDN:          true,
	KeyShieldGroupIPv4:          true,
	KeyShieldObserve:            true,
	KeyShieldAllowCIDRs:         true,
	KeyShieldDenyCIDRs:          true,
	KeyShieldAllowCountries:     true,
	KeyShieldDenyCountries:      true,
	KeyShieldChallengeCountries: true,
	KeyShieldRouteCosts:         true,
	KeyShieldClusterPeers:       true,
	KeyShieldClusterNode:        true,
	KeyShieldIntelFeeds:         true,
	KeyAnalyticsBeacon:          true,
	KeyTorEnabled:               true,
	KeyTorVisits:                true,
	KeyTorBridges:               true,
	KeyTorPageStats:             true,
	KeyTorPageHits:              true,
	KeyTorOnionLocation:         true,
	// Anonymous Tor Space (ADR-0141). MUST be registered here or SetMany silently
	// drops the write — the one-click world toggle would appear to succeed (200)
	// yet never persist, so the reloaded page always reads "off".
	KeyTorSpaceEnabled:     true,
	KeyTorSpaceAPIKey:      true,
	KeyTalkAnonID:          true,
	KeyTalkOnionFederation: true,
}

// Defaults are returned when no DB value exists for a key.
var Defaults = map[string]string{
	KeySiteName:                "VayuPress",
	KeySiteTagline:             "Publishing as an adaptive runtime.",
	KeySiteDescription:         "Durable by design, observable end to end.",
	KeySiteAuthor:              "Ankush Choudhary Johal",
	KeyThemePrimaryLight:       "#0f766e", // teal-700 — clears WCAG AA on the light bg
	KeyThemePrimaryDark:        "#2dd4bf",
	KeyThemeAccentLight:        "#f59e0b",
	KeyThemeAccentDark:         "#fbbf24",
	KeyThemeCustomCSS:          "",
	KeyHeadKeywords:            "",
	KeyHeadThemeColor:          "",
	KeyHeadRobots:              "",
	KeyHeadVerifyGoogle:        "",
	KeyHeadVerifyBing:          "",
	KeyBrandFavicon:            "",
	KeyBrandFaviconType:        "",
	KeyThemeHeroImage:          "",
	KeyThemeHeroImageType:      "",
	KeyThemeOGImage:            "",
	KeyThemeOGImageType:        "",
	KeyHomeHero:                "",
	KeyAuthorBio:               "",
	KeyFeatureComments:         "on",
	KeyFeatureNewsletter:       "on",
	KeyFeatureWebmentions:      "on",
	KeyFeatureSearch:           "on",
	KeyFeaturePayments:         "off",
	KeyFeatureAds:              "off",
	KeyFeatureGoogleAds:        "off",
	KeyFeatureAffiliate:        "off",
	KeyFeatureSponsors:         "off",
	KeyPayDirectInstructions:   "",
	KeyPayCurrency:             "USD",
	KeyPaySupportEmail:         "",
	KeyPayPalSandbox:           "off",
	KeyBTCPayURL:               "",
	KeyBTCPayStoreID:           "",
	KeyPremiumMailIDPriceCents: "500",
	KeyMailIDTerms:             "By claiming this email address you agree to use it lawfully and you accept sole responsibility for all messages sent from it. You must not use it for spam, fraud, impersonation, harassment, or any illegal purpose. The address remains the property of the site operator, who may suspend or reclaim it for a breach of these terms or for non-payment. The operator provides the address as-is and is not liable for your use of it.",
	KeyAdSlotPriceCents:        "1000",
	KeyAdsenseClient:           "",
	KeyAffiliateDisclosure:     "This post may contain affiliate links. We may earn a commission at no extra cost to you.",
	// VayuShield defaults ON (gentle): bot classification is active so real
	// browsers pass silently and verified search/AI crawlers are fast-pathed,
	// while the aggressive resilience gates below (tarpit, rate-limit, load-shed,
	// auto-block, under-attack, surge) stay OFF until the operator opts in. An
	// operator can still turn the whole shield off from the panel or VAYUSHIELD=off.
	KeyShieldEnabled:   "on",
	KeyShieldPoW:       "0.4",
	KeyShieldJS:        "0.6",
	KeyShieldBlock:     "0.8",
	KeyShieldTarpit:    "off",
	KeyShieldRateLimit: "off",
	KeyShieldRateRPM:   "120",
	KeyShieldBurst:     "60",
	// Load shedding is the ONE availability gate that is safe to default on, and
	// the reasoning is worth writing down because the other two look equally
	// harmless and are not.
	//
	// It caps concurrent in-flight requests and answers a cheap 503 with
	// Retry-After when the process is genuinely saturated. It has no per-visitor
	// keying at all, so it cannot single anyone out, cannot be wrong about who a
	// visitor is, and cannot lock a reader out — the worst it does under load is
	// what the process would do anyway, but cheaply and with a signal a crawler
	// honours instead of a timeout.
	//
	// Rate limiting is deliberately NOT defaulted on beside it. It keys on the
	// client address, and on a proxied origin that has not set "Behind a CDN"
	// every visitor resolves to a handful of edge addresses — so the whole
	// audience shares one bucket and 120 requests a minute is nothing. Defaulting
	// it on would take exactly the installs that have never opened this panel and
	// show all of their readers a 429. Auto-block is not defaulted on either: it
	// is the punitive one, and observe mode now exists so an operator can watch
	// what it would have done to their own traffic first.
	KeyShieldLoadShed:           "on",
	KeyShieldMaxInFlight:        "0",
	KeyShieldAutoBlock:          "off",
	KeyShieldJailMinutes:        "10",
	KeyShieldUnderAttack:        "off",
	KeyShieldUnderAttackRPS:     "200",
	KeyShieldBehindCDN:          "off",
	KeyShieldGroupIPv4:          "off",
	KeyShieldObserve:            "off",
	KeyShieldAllowCIDRs:         "",
	KeyShieldDenyCIDRs:          "",
	KeyShieldAllowCountries:     "",
	KeyShieldDenyCountries:      "",
	KeyShieldChallengeCountries: "",
	KeyShieldRouteCosts:         "",
	KeyShieldClusterPeers:       "",
	KeyShieldClusterNode:        "",
	KeyShieldIntelFeeds:         "",
	KeyAnalyticsBeacon:          "on",
	KeyTorEnabled:               "off",
	KeyTorVisits:                "0",
	KeyTorBridges:               "",
	KeyTorPageStats:             "off",
	KeyTorPageHits:              "",
	KeyTorOnionLocation:         "on",
	KeyTorSpaceEnabled:          "off",
	KeyTorSpaceAPIKey:           "",
	KeyTalkAnonID:               "",
	KeyTalkOnionFederation:      "off",
}

// Store is a thread-safe settings store with an in-process read cache.
// Store is the settings store. Every read and write takes a Scope (ADR-0153 D1).
//
// PHASE 1 NOTE. The scope is plumbed through the API and the cache, and the
// site_settings table does not yet carry a scope column — that is ADR-0153
// Phase 2. So today every scope resolves to the same rows, exactly as before,
// and this phase changes no behaviour. The type comes first on purpose: adding
// the column while reads were still unscoped would open a window in which
// unscoped reads hit a scoped table, which is the leak the whole ADR exists to
// close.
type Store struct {
	db *sql.DB
	mu sync.RWMutex
	// cache and ttl are keyed by Scope.key(), so one domain's settings can never
	// be served to another out of a shared map. Getting this wrong would be a
	// cross-tenant leak with no schema change behind it, visible only under
	// concurrency — the first domain to warm the cache would serve its theme to
	// every other domain on the install.
	cache map[string]map[string]string
	ttl   map[string]time.Time
}

// New creates a Store backed by db.
func New(db *sql.DB) *Store {
	return &Store{
		db:    db,
		cache: make(map[string]map[string]string),
		ttl:   make(map[string]time.Time),
	}
}

// GetAll returns all known settings for one scope, merging stored values over
// Defaults.
//
// An unset scope resolves to Defaults alone and never touches the database. A
// caller that did not say whose settings it wants does not get the primary's —
// that silent inheritance is the defect this design removes, and answering it
// with the product's own defaults is the one answer that belongs to nobody.
func (s *Store) GetAll(ctx context.Context, sc Scope) (map[string]string, error) {
	if !sc.Valid() {
		return defaultsCopy(), nil
	}
	sk := sc.key()

	s.mu.RLock()
	if time.Now().Before(s.ttl[sk]) {
		cp := make(map[string]string, len(s.cache[sk]))
		for k, v := range s.cache[sk] {
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

	m := defaultsCopy()
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
	s.cache[sk] = m
	s.ttl[sk] = time.Now().Add(30 * time.Second)
	s.mu.Unlock()
	return m, nil
}

// defaultsCopy returns a fresh copy of the compiled-in defaults.
//
// A copy, not the map itself: Defaults is package state, and handing a caller a
// reference to it means any caller that writes into its result silently
// redefines the product's defaults for every scope on the install.
func defaultsCopy() map[string]string {
	m := make(map[string]string, len(Defaults))
	for k, v := range Defaults {
		m[k] = v
	}
	return m
}

// Get returns a single setting value (falls back to default on any error).
// Get returns one setting's value (falling back to its default).
//
// It reads the cached map in place rather than going through GetAll, which
// COPIES every entry. Get is the hot accessor — the VayuOS shell alone calls it
// several times per page render and it is used from ~60 call sites — so copying
// the whole ~85-entry map to read a single string was pure waste on every admin
// page load and every public render. Only a cold/expired cache falls through to
// GetAll (which does the query and refills).
func (s *Store) Get(ctx context.Context, sc Scope, key string) string {
	// An unset scope reads the product default and nothing else. It must never
	// fall through to the primary's stored value: that is the inheritance that
	// made a hosted domain look like the operator's own site.
	if !sc.Valid() {
		return Defaults[key]
	}
	sk := sc.key()

	s.mu.RLock()
	if time.Now().Before(s.ttl[sk]) {
		v, ok := s.cache[sk][key]
		s.mu.RUnlock()
		if ok {
			return v
		}
		return Defaults[key]
	}
	s.mu.RUnlock()

	all, _ := s.GetAll(ctx, sc)
	if v, ok := all[key]; ok {
		return v
	}
	return Defaults[key]
}

// SetMany upserts multiple settings in one transaction and invalidates the cache.
// Unknown keys are silently ignored.
func (s *Store) SetMany(ctx context.Context, sc Scope, kv map[string]string) error {
	// A write with no scope is REFUSED, where a read merely gets defaults. The
	// asymmetry is deliberate: an unscoped read serves one wrong page, an
	// unscoped write silently edits the operator's own install on behalf of a
	// caller who never named it, and nothing afterwards can tell it apart from
	// an intentional change.
	if !sc.Valid() {
		return errNoScope
	}
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
	s.invalidate(sc)
	return nil
}

// invalidate expires one scope's cache. Only that scope: expiring the whole map
// would make every domain on the install re-query on any single domain's save,
// which on a thirty-client install is thirty cold caches for one edit.
func (s *Store) invalidate(sc Scope) {
	s.mu.Lock()
	delete(s.ttl, sc.key())
	delete(s.cache, sc.key())
	s.mu.Unlock()
}
