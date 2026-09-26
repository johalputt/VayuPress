// SPDX-License-Identifier: Apache-2.0

//go:build !cgo

package sqlitecopy

import (
	"context"
	"database/sql"
	"errors"

	"github.com/johalputt/vayupress/internal/pacedio"
)

// backup is unavailable without cgo: go-sqlite3's pure-Go stub has no backup
// API, and no database opens in such a build anyway. The package still
// compiles so the cross-compile gate can build every other package.
func backup(context.Context, *sql.Conn, *sql.Conn, pacedio.Pacer, int, func(Progress)) error {
	return errors.New("sqlitecopy: this build has no SQLite (built without cgo)")
}
