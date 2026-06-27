// Package ads implements VayuPress's activation-gated advertising surface: an
// operator-managed catalogue of ad "slots" and a strict-CSP-safe renderer that
// injects them into public pages.
//
// Design (ADR-0091): advertising is OFF by default and never appears until the
// operator switches the Ads module on (feature.ads) AND creates an enabled
// slot. There are three creative kinds:
//
//   - image   — a same-origin or absolute image linked to a destination URL.
//     Fully CSP-safe; the link is marked rel="sponsored nofollow".
//   - html    — an operator-authored HTML creative, sanitised before emit, for
//     direct-sold/house ads that need richer markup.
//   - adsense — a Google AdSense unit. Only renders when the Google Ads module
//     is additionally enabled and a publisher id is configured; the inline
//     activation call is nonce-gated and the loader is emitted once per page by
//     the caller, which also widens that page's CSP to admit Google's origins.
//
// This package owns persistence + rendering only; gating decisions (which
// feature flags are on) are made by the caller and passed in via RenderConfig.
package ads

import (
	"context"
	"crypto/rand"
	"database/sql"
	"fmt"
	"html"
	"strings"
	"time"
)

// Placements — where on a public page a slot renders.
const (
	PlacementHeader    = "header"     // top of the page, under the nav
	PlacementAbovePost = "above_post" // before the article body
	PlacementBelowPost = "below_post" // after the article body
	PlacementSidebar   = "sidebar"    // aside column (theme-dependent)
	PlacementFooter    = "footer"     // above the site footer
)

// Creative kinds.
const (
	KindImage   = "image"
	KindHTML    = "html"
	KindAdSense = "adsense"
)

// ValidPlacement reports whether p is a known placement.
func ValidPlacement(p string) bool {
	switch p {
	case PlacementHeader, PlacementAbovePost, PlacementBelowPost, PlacementSidebar, PlacementFooter:
		return true
	}
	return false
}

// Placements returns the ordered list of placements for admin UIs.
func Placements() []struct{ ID, Label string } {
	return []struct{ ID, Label string }{
		{PlacementHeader, "Header (top of page)"},
		{PlacementAbovePost, "Above the post"},
		{PlacementBelowPost, "Below the post"},
		{PlacementSidebar, "Sidebar"},
		{PlacementFooter, "Footer"},
	}
}

// ValidKind reports whether k is a known creative kind.
func ValidKind(k string) bool {
	return k == KindImage || k == KindHTML || k == KindAdSense
}

// Slot is one advertising placement creative.
type Slot struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Placement string    `json:"placement"`
	Kind      string    `json:"kind"`
	ImageURL  string    `json:"image_url,omitempty"`
	LinkURL   string    `json:"link_url,omitempty"`
	AltText   string    `json:"alt_text,omitempty"`
	HTML      string    `json:"html,omitempty"` // html creative, or AdSense unit id for adsense kind
	Enabled   bool      `json:"enabled"`
	Sort      int       `json:"sort"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Store persists ad slots in the ad_slots table (migration 043).
type Store struct{ db *sql.DB }

// New creates a Store backed by db.
func New(db *sql.DB) *Store { return &Store{db: db} }

const slotCols = `id,name,placement,kind,image_url,link_url,alt_text,html,enabled,sort,created_at,updated_at`

// SlotInput carries the editable fields of a slot.
type SlotInput struct {
	Name      string
	Placement string
	Kind      string
	ImageURL  string
	LinkURL   string
	AltText   string
	HTML      string
	Sort      int
	Enabled   bool
}

// Create inserts a new slot and returns it.
func (s *Store) Create(ctx context.Context, in SlotInput) (*Slot, error) {
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return nil, fmt.Errorf("slot name is required")
	}
	if !ValidPlacement(in.Placement) {
		return nil, fmt.Errorf("invalid placement %q", in.Placement)
	}
	if !ValidKind(in.Kind) {
		return nil, fmt.Errorf("invalid creative kind %q", in.Kind)
	}
	id := "ad_" + randHex(8)
	en := boolToInt(in.Enabled)
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO ad_slots(id,name,placement,kind,image_url,link_url,alt_text,html,enabled,sort) VALUES(?,?,?,?,?,?,?,?,?,?)`,
		id, name, in.Placement, in.Kind, strings.TrimSpace(in.ImageURL), strings.TrimSpace(in.LinkURL),
		strings.TrimSpace(in.AltText), in.HTML, en, in.Sort); err != nil {
		return nil, fmt.Errorf("create slot: %w", err)
	}
	return s.GetByID(ctx, id)
}

