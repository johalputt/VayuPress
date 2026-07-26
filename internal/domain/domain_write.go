// SPDX-License-Identifier: Apache-2.0

package domain

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
)

// newID mints a 24-hex-character opaque identifier, matching the scheme other
// VayuPress stores use.
func newID() string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// validSiteType reports whether t is a site type the registry accepts.
func validSiteType(t string) bool {
	switch t {
	case SiteBlog, SiteBusiness, SiteBusinessSubpath, SiteStatic, SiteMailOnly:
		return true
	default:
		return false
	}
}

// EnsurePrimary seeds (or repairs) the single primary domain row from the
// install's configured host. It is idempotent and safe to call on every boot:
//   - no primary yet, host free      → insert the primary row.
//   - primary exists on a DIFFERENT host (operator changed DOMAIN) → move the
//     primary flag to the configured host, inserting it if missing.
//   - primary already on this host    → no-op.
//
// The seeded row carries site_type = the current global site.mode so the
// primary domain is a faithful description of the existing install; johal.in is
// therefore byte-identical. tls_state is 'primary' (its certificate is managed
// outside the registry — the existing certbot cert).
func (r *Registry) EnsurePrimary(ctx context.Context, host, siteType string) error {
	host = NormalizeHost(host)
	if host == "" {
		return fmt.Errorf("domain: cannot seed primary with empty host")
	}
	if !validSiteType(siteType) {
		siteType = SiteBlog
	}
	defer r.invalidate()

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Is there already a primary, and on which host?
	var curHost string
	err = tx.QueryRowContext(ctx, `SELECT host FROM domains WHERE is_primary=1 LIMIT 1`).Scan(&curHost)
	switch {
	case err == nil && NormalizeHost(curHost) == host:
		// Primary already correct — keep its operator-tuned fields, only refresh
		// site_type to track the live site.mode.
		if _, err := tx.ExecContext(ctx, `UPDATE domains SET site_type=?,tls_state=?,status=?,updated_at=CURRENT_TIMESTAMP WHERE host=?`, siteType, TLSPrimary, StatusActive, host); err != nil {
			return err
		}
		return tx.Commit()
	case err == nil:
		// A primary exists on a different host: demote it, then promote/insert
		// the configured host as the new primary.
		if _, err := tx.ExecContext(ctx, `UPDATE domains SET is_primary=0,updated_at=CURRENT_TIMESTAMP WHERE is_primary=1`); err != nil {
			return err
		}
	}

	// No primary (or we just demoted the old one): upsert the configured host as
	// the primary. ON CONFLICT handles the case where the host already exists as
	// a non-primary row.
	id := newID()
	_, err = tx.ExecContext(ctx, `INSERT INTO domains(id,host,site_type,mail_enabled,tls_state,config_json,is_primary,status) VALUES(?,?,?,0,?,'',1,?) ON CONFLICT(host) DO UPDATE SET is_primary=1,site_type=excluded.site_type,tls_state=excluded.tls_state,status=excluded.status,updated_at=CURRENT_TIMESTAMP`, id, host, siteType, TLSPrimary, StatusActive)
	if err != nil {
		return err
	}
	return tx.Commit()
}

// Create registers a new secondary domain. The host is normalised and must be
// unique and not equal to an existing row; is_primary is always 0 here (the
// primary is owned by EnsurePrimary). The row starts on sync_state=hold: adding
// a domain only registers it — nothing is provisioned until the operator
// approves the sync explicitly (P5 manual sync gate).
func (r *Registry) Create(ctx context.Context, host, siteType string, mailEnabled bool) (Domain, error) {
	host = NormalizeHost(host)
	if host == "" {
		return Domain{}, fmt.Errorf("domain: host is required")
	}
	if !validSiteType(siteType) {
		return Domain{}, fmt.Errorf("domain: invalid site type %q", siteType)
	}
	defer r.invalidate()

	id := newID()
	mail := 0
	if mailEnabled {
		mail = 1
	}
	_, err := r.db.ExecContext(ctx, `INSERT INTO domains(id,host,site_type,mail_enabled,tls_state,sync_state,config_json,is_primary,status) VALUES(?,?,?,?,?,?,'',0,?)`, id, host, siteType, mail, TLSPending, SyncHold, StatusActive)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return Domain{}, fmt.Errorf("domain: host %q already registered", host)
		}
		return Domain{}, err
	}
	d, err := r.get(ctx, id)
	return d, err
}

// Update changes the mutable fields of a secondary domain. The primary domain's
// site_type is owned by EnsurePrimary (it tracks the global site.mode), so this
// refuses to edit the primary row.
func (r *Registry) Update(ctx context.Context, id, siteType string, mailEnabled bool) (Domain, error) {
	if !validSiteType(siteType) {
		return Domain{}, fmt.Errorf("domain: invalid site type %q", siteType)
	}
	cur, err := r.get(ctx, id)
	if err != nil {
		return Domain{}, err
	}
	if cur.IsPrimary {
		return Domain{}, fmt.Errorf("domain: the primary domain is managed from Website settings")
	}
	defer r.invalidate()
	mail := 0
	if mailEnabled {
		mail = 1
	}
	if _, err := r.db.ExecContext(ctx, `UPDATE domains SET site_type=?,mail_enabled=?,updated_at=CURRENT_TIMESTAMP WHERE id=?`, siteType, mail, id); err != nil {
		return Domain{}, err
	}
	return r.get(ctx, id)
}

