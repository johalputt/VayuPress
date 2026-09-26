// SPDX-License-Identifier: Apache-2.0

//go:build cgo

package sqlitecopy

import (
	"context"
	"database/sql"

	"github.com/johalputt/vayupress/internal/pacedio"
	sqlite3 "github.com/mattn/go-sqlite3"
)

// backup runs SQLite's backup API from src to dst in paced steps.
func backup(ctx context.Context, dst, src *sql.Conn, p pacedio.Pacer, pageSize int, onStep func(Progress)) error {
	return dst.Raw(func(d any) error {
		return src.Raw(func(s any) error {
			bk, err := d.(*sqlite3.SQLiteConn).Backup("main", s.(*sqlite3.SQLiteConn), "main")
			if err != nil {
				return err
			}
			if err := steps(ctx, bk, p, pageSize, onStep); err != nil {
				_ = bk.Finish()
				return err
			}
			return bk.Finish()
		})
	})
}
