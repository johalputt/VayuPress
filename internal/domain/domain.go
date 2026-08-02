// SPDX-License-Identifier: Apache-2.0

// Package domain is the VayuDomains registry: the single source of truth for
// every hostname this one binary serves. Stage 1 is deliberately read-mostly —
// it resolves an incoming Host header to a registered domain and lets the
// operator manage the list — while content, mail and member scoping land in
// later stages. The primary domain (the original single-host install) is seeded
// from config at startup, so an existing site is byte-identical: the registry
// describes what already exists rather than changing how it is served.
package domain

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/johalputt/vayupress/internal/logging"
)

// Site types. These mirror the global site.mode vocabulary so the primary
// domain's type is a faithful copy of the existing install, and add the
// multi-domain-only kinds that later stages fill in.
const (
	SiteBlog            = "blog"             // blog at "/" (historic default; empty is treated as this)
	SiteBusiness        = "business"         // business site at "/", blog at blog.<host>
	SiteBusinessSubpath = "business_subpath" // business at "/", blog at /blog
	SiteStatic          = "static"           // a static bundle (Stage 2)
	SiteMailOnly        = "mail_only"        // no public site; branded mail only (Stage 3)
)

// TLS lifecycle states recorded (never enforced) by Stage 1.
const (
	TLSPrimary = "primary" // managed outside the registry (e.g. the existing certbot cert)
	TLSPending = "pending"
	TLSActive  = "active"
	TLSFailed  = "failed"
)

// Domain statuses. A disabled domain stays in the table (auditable, reversible)
// but host resolution treats it as unknown.
const (
	StatusActive   = "active"
	StatusDisabled = "disabled"
)

// Sync states — the manual gate between "registered" and "provisioned" (P5).
// The out-of-process TLS+nginx helper acts only on approved secondaries; a
// newly created domain starts on hold so adding it never provisions anything
// until the operator explicitly presses "Sync now" (VayuOS → Domains) or runs
// `vayupress domains sync <host>`.
const (
	SyncApproved = "approved" // helper may provision and keep maintaining it
	SyncHold     = "hold"     // registered only; helper must skip it
)

// ErrNotFound is returned when no registered, active domain matches a host.
var ErrNotFound = errors.New("domain: host not registered")

