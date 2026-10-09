// SPDX-License-Identifier: Apache-2.0

package main

// vayutor_store.go — the persistence adapter for the VayuTor subsystem. Onion
// identities live in the tor_onions table (so a DB restore brings the SAME
// .onion addresses back); the visit counter lives in a single settings key. The
// engine (internal/vayuos/vayutor) stays DB-free and talks to this via the
// vtor.Store interface.
//
// An onion's private key is its identity: whoever holds it can be that
// address. It is kept sealed (secrets.SealField), under the key the install
// keeps outside the database, so a copy of the database alone (a snapshot, a
// downloaded .db, a pre-update copy sent elsewhere) does not carry it. A
// VayuKeep backup still restores it: the backup holds the data directory, the
// key file with it, under the backup's own passphrase.

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"sync"

	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/logging"
	"github.com/johalputt/vayupress/internal/settings"
	vtor "github.com/johalputt/vayupress/internal/vayuos/vayutor"
)

// fieldCodec seals and opens a column value: the install's secrets store.
type fieldCodec interface {
	SealField(plaintext string) (string, error)
	OpenField(stored string) (string, error)
}

// torStore implements vtor.Store over the site DB + settings store.
type torStore struct {
	settings *settings.Store
	codec    fieldCodec
	// unreadable holds the hosts whose stored key could not be opened, which
	// SaveOnion leaves as they are (see LoadOnions).
	mu         sync.Mutex
	unreadable map[string]bool
}

// errOnionKeyUnreadable refuses to overwrite a stored key this install
// cannot open.
var errOnionKeyUnreadable = errors.New("vayutor: the stored onion key cannot be opened with this install's key file, so it is kept as it is")

// LoadOnions returns every persisted onion identity, opened. A key stored
// before sealing is sealed in place as it is read. A key that cannot be
// opened (the database restored without the key file it was sealed under)
// is left out and said in the log, and is not overwritten: the engine then
// serves a fresh address, and the old one returns once the key file does.
func (s *torStore) LoadOnions(ctx context.Context) ([]vtor.OnionRecord, error) {
	rows, err := dbpkg.Reader().QueryContext(ctx, `SELECT host,address,private_key FROM tor_onions`)
	if err != nil {
		return nil, err
	}
	var stored []vtor.OnionRecord
	for rows.Next() {
		var r vtor.OnionRecord
		if err := rows.Scan(&r.Host, &r.Address, &r.PrivateKey); err != nil {
			rows.Close()
			return nil, err
		}
		stored = append(stored, r)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	unreadable := map[string]bool{}
	var out []vtor.OnionRecord
	for _, r := range stored {
		key, err := s.codec.OpenField(r.PrivateKey)
		if err != nil {
			unreadable[r.Host] = true
			logging.LogWarn("vayutor", "the onion key for "+r.Host+" cannot be opened with this install's key file; "+r.Address+" is kept for when it returns")
			continue
		}
		if key == r.PrivateKey {
			// Stored before sealing: seal it now, so it does not wait for a
			// change of address to leave the database in the clear.
			if err := s.SaveOnion(ctx, vtor.OnionRecord{Host: r.Host, Address: r.Address, PrivateKey: key}); err != nil {
				logging.LogWarn("vayutor", "the onion key for "+r.Host+" could not be sealed: "+err.Error())
			}
		}
		r.PrivateKey = key
		out = append(out, r)
	}
	s.mu.Lock()
	s.unreadable = unreadable
	s.mu.Unlock()
	return out, nil
}

// SaveOnion upserts an onion identity keyed by clearnet host, its key sealed.
func (s *torStore) SaveOnion(ctx context.Context, rec vtor.OnionRecord) error {
	s.mu.Lock()
	keep := s.unreadable[rec.Host]
	s.mu.Unlock()
	if keep {
		return errOnionKeyUnreadable
	}
	sealed, err := s.codec.SealField(rec.PrivateKey)
	if err != nil {
		return err
	}
	_, err = dbpkg.DB.ExecContext(ctx,
		`INSERT INTO tor_onions(host,address,private_key) VALUES(?,?,?)
		 ON CONFLICT(host) DO UPDATE SET address=excluded.address,private_key=excluded.private_key`,
		rec.Host, rec.Address, sealed)
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
	n, _ := strconv.ParseInt(s.settings.Get(ctx, settings.ForPrimary(), settings.KeyTorVisits), 10, 64)
	return n
}

// SaveVisits persists the aggregate onion pageview count (batched by the engine
// to one write per reconcile tick).
func (s *torStore) SaveVisits(ctx context.Context, n int64) error {
	if s.settings == nil {
		return nil
	}
	return s.settings.SetMany(ctx, settings.ForPrimary(), map[string]string{settings.KeyTorVisits: strconv.FormatInt(n, 10)})
}

// LoadPageHits reads the persisted per-page onion counts (aggregate; opt-in).
func (s *torStore) LoadPageHits(ctx context.Context) map[string]int64 {
	if s.settings == nil {
		return nil
	}
	raw := s.settings.Get(ctx, settings.ForPrimary(), settings.KeyTorPageHits)
	if raw == "" {
		return nil
	}
	var hits map[string]int64
	if err := json.Unmarshal([]byte(raw), &hits); err != nil {
		return nil
	}
	return hits
}

// SavePageHits persists the per-page onion counts as a compact JSON object
// (batched by the engine to one write per reconcile tick). Empty ⇒ cleared.
func (s *torStore) SavePageHits(ctx context.Context, hits map[string]int64) error {
	if s.settings == nil {
		return nil
	}
	val := ""
	if len(hits) > 0 {
		b, err := json.Marshal(hits)
		if err != nil {
			return err
		}
		val = string(b)
	}
	return s.settings.SetMany(ctx, settings.ForPrimary(), map[string]string{settings.KeyTorPageHits: val})
}