// SetStatus enables or disables a secondary domain. The primary cannot be
// disabled — doing so would take the whole install offline.
func (r *Registry) SetStatus(ctx context.Context, id, status string) error {
	if status != StatusActive && status != StatusDisabled {
		return fmt.Errorf("domain: invalid status %q", status)
	}
	cur, err := r.get(ctx, id)
	if err != nil {
		return err
	}
	if cur.IsPrimary && status != StatusActive {
		return fmt.Errorf("domain: the primary domain cannot be disabled")
	}
	defer r.invalidate()
	_, err = r.db.ExecContext(ctx, `UPDATE domains SET status=?,updated_at=CURRENT_TIMESTAMP WHERE id=?`, status, id)
	return err
}

// SetHost replaces a secondary domain's host. It exists for the Tor world
// (ADR-0141): a "Tor site" is created with a placeholder host, then the parent
// mints its .onion and rewrites the host to that address. The primary is refused
// (its host is the install's identity) and the new host must be unique.
func (r *Registry) SetHost(ctx context.Context, id, host string) error {
	host = NormalizeHost(host)
	if host == "" {
		return fmt.Errorf("domain: host is required")
	}
	cur, err := r.get(ctx, id)
	if err != nil {
		return err
	}
	if cur.IsPrimary {
		return fmt.Errorf("domain: the primary domain host cannot be changed")
	}
	defer r.invalidate()
	_, err = r.db.ExecContext(ctx, `UPDATE domains SET host=?,updated_at=CURRENT_TIMESTAMP WHERE id=?`, host, id)
	return err
}

// SetTLSState records a certificate lifecycle transition. Provisioning itself
// lands in a later stage; Stage 1 only stores the state the operator or a future
// provisioner reports.
func (r *Registry) SetTLSState(ctx context.Context, id, state string) error {
	switch state {
	case TLSPrimary, TLSPending, TLSActive, TLSFailed:
	default:
		return fmt.Errorf("domain: invalid tls state %q", state)
	}
	defer r.invalidate()
	_, err := r.db.ExecContext(ctx, `UPDATE domains SET tls_state=?,updated_at=CURRENT_TIMESTAMP WHERE id=?`, state, id)
	return err
}

// SetSyncState approves or holds a secondary domain for out-of-process
// provisioning (P5 manual sync gate). The primary is refused: its certificate
// and vhost predate the registry and are never driven by the helper.
func (r *Registry) SetSyncState(ctx context.Context, id, state string) error {
	if state != SyncApproved && state != SyncHold {
		return fmt.Errorf("domain: invalid sync state %q", state)
	}
	cur, err := r.get(ctx, id)
	if err != nil {
		return err
	}
	if cur.IsPrimary {
		return fmt.Errorf("domain: the primary domain is provisioned outside the registry")
	}
	defer r.invalidate()
	_, err = r.db.ExecContext(ctx, `UPDATE domains SET sync_state=?,updated_at=CURRENT_TIMESTAMP WHERE id=? AND is_primary=0`, state, id)
	return err
}

// SetAllSyncState flips every secondary domain to state in one statement and
// reports how many rows changed — the bulk counterpart to SetSyncState, so an
// operator can approve (or hold) a batch of newly-registered domains without
// clicking each row. The primary is excluded by the is_primary=0 guard, and
// only rows not already in the target state are counted, so approving an
// already-synced set reports 0 changed.
func (r *Registry) SetAllSyncState(ctx context.Context, state string) (int, error) {
	if state != SyncApproved && state != SyncHold {
		return 0, fmt.Errorf("domain: invalid sync state %q", state)
	}
	defer r.invalidate()
	res, err := r.db.ExecContext(ctx, `UPDATE domains SET sync_state=?,updated_at=CURRENT_TIMESTAMP WHERE is_primary=0 AND sync_state!=?`, state, state)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	return int(n), err
}

// SetBrand stores a secondary domain's public branding overrides in its
// config_json. Like every mutable per-domain field, the primary is refused: its
// identity is the global Website settings, and re-branding it here would let a
// single-host install diverge from byte-identical. An empty brand clears the
// stored config back to empty so the resolve path's Brand() short-circuit holds.
func (r *Registry) SetBrand(ctx context.Context, id string, b Brand) error {
	cur, err := r.get(ctx, id)
	if err != nil {
		return err
	}
	if cur.IsPrimary {
		return fmt.Errorf("domain: the primary domain's branding is managed from Website settings")
	}
	cfg, err := EncodeBrandConfig(b)
	if err != nil {
		return err
	}
	defer r.invalidate()
	_, err = r.db.ExecContext(ctx, `UPDATE domains SET config_json=?,updated_at=CURRENT_TIMESTAMP WHERE id=? AND is_primary=0`, cfg, id)
	return err
}

// Delete removes a secondary domain outright. The primary is never deletable.
func (r *Registry) Delete(ctx context.Context, id string) error {
	cur, err := r.get(ctx, id)
	if err != nil {
		return err
	}
	if cur.IsPrimary {
		return fmt.Errorf("domain: the primary domain cannot be removed")
	}
	defer r.invalidate()
	_, err = r.db.ExecContext(ctx, `DELETE FROM domains WHERE id=?`, id)
	return err
}

// get loads one domain by id from the writer connection (used right after a
// write, where the read pool may not yet see the change).
func (r *Registry) get(ctx context.Context, id string) (Domain, error) {
	var d Domain
	var mail, prim int
	err := r.db.QueryRowContext(ctx, `SELECT id,host,site_type,mail_enabled,tls_state,sync_state,config_json,is_primary,status,created_at,updated_at FROM domains WHERE id=?`, id).
		Scan(&d.ID, &d.Host, &d.SiteType, &mail, &d.TLSState, &d.SyncState, &d.ConfigJSON, &prim, &d.Status, &d.CreatedAt, &d.UpdatedAt)
	if err != nil {
		return Domain{}, err
	}
	d.MailEnabled = mail != 0
	d.IsPrimary = prim != 0
	return d, nil
}