// Domain is one registered hostname and how this binary should serve it.
type Domain struct {
	ID          string    `json:"id"`
	Host        string    `json:"host"`
	SiteType    string    `json:"site_type"`
	MailEnabled bool      `json:"mail_enabled"`
	TLSState    string    `json:"tls_state"`
	SyncState   string    `json:"sync_state"`
	ConfigJSON  string    `json:"config_json,omitempty"`
	IsPrimary   bool      `json:"is_primary"`
	Status      string    `json:"status"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// Brand is the per-domain public identity a secondary domain overlays on the
// primary site's settings so it presents as its own site: name, tagline,
// description, accent colours and the browser theme-colour. Every field is
// optional — a blank field falls back to the primary's value — so a domain can
// re-brand just its name and inherit everything else. It is stored inside the
// domain's config_json under the "brand" key (see Domain.Brand); the primary
// domain never carries one, keeping a single-host install byte-identical.
type Brand struct {
	SiteName    string `json:"site_name,omitempty"`
	Tagline     string `json:"tagline,omitempty"`
	Description string `json:"description,omitempty"`
	AccentLight string `json:"accent_light,omitempty"`
	AccentDark  string `json:"accent_dark,omitempty"`
	ThemeColor  string `json:"theme_color,omitempty"`
}

// Empty reports whether the brand carries no overrides at all, so callers can
// skip the overlay entirely and serve the primary settings unchanged.
func (b Brand) Empty() bool {
	return b.SiteName == "" && b.Tagline == "" && b.Description == "" &&
		b.AccentLight == "" && b.AccentDark == "" && b.ThemeColor == ""
}

// SiteConfig is a domain's website override: which of the four site modes its
// root serves, and the business-template selection and content that go with it.
//
// It exists because mode, template and content were install-wide settings keys,
// so one custom bundle and one template served every registered domain — a
// studio could host one hand-built site, never thirty. An empty Mode means
// "inherit the install's own setting", which is what every existing row carries,
// so a single-domain install is byte-identical.
type SiteConfig struct {
	Mode     string `json:"mode,omitempty"`     // "" = inherit; blog | business | business_subpath | custom
	Template string `json:"template,omitempty"` // business template key
	Content  string `json:"content,omitempty"`  // business content, as stored by bizsite
}

// Empty reports whether this carries no website override at all.
func (s SiteConfig) Empty() bool {
	return s.Mode == "" && s.Template == "" && s.Content == ""
}

// domainConfig is the shape of the domains.config_json blob. It is a small
// envelope so stages can add sibling keys without disturbing each other.
//
// Sibling keys only stay undisturbed if writers MERGE. The first version of the
// brand writer marshalled a fresh domainConfig{Brand: b}, which silently dropped
// every other key in the blob — so the moment a second key existed, saving a
// brand would erase the website override (and, later, an operator-set mailbox
// allowance). Every writer here is therefore read-modify-write, and there is no
// whole-blob encoder left to reach for by mistake.
type domainConfig struct {
	Brand Brand      `json:"brand"`
	Site  SiteConfig `json:"site,omitempty"`
}

// decodeConfig parses a config_json blob. Malformed JSON yields a zero envelope
// rather than an error: this blob is presentational and operational overrides,
// and must never break serving.
func decodeConfig(raw string) domainConfig {
	var c domainConfig
	if strings.TrimSpace(raw) == "" {
		return c
	}
	if err := json.Unmarshal([]byte(raw), &c); err != nil {
		return domainConfig{}
	}
	return c
}

// encodeConfig serialises an envelope, collapsing a wholly-empty one to "" so a
// cleared override stores nothing rather than an empty object — which keeps the
// short-circuits in Brand() and Site() intact.
func encodeConfig(c domainConfig) (string, error) {
	if c.Brand.Empty() && c.Site.Empty() {
		return "", nil
	}
	out, err := json.Marshal(c)
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// Site decodes this domain's website override. ok is false when the domain
// carries none, so the caller falls back to the install-wide setting exactly as
// it did before per-domain sites existed.
func (d Domain) Site() (SiteConfig, bool) {
	s := decodeConfig(d.ConfigJSON).Site
	if s.Empty() {
		return SiteConfig{}, false
	}
	return s, true
}

// EncodeBrandConfigInto merges a Brand into an EXISTING config_json, preserving
// every sibling key. Passing "" is the correct call for a domain that has no
// config yet.
func EncodeBrandConfigInto(existing string, b Brand) (string, error) {
	c := decodeConfig(existing)
	c.Brand = b
	return encodeConfig(c)
}

// EncodeSiteConfigInto merges a SiteConfig into an existing config_json,
// preserving every sibling key — notably the brand, which a client may edit.
func EncodeSiteConfigInto(existing string, s SiteConfig) (string, error) {
	c := decodeConfig(existing)
	c.Site = s
	return encodeConfig(c)
}

// Brand decodes this domain's stored branding overrides. ok is false when the
// domain has no config_json or no brand overrides, so the caller serves the
// primary settings unchanged. Malformed JSON is treated as "no brand" rather
// than an error: branding is presentational and must never break serving.
func (d Domain) Brand() (Brand, bool) {
	b := decodeConfig(d.ConfigJSON).Brand
	if b.Empty() {
		return Brand{}, false
	}
	return b, true
}

// EffectiveSyncState maps an empty sync_state to approved. Rows written before
// migration 064 (or by an out-of-band editor) carry "" — treating that as
// approved preserves the pre-gate behaviour for domains that were already
// provisioned, exactly like the migration's backfill default.
func (d Domain) EffectiveSyncState() string {
	if d.SyncState == SyncHold {
		return SyncHold
	}
	return SyncApproved
}

// IsSyncApproved reports whether the out-of-process provisioning helper may
// act on this domain.
func (d Domain) IsSyncApproved() bool { return d.EffectiveSyncState() == SyncApproved }

// EffectiveSiteType maps an empty or unknown site_type to the blog default,
// exactly as the historic site.mode handling does.
func (d Domain) EffectiveSiteType() string {
	switch d.SiteType {
	case SiteBusiness, SiteBusinessSubpath, SiteStatic, SiteMailOnly:
		return d.SiteType
	default:
		return SiteBlog
	}
}

// cacheTTL bounds how long a resolved snapshot is trusted before a refresh.
// Host resolution runs on every public request, so the hot path must not touch
// SQLite; writes invalidate the cache immediately, and the TTL only bounds
// staleness from an out-of-band DB edit.
const cacheTTL = 30 * time.Second

// Registry is the thread-safe domain store with an in-process snapshot cache
// keyed by lower-cased host.
type Registry struct {
	db  *sql.DB
	rdb *sql.DB // read pool; falls back to db when nil

	mu      sync.RWMutex
	byHost  map[string]Domain
	primary Domain
	hasPrim bool
	hasSec  bool // any non-primary domain registered — precomputed so the
	// per-request HasSecondaries gate (hit on every public blog page and VayuOS
	// request) is a single cached bool read, never a map scan or DB query.
	ttl time.Time
}

// New constructs a Registry. rdb is the read-only pool (may be nil, in which
// case reads use db).
func New(db, rdb *sql.DB) *Registry {
	return &Registry{db: db, rdb: rdb, byHost: make(map[string]Domain)}
}

func (r *Registry) reader() *sql.DB {
	if r.rdb != nil {
		return r.rdb
	}
	return r.db
}

// NormalizeHost lower-cases a Host header value and strips the port and any
// trailing dot, yielding the bare hostname used as the registry key.
func NormalizeHost(host string) string {
	host = strings.TrimSpace(host)
	if host == "" {
		return ""
	}
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.TrimSuffix(host, ".")
	return strings.ToLower(host)
}

// refresh reloads the whole registry into the cache. The table is small (one
// row per served hostname), so a full scan is cheaper than per-host queries.
func (r *Registry) refresh(ctx context.Context) error {
	rows, err := r.reader().QueryContext(ctx, `SELECT id,host,site_type,mail_enabled,tls_state,sync_state,config_json,is_primary,status,created_at,updated_at FROM domains`)
	if err != nil {
		return err
	}
	defer rows.Close()

	byHost := make(map[string]Domain)
	var primary Domain
	var hasPrim, hasSec bool
	for rows.Next() {
		var d Domain
		var mail, prim int
		if err := rows.Scan(&d.ID, &d.Host, &d.SiteType, &mail, &d.TLSState, &d.SyncState, &d.ConfigJSON, &prim, &d.Status, &d.CreatedAt, &d.UpdatedAt); err != nil {
			return err
		}
		d.MailEnabled = mail != 0
		d.IsPrimary = prim != 0
		d.Host = NormalizeHost(d.Host)
		byHost[d.Host] = d
		if d.IsPrimary {
			primary = d
			hasPrim = true
		} else {
			hasSec = true
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}

	r.mu.Lock()
	r.byHost = byHost
	r.primary = primary
	r.hasPrim = hasPrim
	r.hasSec = hasSec
	r.ttl = time.Now().Add(cacheTTL)
	r.mu.Unlock()
	return nil
}

// ensureFresh refreshes the cache when the TTL has expired. Freshness is purely
// time-based: an empty registry (localhost/dev, before seeding) is a valid,
// cacheable state, so the hot path must not re-query the DB on every request
// just because there are no rows yet.
func (r *Registry) ensureFresh(ctx context.Context) {
	r.mu.RLock()
	fresh := !r.ttl.IsZero() && time.Now().Before(r.ttl)
	r.mu.RUnlock()
	if fresh {
		return
	}
	if err := r.refresh(ctx); err != nil {
		logging.LogError("domains", "registry refresh failed", err.Error())
	}
}

// invalidate forces the next read to reload from the DB.
func (r *Registry) invalidate() {
	r.mu.Lock()
	r.ttl = time.Time{}
	r.mu.Unlock()
}

// Resolve returns the active domain registered for host, or ErrNotFound. A
// disabled domain resolves as not-found so callers fall back to primary
// behaviour without a special case.
func (r *Registry) Resolve(ctx context.Context, host string) (Domain, error) {
	key := NormalizeHost(host)
	if key == "" {
		return Domain{}, ErrNotFound
	}
	r.ensureFresh(ctx)
	r.mu.RLock()
	d, ok := r.byHost[key]
	r.mu.RUnlock()
	if !ok || d.Status != StatusActive {
		return Domain{}, ErrNotFound
	}
	return d, nil
}

// ByID returns one registered domain by its opaque id.
//
// Exported so a caller holding an identifier — a client account's binding, an
// admin action on a single row — can resolve it without listing every domain
// and filtering, which is both wasteful and easy to get subtly wrong.
func (r *Registry) ByID(ctx context.Context, id string) (Domain, error) {
	return r.get(ctx, id)
}

// Primary returns the primary domain (the original install). ok is false when
// the registry has not been seeded yet.
func (r *Registry) Primary(ctx context.Context) (Domain, bool) {
	r.ensureFresh(ctx)
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.primary, r.hasPrim
}

// List returns every registered domain (active and disabled), primary first
// then alphabetically by host, for the admin console.
func (r *Registry) List(ctx context.Context) ([]Domain, error) {
	rows, err := r.reader().QueryContext(ctx, `SELECT id,host,site_type,mail_enabled,tls_state,sync_state,config_json,is_primary,status,created_at,updated_at FROM domains ORDER BY is_primary DESC, host ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Domain
	for rows.Next() {
		var d Domain
		var mail, prim int
		if err := rows.Scan(&d.ID, &d.Host, &d.SiteType, &mail, &d.TLSState, &d.SyncState, &d.ConfigJSON, &prim, &d.Status, &d.CreatedAt, &d.UpdatedAt); err != nil {
			return nil, err
		}
		d.MailEnabled = mail != 0
		d.IsPrimary = prim != 0
		out = append(out, d)
	}
	return out, rows.Err()
}

// Count returns the number of registered domains.
func (r *Registry) Count(ctx context.Context) (int, error) {
	var n int
	err := r.reader().QueryRowContext(ctx, `SELECT COUNT(1) FROM domains`).Scan(&n)
	return n, err
}

// HasSecondaries reports whether any non-primary domain is registered. It reads
// the cached snapshot, so it is cheap enough to gate the public hot path: when
// it is false (a plain single-domain install) the per-domain content code paths
// are skipped entirely and behaviour is byte-identical.
func (r *Registry) HasSecondaries(ctx context.Context) bool {
	r.ensureFresh(ctx)
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.hasSec // precomputed at refresh — O(1), no scan (hot path)
}
