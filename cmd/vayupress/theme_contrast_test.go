// SPDX-License-Identifier: Apache-2.0

package main

import (
	"math"
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/settings"
)

func TestContrastRatioKnownValues(t *testing.T) {
	if cr := contrastRatio("#000000", "#ffffff"); math.Abs(cr-21.0) > 0.05 {
		t.Errorf("black/white should be 21:1, got %.2f", cr)
	}
	if cr := contrastRatio("#abcdef", "#abcdef"); math.Abs(cr-1.0) > 0.001 {
		t.Errorf("identical colours should be 1:1, got %.2f", cr)
	}
	// #rgb shorthand must expand identically to #rrggbb.
	if a, b := contrastRatio("#fff", "#000"), contrastRatio("#ffffff", "#000000"); math.Abs(a-b) > 0.001 {
		t.Errorf("#rgb and #rrggbb must agree: %.2f vs %.2f", a, b)
	}
}

func TestDefaultPalettePassesWCAGAA(t *testing.T) {
	// The shipped defaults must clear AA, or the checker would flag its own
	// defaults. Light primary #0f766e and dark primary #2dd4bf are the defaults.
	if w := contrastWarnings("#0f766e", "#2dd4bf"); len(w) != 0 {
		t.Errorf("default palette must pass WCAG AA, got warnings: %v", w)
	}
}

