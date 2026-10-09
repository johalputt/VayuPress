// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/config"
	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/secrets"
	vtor "github.com/johalputt/vayupress/internal/vayuos/vayutor"
)

// onionStore is a torStore over a fresh database, sealing with a secrets
// store whose key file lies beside it, as on an install.
func onionStore(t *testing.T) *torStore {
	t.Helper()
	dir := t.TempDir()
	os.Setenv("DB_PATH", filepath.Join(dir, "tor.db"))
	os.Setenv("API_KEY", "test-key")
	os.Setenv("DOMAIN", "localhost")
	os.Setenv("CACHE_DIR", dir)
	config.Load()
	if err := dbpkg.Init(); err != nil {
		t.Fatalf("db init: %v", err)
	}
	t.Cleanup(func() { dbpkg.ClosePools(); dbpkg.DB.Close() })
	return &torStore{codec: secrets.New(dbpkg.DB, nil, filepath.Join(dir, ".vayu-secret-kek"))}
}

func storedKey(t *testing.T, host string) string {
	t.Helper()
	var k string
	if err := dbpkg.DB.QueryRow(`SELECT private_key FROM tor_onions WHERE host=?`, host).Scan(&k); err != nil {
		t.Fatal(err)
	}
	return k
}

const onionKey = "ED25519-V3:c2VjcmV0LW9uaW9uLWlkZW50aXR5LWtleS1ieXRlcw=="

// The database holds an onion's key only sealed, and it reads back whole.
func TestAnOnionKeyIsSealedInTheDatabase(t *testing.T) {
	s := onionStore(t)
	ctx := context.Background()
	if err := s.SaveOnion(ctx, vtor.OnionRecord{Host: "example.com", Address: "abc.onion", PrivateKey: onionKey}); err != nil {
		t.Fatal(err)
	}
	if k := storedKey(t, "example.com"); strings.Contains(k, "c2VjcmV0") || !strings.HasPrefix(k, "f1.") {
		t.Fatalf("the database holds the onion key as %q", k)
	}
	recs, err := s.LoadOnions(ctx)
	if err != nil || len(recs) != 1 || recs[0].PrivateKey != onionKey {
		t.Fatalf("read back %+v, %v", recs, err)
	}
}

// A key stored before sealing is sealed as it is read, and still served.
func TestAKeyStoredInTheClearIsSealedOnRead(t *testing.T) {
	s := onionStore(t)
	ctx := context.Background()
	if _, err := dbpkg.DB.Exec(`INSERT INTO tor_onions(host,address,private_key) VALUES('old.example','old.onion',?)`, onionKey); err != nil {
		t.Fatal(err)
	}
	recs, err := s.LoadOnions(ctx)
	if err != nil || len(recs) != 1 || recs[0].PrivateKey != onionKey {
		t.Fatalf("read back %+v, %v", recs, err)
	}
	if k := storedKey(t, "old.example"); !strings.HasPrefix(k, "f1.") {
		t.Fatalf("the key read in the clear is still stored as %q", k)
	}
}

// A key this install cannot open is not served, and not overwritten by the
// fresh identity the engine then makes: it returns with its key file.
func TestAKeyThatCannotBeOpenedIsKept(t *testing.T) {
	s := onionStore(t)
	ctx := context.Background()
	const unopenable = "f1.00112233445566778899aabb.00112233445566778899aabbccddeeff"
	if _, err := dbpkg.DB.Exec(`INSERT INTO tor_onions(host,address,private_key) VALUES('lost.example','lost.onion',?)`, unopenable); err != nil {
		t.Fatal(err)
	}
	recs, err := s.LoadOnions(ctx)
	if err != nil || len(recs) != 0 {
		t.Fatalf("an unopenable key was served: %+v, %v", recs, err)
	}
	if err := s.SaveOnion(ctx, vtor.OnionRecord{Host: "lost.example", Address: "new.onion", PrivateKey: onionKey}); !errors.Is(err, errOnionKeyUnreadable) {
		t.Fatalf("overwriting it: %v", err)
	}
	if k := storedKey(t, "lost.example"); k != unopenable {
		t.Fatalf("the unopenable key was replaced by %q", k)
	}
}
