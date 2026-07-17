package main

// vayutor_store.go — the persistence adapter for the VayuTor subsystem. Onion
// identities live in the tor_onions table (so a DB restore brings the SAME
// .onion addresses back); the visit counter lives in a single settings key. The
// engine (internal/vayuos/vayutor) stays DB-free and talks to this via the
// vtor.Store interface.

import (
	"context"
	"strconv"

	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/settings"
	vtor "github.com/johalputt/vayupress/internal/vayuos/vayutor"
)

// torStore implements vtor.Store over the site DB + settings store.
type torStore struct {
	settings *settings.Store
}

// LoadOnions returns every persisted onion identity.
func (s *torStore) LoadOnions(ctx context.Context) ([]vtor.OnionRecord, error) {
	rows, err := dbpkg.Reader().QueryContext(ctx, `SELECT host,address,private_key FROM tor_onions`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []vtor.OnionRecord
	for rows.Next() {
		var r vtor.OnionRecord
		if err := rows.Scan(&r.Host, &r.Address, &r.PrivateKey); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// SaveOnion upserts an onion identity keyed by clearnet host.
func (s *torStore) SaveOnion(ctx context.Context, rec vtor.OnionRecord) error {
	_, err := dbpkg.DB.ExecContext(ctx,
		`INSERT INTO tor_onions(host,address,private_key) VALUES(?,?,?)
		 ON CONFLICT(host) DO UPDATE SET address=excluded.address,private_key=excluded.private_key`,
		rec.Host, rec.Address, rec.PrivateKey)
	return err
}

// DeleteOnion removes the identity for a host no longer served.
func (s *torStore) DeleteOnion(ctx context.Context, host string) error {
	_, err := dbpkg.DB.ExecContext(ctx, `DELETE FROM tor_onions WHERE host=?`, host)
	return err
}

// LoadVisits reads the persisted aggregate onion pageview count.
func (s *torStore) LoadVisits(ctx context.Context) int64 {
	if s.settings == nil {
		return 0
	}
	n, _ := strconv.ParseInt(s.settings.Get(ctx, settings.KeyTorVisits), 10, 64)
	return n
}

// SaveVisits persists the aggregate onion pageview count (batched by the engine
// to one write per reconcile tick).
func (s *torStore) SaveVisits(ctx context.Context, n int64) error {
	if s.settings == nil {
		return nil
	}
	return s.settings.SetMany(ctx, map[string]string{settings.KeyTorVisits: strconv.FormatInt(n, 10)})
}