// Update edits an existing slot's fields by id.
func (s *Store) Update(ctx context.Context, id string, in SlotInput) error {
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return fmt.Errorf("slot name is required")
	}
	if !ValidPlacement(in.Placement) {
		return fmt.Errorf("invalid placement %q", in.Placement)
	}
	if !ValidKind(in.Kind) {
		return fmt.Errorf("invalid creative kind %q", in.Kind)
	}
	res, err := s.db.ExecContext(ctx,
		`UPDATE ad_slots SET name=?,placement=?,kind=?,image_url=?,link_url=?,alt_text=?,html=?,enabled=?,sort=?,updated_at=? WHERE id=?`,
		name, in.Placement, in.Kind, strings.TrimSpace(in.ImageURL), strings.TrimSpace(in.LinkURL),
		strings.TrimSpace(in.AltText), in.HTML, boolToInt(in.Enabled), in.Sort, time.Now().UTC(), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("slot not found")
	}
	return nil
}

// SetEnabled toggles a slot's enabled flag.
func (s *Store) SetEnabled(ctx context.Context, id string, enabled bool) error {
	res, err := s.db.ExecContext(ctx, `UPDATE ad_slots SET enabled=?,updated_at=? WHERE id=?`, boolToInt(enabled), time.Now().UTC(), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("slot not found")
	}
	return nil
}

// Delete removes a slot.
func (s *Store) Delete(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM ad_slots WHERE id=?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("slot not found")
	}
	return nil
}

// GetByID returns one slot.
func (s *Store) GetByID(ctx context.Context, id string) (*Slot, error) {
	return scanSlot(s.db.QueryRowContext(ctx, `SELECT `+slotCols+` FROM ad_slots WHERE id=?`, id))
}

// List returns every slot ordered for the admin console.
func (s *Store) List(ctx context.Context) ([]Slot, error) {
	return s.query(ctx, `SELECT `+slotCols+` FROM ad_slots ORDER BY placement ASC, sort ASC, created_at ASC`)
}

// EnabledByPlacement returns the enabled slots for a placement, render-ordered.
func (s *Store) EnabledByPlacement(ctx context.Context, placement string) ([]Slot, error) {
	return s.query(ctx, `SELECT `+slotCols+` FROM ad_slots WHERE placement=? AND enabled=1 ORDER BY sort ASC, created_at ASC`, placement)
}

func (s *Store) query(ctx context.Context, q string, args ...interface{}) ([]Slot, error) {
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Slot
	for rows.Next() {
		sl, err := scanSlot(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *sl)
	}
	return out, rows.Err()
}

func scanSlot(sc interface{ Scan(...interface{}) error }) (*Slot, error) {
	var sl Slot
	var enabled int
	if err := sc.Scan(&sl.ID, &sl.Name, &sl.Placement, &sl.Kind, &sl.ImageURL, &sl.LinkURL,
		&sl.AltText, &sl.HTML, &enabled, &sl.Sort, &sl.CreatedAt, &sl.UpdatedAt); err != nil {
		return nil, err
	}
	sl.Enabled = enabled != 0
	return &sl, nil
}

// ── Rendering ─────────────────────────────────────────────────────────────────

// RenderConfig carries the per-request gating + escaping context the renderer
// needs. The caller decides what is enabled; the renderer never reads settings.
type RenderConfig struct {
	// GoogleAdsEnabled is true when the Google Ads module is on and a publisher
	// id is configured; only then are adsense-kind slots emitted.
	GoogleAdsEnabled bool
	// AdsenseClient is the AdSense publisher id ("ca-pub-…").
	AdsenseClient string
	// Nonce is the per-request CSP nonce for the inline AdSense activation call.
	Nonce string
	// Sanitize sanitises operator HTML creatives (typically bluemonday). When
	// nil, html-kind creatives are dropped (fail closed).
	Sanitize func(string) string
}

// Render renders a list of slots (already filtered to one placement) into a
// single CSP-safe HTML fragment, or "" when nothing renders. Use HasAdSense to
// decide whether the page must also emit the loader + widen its CSP.
func Render(slots []Slot, cfg RenderConfig) string {
	var b strings.Builder
	for i := range slots {
		unit := renderSlot(&slots[i], cfg)
		if unit != "" {
			b.WriteString(`<div class="vp-ad-slot" data-ad-placement="` + html.EscapeString(slots[i].Placement) + `">` + unit + `</div>`)
		}
	}
	if b.Len() == 0 {
		return ""
	}
	return `<div class="vp-ads" aria-label="Advertisement">` + b.String() + `</div>`
}

