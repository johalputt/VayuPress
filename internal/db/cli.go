// SPDX-License-Identifier: Apache-2.0

package db

// cli.go — opening the database for a short-lived helper process.
//
// # The outage this exists to prevent
//
// The privileged provisioning helpers record their results by invoking the
// VayuPress CLI: `vayupress domains set-tls <host> <state>`, once per domain,
// plus `domains hosts` to read the work list. Every one of those processes was
// calling Init(), which is the SERVER's opening sequence:
//
//   - CREATE TABLE IF NOT EXISTS schema_migrations — a WRITE, so it takes
//     SQLite's write lock immediately;
//   - a SELECT per migration to see whether it has been applied. There are 87;
//   - a checksum verification pass over all of them;
//   - PRAGMA mmap_size=512MB and a 32 MB page cache on the writer;
//   - a read pool of NumCPU×4 connections, minimum 24, plus an admin read pool.
//
// That is the correct opening sequence for the process that OWNS the database
// and is about to serve traffic from it for months. It is a remarkable thing to
// do nine times in a row, from short-lived processes, against a live database
// that another process is serving a website from.
//
// The consequence is not subtle. Both the server and the CLI cap the write pool
// at one connection, so while a helper holds the write lock the server's single
// writer blocks — and on this product every page view takes a write. Requests
// queue behind one blocked connection, nginx's proxy read timeout expires, and
// visitors get 502 from a server that never crashed and never restarted. It
// recovers on its own when the helper finishes, which is exactly what makes it
// so hard to attribute: nothing is down, nothing is in the log, and by the time
// anybody looks the site is fine.
//
// # What a helper actually needs
//
// One connection, to read a few rows and update one. It does not need the
// schema created — the server did that before it ever served a request. It does
// not need 87 existence checks, or a half-gigabyte mapping, or 24 readers.
//
// InitCLI gives it exactly that, and refuses to migrate. Only one process ever
// migrates this database, and it is the one that owns it.

import (
	"database/sql"
	"fmt"
	"strings"

	"github.com/johalputt/vayupress/internal/config"
)

// cliBusyTimeoutMS is how long a helper waits for the write lock.
//
// LONGER than the server's five seconds, not shorter, which is the opposite of
// the intuitive choice. A helper is a background chore with nobody watching and
// no request timing out behind it; the server is answering a person. So when the
// two contend, the one that should wait is the helper — and waiting politely is
// how it avoids failing a certificate record over a lock it could have had a
// second later.
const cliBusyTimeoutMS = 20000

// InitCLI opens the database for a short-lived command-line process.
//
// It sets the same package globals Init does, so callers are unchanged, but it
// creates nothing, migrates nothing and verifies nothing. RDB is deliberately
// left nil: every store in this codebase treats a nil read pool as "use the
// write handle", and a one-shot process has no use for two dozen readers.
func InitCLI() error {
	// mmap_size=0 and a small page cache. The server's half-gigabyte mapping
	// pays for itself over months of serving; a process that will exit in
	// milliseconds only pays the cost of establishing it, and pays it again on
	// every invocation.
	dsn := config.Cfg.DBPath +
		"?_journal_mode=WAL&_busy_timeout=" + itoa(cliBusyTimeoutMS) +
		"&_foreign_keys=on&_synchronous=NORMAL"

	var err error
	DB, err = sql.Open("sqlite3", dsn)
	if err != nil {
		return fmt.Errorf("open: %w", err)
	}
	DB.SetMaxOpenConns(1)
	DB.SetMaxIdleConns(1)
	if err := DB.Ping(); err != nil {
		return fmt.Errorf("ping: %w", err)
	}
	WDB = wrappedDB{DB}

	// READ-ONLY PRAGMAS ONLY. journal_mode is not set here on purpose: setting
	// WAL is a write to the database header, and a helper must not take a write
	// lock merely to open. The server has already put this database in WAL, and
	// the DSN above asks for it without forcing it.
	for _, p := range []string{
		"PRAGMA busy_timeout=" + itoa(cliBusyTimeoutMS),
		"PRAGMA foreign_keys=ON",
		"PRAGMA cache_size=-2048", // 2 MB, against the server's 32
		"PRAGMA mmap_size=0",      // no 512 MB mapping for a process that exits
		"PRAGMA temp_store=MEMORY",
	} {
		if _, err := DB.Exec(p); err != nil {
			return fmt.Errorf("pragma %q: %w", p, err)
		}
	}

	// The one check worth making: has the server ever opened this database?
	//
	// If schema_migrations is absent then no VayuPress process has initialised
	// it, and a helper that carried on would be recording a TLS state into a
	// database with no schema — reporting success while writing nothing, or
	// worse, CREATING the table and racing the real owner's migration run. It
	// says so and stops instead.
	var name string
	err = DB.QueryRow(
		`SELECT name FROM sqlite_master WHERE type='table' AND name='schema_migrations'`).Scan(&name)
	if err != nil {
		_ = DB.Close()
		DB, WDB = nil, wrappedDB{}
		return fmt.Errorf(
			"this database has no schema yet (%s). The VayuPress service creates and migrates it on "+
				"first start; a helper must never do that against a live install. Start the service, "+
				"then run this again", strings.TrimSpace(errText(err)))
	}
	return nil
}

// errText renders an error for a message without a nil dereference.
func errText(err error) string {
	if err == nil {
		return "no schema_migrations table"
	}
	if err == sql.ErrNoRows {
		return "no schema_migrations table"
	}
	return err.Error()
}

// itoa avoids pulling strconv into this file's import list for two call sites.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
