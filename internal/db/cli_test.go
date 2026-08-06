// SPDX-License-Identifier: Apache-2.0

package db

// cli_test.go — a helper must not behave like the server that owns the database.
//
// The outage this guards: `vayupress domains set-tls` ran the SERVER's opening
// sequence — write-locking to create a table that already existed, scanning 87
// migrations, mapping 512 MB and opening two dozen readers — once per domain,
// while the site was serving. The server caps its writer at one connection, and
// on this product every page view takes a write, so requests queued behind the
// helper's lock until nginx timed out. Visitors saw 502 from a process that
// never crashed, never restarted, and recovered by itself the moment the helper
// finished.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/config"
)

func withTempDBPath(t *testing.T) string {
	t.Helper()
	prev := config.Cfg.DBPath
	p := filepath.Join(t.TempDir(), "t.db")
	config.Cfg.DBPath = p
	t.Cleanup(func() {
		if DB != nil {
			_ = DB.Close()
		}
		DB, WDB, RDB = nil, wrappedDB{}, nil
		config.Cfg.DBPath = prev
	})
	return p
}

// THE test. A helper must refuse an uninitialised database rather than migrate
// it. Two processes migrating the same database is a race, and the one that
// loses corrupts the schema_migrations bookkeeping the other depends on.
func TestInitCLIRefusesToMigrateAnEmptyDatabase(t *testing.T) {
	withTempDBPath(t)

	err := InitCLI()
	if err == nil {
		t.Fatal("a helper opened an unmigrated database and carried on; it must refuse rather " +
			"than create a schema against a live install")
	}
	if !strings.Contains(err.Error(), "no schema") {
		t.Errorf("the refusal does not say why: %v", err)
	}
	// And it must say what to DO. A refusal with no next step sends somebody to
	// a shell to guess.
	if !strings.Contains(err.Error(), "Start the service") {
		t.Errorf("the refusal does not tell the operator what to do next: %v", err)
	}
	if DB != nil {
		t.Error("the handle was left open after a refusal")
	}
}

// After the owner has migrated, a helper opens cleanly and creates nothing.
func TestInitCLIOpensAMigratedDatabaseAndCreatesNothing(t *testing.T) {
	withTempDBPath(t)

	// The server's path, once — this is the process that owns the schema.
	if err := Init(); err != nil {
		t.Fatalf("server Init: %v", err)
	}
	var before int
	if err := DB.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table'`).Scan(&before); err != nil {
		t.Fatalf("count tables: %v", err)
	}
	_ = DB.Close()
	DB, WDB, RDB = nil, wrappedDB{}, nil

	if err := InitCLI(); err != nil {
		t.Fatalf("InitCLI on a migrated database: %v", err)
	}
	var after int
	if err := DB.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table'`).Scan(&after); err != nil {
		t.Fatalf("count tables after: %v", err)
	}
	if after != before {
		t.Errorf("the helper changed the schema: %d tables before, %d after", before, after)
	}
}

// The helper's footprint must stay small. The server maps half a gigabyte and
// opens NumCPU×4 readers because it serves for months; a process that exits in
// milliseconds pays that cost on every invocation and gets nothing for it.
func TestInitCLIKeepsAMinimalFootprint(t *testing.T) {
	withTempDBPath(t)
	if err := Init(); err != nil {
		t.Fatalf("server Init: %v", err)
	}
	_ = DB.Close()
	DB, WDB, RDB = nil, wrappedDB{}, nil

	if err := InitCLI(); err != nil {
		t.Fatalf("InitCLI: %v", err)
	}

	// No read pool. Every store treats a nil RDB as "use the write handle".
	if RDB != nil {
		t.Error("the helper opened a read pool; a one-shot process has no use for two dozen readers")
	}

	var mmap int64
	if err := DB.QueryRow(`PRAGMA mmap_size`).Scan(&mmap); err != nil {
		t.Fatalf("read mmap_size: %v", err)
	}
	if mmap != 0 {
		t.Errorf("the helper mapped %d bytes; the server's 512 MB mapping is for a process that "+
			"serves for months, not one that exits", mmap)
	}

	// And it must WAIT for the lock rather than fail fast. A helper is a
	// background chore with nobody watching; the server is answering a person.
	// When they contend the helper is the one that should yield.
	var busy int
	if err := DB.QueryRow(`PRAGMA busy_timeout`).Scan(&busy); err != nil {
		t.Fatalf("read busy_timeout: %v", err)
	}
	if busy < 10000 {
		t.Errorf("busy_timeout is %dms; a helper that gives up quickly turns lock contention into "+
			"a failed certificate record", busy)
	}
}

// A helper must be able to do the one thing it came for.
func TestInitCLICanStillWrite(t *testing.T) {
	withTempDBPath(t)
	if err := Init(); err != nil {
		t.Fatalf("server Init: %v", err)
	}
	_ = DB.Close()
	DB, WDB, RDB = nil, wrappedDB{}, nil

	if err := InitCLI(); err != nil {
		t.Fatalf("InitCLI: %v", err)
	}
	if _, err := DB.Exec(
		`INSERT INTO site_settings(scope,key,value) VALUES('','cli.probe','1')
		 ON CONFLICT(scope,key) DO UPDATE SET value=excluded.value`); err != nil {
		t.Fatalf("the helper cannot write, so it cannot record a TLS state: %v", err)
	}
	var v string
	if err := DB.QueryRow(`SELECT value FROM site_settings WHERE scope='' AND key='cli.probe'`).
		Scan(&v); err != nil || v != "1" {
		t.Fatalf("write did not land: %q %v", v, err)
	}
}

// The subcommands the PRIVILEGED HELPERS drive must use the light path. This is
// a source check because it is the wiring, not the function, that regresses:
// InitCLI can be perfect and unused.
func TestTheProvisioningSubcommandsUseTheLightPath(t *testing.T) {
	b, err := os.ReadFile(filepath.Clean("../../cmd/vayupress/main.go"))
	if err != nil {
		t.Skipf("main.go not readable from here: %v", err)
		return
	}
	src := string(b)
	for _, sub := range []string{"talk", "domains"} {
		marker := `os.Args[1] == "` + sub + `"`
		i := strings.Index(src, marker)
		if i < 0 {
			t.Errorf("the %q subcommand is gone; this guard is blind", sub)
			continue
		}
		// Look only as far as the next subcommand block, so this cannot pass on
		// a neighbour's InitCLI.
		seg := src[i:]
		if j := strings.Index(seg[len(marker):], "os.Args[1] == "); j > 0 {
			seg = seg[:len(marker)+j]
		}
		if !strings.Contains(seg, "dbpkg.InitCLI()") {
			t.Errorf("the %q subcommand still calls the server's Init. It runs from a privileged "+
				"helper once per domain while the site is live, and Init write-locks to create a "+
				"table that exists, scans 87 migrations and opens two dozen readers — which is what "+
				"made visitors see 502 from a server that never went down.", sub)
		}
	}
}