// HasAdSense reports whether any of the slots would render an AdSense unit under
// cfg, so the caller can emit the loader once and widen the page CSP.
func HasAdSense(slots []Slot, cfg RenderConfig) bool {
	if !cfg.GoogleAdsEnabled || strings.TrimSpace(cfg.AdsenseClient) == "" {
		return false
	}
	for i := range slots {
		if slots[i].Kind == KindAdSense {
			return true
		}
	}
	return false
}

// AdSenseLoader returns the single async loader script tag for AdSense, keyed to
// the publisher id. The external origin is admitted by the widened ad CSP; the
// tag carries the page nonce so it also satisfies a nonce-only script-src.
func AdSenseLoader(nonce, client string) string {
	c := adsenseClient(client)
	if c == "" {
		return ""
	}
	return `<script async nonce="` + html.EscapeString(nonce) +
		`" src="https://pagead2.googlesyndication.com/pagead/js/adsbygoogle.js?client=` + html.EscapeString(c) +
		`" crossorigin="anonymous"></script>`
}

// renderSlot renders one creative, or "" when it cannot render safely.
func renderSlot(sl *Slot, cfg RenderConfig) string {
	switch sl.Kind {
	case KindImage:
		img := safeURL(sl.ImageURL)
		if img == "" {
			return ""
		}
		imgTag := `<img class="vp-ad__img" src="` + html.EscapeString(img) + `" alt="` + html.EscapeString(sl.AltText) + `" loading="lazy" decoding="async">`
		link := safeURL(sl.LinkURL)
		if link == "" {
			return imgTag
		}
		return `<a class="vp-ad__link" href="` + html.EscapeString(link) + `" rel="sponsored nofollow noopener" target="_blank">` + imgTag + `</a>`
	case KindHTML:
		if cfg.Sanitize == nil {
			return "" // fail closed: never emit unsanitised operator HTML
		}
		clean := strings.TrimSpace(cfg.Sanitize(sl.HTML))
		if clean == "" {
			return ""
		}
		return `<div class="vp-ad__html">` + clean + `</div>`
	case KindAdSense:
		if !cfg.GoogleAdsEnabled {
			return ""
		}
		client := adsenseClient(cfg.AdsenseClient)
		if client == "" {
			return ""
		}
		slotAttr := ""
		if unit := strings.TrimSpace(sl.HTML); unit != "" {
			slotAttr = ` data-ad-slot="` + html.EscapeString(unit) + `"`
		}
		return `<ins class="adsbygoogle vp-ad__adsense" style="display:block" data-ad-client="` + html.EscapeString(client) + `"` + slotAttr +
			` data-ad-format="auto" data-full-width-responsive="true"></ins>` +
			`<script nonce="` + html.EscapeString(cfg.Nonce) + `">(adsbygoogle = window.adsbygoogle || []).push({});</script>`
	}
	return ""
}

// adsenseClient trims and validates a publisher id, returning "" if it does not
// look like a "ca-pub-…" id so a malformed value never reaches the page.
func adsenseClient(c string) string {
	c = strings.TrimSpace(c)
	if !strings.HasPrefix(c, "ca-pub-") {
		return ""
	}
	for _, r := range c[len("ca-pub-"):] {
		if r < '0' || r > '9' {
			return ""
		}
	}
	return c
}

// safeURL allows only same-origin (leading "/") or http(s) absolute URLs,
// returning "" for anything else (e.g. javascript:, data:) so a crafted creative
// URL can never inject script.
func safeURL(u string) string {
	u = strings.TrimSpace(u)
	if u == "" {
		return ""
	}
	if strings.HasPrefix(u, "/") && !strings.HasPrefix(u, "//") {
		return u
	}
	lower := strings.ToLower(u)
	if strings.HasPrefix(lower, "https://") || strings.HasPrefix(lower, "http://") {
		return u
	}
	return ""
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func randHex(n int) string {
	const hexd = "0123456789abcdef"
	b := make([]byte, n)
	_, _ = rand.Read(b)
	out := make([]byte, n*2)
	for i, v := range b {
		out[i*2] = hexd[v>>4]
		out[i*2+1] = hexd[v&0x0f]
	}
	return string(out)
}