// TestThemeEditorCoversSettingsAllowlist is a drift guard: every key in the
// settings allowlist must appear in the rendered editor — both as an input id and
// in the import key list — so export/import and the editor can never fall out of
// sync with the allowlist as keys are added or removed.
func TestThemeEditorCoversSettingsAllowlist(t *testing.T) {
	// Branding keys are managed out-of-band by the multipart favicon upload
	// handler (POST /admin/theme/favicon), not the JSON Save form, so they are
	// deliberately absent from the form-field / import-key drift guard below.
	outOfBand := map[string]bool{
		settings.KeyBrandFavicon: true,
		// The display timezone is managed on VayuOS → Settings → General (its own
		// data-setting-key select), not the theme editor.
		settings.KeySiteTimezone:     true,
		settings.KeyBrandFaviconType: true,
		// VayuTor onion services — managed on the VayuTor page (/os/tor), not the
		// theme editor. The visit counter is engine-maintained, never a form field.
		settings.KeyTorEnabled:       true,
		settings.KeyTorVisits:        true,
		settings.KeyTorBridges:       true,
		settings.KeyTorPageStats:     true,
		settings.KeyTorPageHits:      true,
		settings.KeyTorOnionLocation: true,
		// Anonymous Tor Space (ADR-0141): the world switch (/os/spaces/toggle) owns
		// these operational keys, and the child API key is engine-generated — never
		// theme-editor form fields.
		settings.KeyTorSpaceEnabled: true,
		settings.KeyTorSpaceAPIKey:  true,
		// The anonymous VayuTalk handle is engine-generated + rotated from the Talk
		// page, never a theme-editor field.
		settings.KeyTalkAnonID: true,
		// Onion-federation opt-in (ADR-0142) is toggled from the Talk page, not the
		// theme editor.
		settings.KeyTalkOnionFederation: true,
		// Feature flags are toggled through the Tools & Plugins panel
		// (POST /os/api/tools/toggle), not the theme editor form.
		settings.KeyFeatureComments:    true,
		settings.KeyFeatureNewsletter:  true,
		settings.KeyFeatureWebmentions: true,
		// The public Sign in / Sign up toggle lives in the VayuOS Members
		// settings (/os/settings/members), not the legacy theme editor.
		settings.KeyMembershipButtons: true,
		// Maintenance mode + its visitor message are managed on the Power &
		// Maintenance page (/os/power, POST /os/api/power/maintenance), not the
		// theme editor.
		settings.KeyMaintenanceMode:    true,
		settings.KeyMaintenanceMessage: true,
		// The search-engine / AI crawler block is a Power & Maintenance switch
		// (/os/power, POST /os/api/power/crawlers), not a theme-editor field.
		settings.KeyBlockCrawlers: true,
		// The feedback inbox is set on the Power & Maintenance page (feedback
		// button), not the theme editor.
		settings.KeyFeedbackEmail: true,
		// Navigation menu is managed through the VayuOS Navigation tab
		// (/os/settings/navigation), not the legacy theme editor.
		settings.KeyNavItems: true,
		// Footer is managed through the VayuOS Footer tab
		// (/os/settings/footer), not the legacy theme editor.
		settings.KeyFooterConfig: true,
		// Contact-form recipient + auto-reply toggle are set in the VayuOS Pages
		// surface, not the legacy theme editor.
		settings.KeyContactEmail:     true,
		settings.KeyContactAutoReply: true,
		// Media alt-text map is managed in the Media library, not the theme editor.
		settings.KeyMediaAlt: true,
		// Hero image is uploaded via the Theme Studio Hero control (multipart),
		// not a text/colour field in the legacy theme editor.
		settings.KeyThemeHeroImage:     true,
		settings.KeyThemeHeroImageType: true,
		// Social/share (OG) image is uploaded via the Theme Studio Site-basics
		// control (multipart), not the legacy theme editor.
		settings.KeyThemeOGImage:     true,
		settings.KeyThemeOGImageType: true,
		// The homepage-hero toggle lives in the Theme Studio Hero group
		// (POST /os/api/settings), not the legacy theme editor.
		settings.KeyHomeHero: true,
		// Business-website mode, template and content are managed in the VayuOS
		// Website studio (/os/website), not the legacy theme editor.
		settings.KeySiteMode:    true,
		settings.KeyBizTemplate: true,
		settings.KeyBizContent:  true,
		// Author bio is edited in the Theme Studio Article-pages group
		// (POST /os/api/settings), not the legacy theme editor.
		settings.KeyAuthorBio: true,
		// Monetization + advertising feature flags are toggled through the
		// Tools & Plugins panel (POST /os/api/tools/toggle), and their config
		// keys are edited in the VayuOS Monetization (/os/monetization) and
		// Advertising (/os/ads) consoles (POST /os/api/settings) — not the
		// legacy theme editor.
		settings.KeyFeaturePayments:  true,
		settings.KeyFeatureAds:       true,
		settings.KeyFeatureGoogleAds: true,
		settings.KeyFeatureAffiliate: true,
		settings.KeyFeatureSponsors:  true,
		// The built-in search engine is toggled in Tools & Plugins, not the theme
		// editor.
		settings.KeyFeatureSearch: true,
		// The Trending & pinned-posts widget is toggled in Tools & Plugins.
		settings.KeyFeatureTrending:         true,
		settings.KeyPayDirectInstructions:   true,
		settings.KeyPayCurrency:             true,
		settings.KeyPaySupportEmail:         true,
		settings.KeyPayPalSandbox:           true,
		settings.KeyBTCPayURL:               true,
		settings.KeyBTCPayStoreID:           true,
		settings.KeyPremiumMailIDPriceCents: true,
		settings.KeyMailIDTerms:             true,
		settings.KeyAdSlotPriceCents:        true,
		settings.KeyAdsenseClient:           true,
		settings.KeyAffiliateDisclosure:     true,
		// The VayuOS console colour theme is set from the topbar theme toggle
		// (POST /os/api/settings), not the legacy public-site theme editor.
		"admin.theme": true,
		// VayuShield DDoS/bot-defence toggles and thresholds are managed in the
		// VayuShield console (/os/shield, POST /os/api/settings), not the legacy
		// theme editor.
		settings.KeyShieldEnabled:        true,
		settings.KeyShieldPoW:            true,
		settings.KeyShieldJS:             true,
		settings.KeyShieldBlock:          true,
		settings.KeyShieldTarpit:         true,
		settings.KeyShieldRateLimit:      true,
		settings.KeyShieldRateRPM:        true,
		settings.KeyShieldBurst:          true,
		settings.KeyShieldLoadShed:       true,
		settings.KeyShieldMaxInFlight:    true,
		settings.KeyShieldAutoBlock:      true,
		settings.KeyShieldJailMinutes:    true,
		settings.KeyShieldUnderAttack:    true,
		settings.KeyShieldUnderAttackRPS: true,
		settings.KeyShieldSurge:          true,
		settings.KeyShieldBehindCDN:      true,
		settings.KeyShieldGroupIPv4:      true,
		settings.KeyShieldObserve:        true,
		// The operator's own allow/deny/route rules live in the same console, in
		// the "Your own rules" band. They are multi-line policy text rather than
		// presentation, so they have no place in a theme bundle: exporting a theme
		// that carried someone's network deny list and importing it elsewhere
		// would move access control between sites as a side effect of a look.
		settings.KeyShieldAllowCIDRs:         true,
		settings.KeyShieldDenyCIDRs:          true,
		settings.KeyShieldAllowCountries:     true,
		settings.KeyShieldDenyCountries:      true,
		settings.KeyShieldChallengeCountries: true,
		settings.KeyShieldRouteCosts:         true,
		settings.KeyShieldClusterPeers:       true,
		settings.KeyShieldClusterNode:        true,
		// Which third-party network lists an operator has opted into. Same
		// reasoning as the rules above, plus one of its own: enabling a feed
		// carries that publisher's licence terms, and a theme import must never
		// accept somebody else's terms on this operator's behalf.
		settings.KeyShieldIntelFeeds: true,
		// The VayuAnalytics engagement beacon is toggled in Tools & Plugins /
		// the Analytics console, not the theme editor.
		settings.KeyAnalyticsBeacon: true,
		// VayuVeil's activate/deactivate switch (ADR-0150) lives on its own
		// console. It is emphatically not presentation, and a theme bundle that
		// carried it would switch a privacy subsystem's reporting on or off on
		// another install as a side effect of importing a look — the same class of
		// mistake as carrying somebody's network deny list in a theme.
		settings.KeyVeilEnabled: true,
	}
	page := themeEditorPage(map[string]string{}, "NORMAL", "test-nonce", "")
	for key := range settings.AllKeys {
		if outOfBand[key] {
			continue
		}
		if !strings.Contains(page, `id="`+key+`"`) {
			t.Errorf("settings key %q has no input field in the theme editor", key)
		}
		if !strings.Contains(page, `'`+key+`'`) {
			t.Errorf("settings key %q is missing from the import/save key list", key)
		}
	}
	// The export and import sides must agree on the bundle schema version.
	if themeExportVersion != 1 {
		t.Errorf("import JS pins vayupress_theme===1; bump it in lockstep with themeExportVersion (%d)", themeExportVersion)
	}
}

func TestContrastWarningsFlagLowContrast(t *testing.T) {
	// A near-white light primary on the light background must warn; a bright
	// dark primary on the dark background must not.
	w := contrastWarnings("#fefefe", "#ffffff")
	if len(w) == 0 {
		t.Error("expected a contrast warning for near-white light primary")
	}
	if w := contrastWarnings("", ""); len(w) != 0 {
		t.Errorf("empty colours should produce no warnings, got: %v", w)
	}
}
