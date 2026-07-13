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

// ErrNotFound is returned when no registered, active domain matches a host.
var ErrNotFound = errors.New("domain: host not registered")

// Domain is one registered hostname and how this binary should serve it.
type Domain struct {
	ID          string    `json:"id"`
	Host        string    `json:"host"`
	SiteType    string    `json:"site_type"`
	MailEnabled bool      `json:"mail_enabled"`
	TLSState    string    `json:"tls_state"`
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

// domainConfig is the shape of the domains.config_json blob. It is intentionally
// a small envelope so later stages can add sibling keys without disturbing the
// brand overlay.
type domainConfig struct {
	Brand Brand `json:"brand"`
}

// Brand decodes this domain's stored branding overrides. ok is false when the
// domain has no config_json or no brand overrides, so the caller serves the
// primary settings unchanged. Malformed JSON is treated as "no brand" rather
// than an error: branding is presentational and must never break serving.
func (d Domain) Brand() (Brand, bool) {
	if strings.TrimSpace(d.ConfigJSON) == "" {
		return Brand{}, false
	}
	var c domainConfig
	if err := json.Unmarshal([]byte(d.ConfigJSON), &c); err != nil {
		return Brand{}, false
	}
	if c.Brand.Empty() {
		return Brand{}, false
	}
	return c.Brand, true
}

// EncodeBrandConfig serialises a Brand into the config_json envelope for
// storage. An empty brand yields "" so a cleared brand stores nothing rather
// than an empty object, keeping the resolve path's Brand() short-circuit intact.
func EncodeBrandConfig(b Brand) (string, error) {
	if b.Empty() {
		return "", nil
	}
	out, err := json.Marshal(domainConfig{Brand: b})
	if err != nil {
		return "", err
	}
	return string(out), nil
}

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
	rows, err := r.reader().QueryContext(ctx, `SELECT id,host,site_type,mail_enabled,tls_state,config_json,is_primary,status,created_at,updated_at FROM domains`)
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
		if err := rows.Scan(&d.ID, &d.Host, &d.SiteType, &mail, &d.TLSState, &d.ConfigJSON, &prim, &d.Status, &d.CreatedAt, &d.UpdatedAt); err != nil {
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
	rows, err := r.reader().QueryContext(ctx, `SELECT id,host,site_type,mail_enabled,tls_state,config_json,is_primary,status,created_at,updated_at FROM domains ORDER BY is_primary DESC, host ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Domain
	for rows.Next() {
		var d Domain
		var mail, prim int
		if err := rows.Scan(&d.ID, &d.Host, &d.SiteType, &mail, &d.TLSState, &d.ConfigJSON, &prim, &d.Status, &d.CreatedAt, &d.UpdatedAt); err != nil {
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
