package migrations_test

import (
	"database/sql"
	"testing"

	_ "github.com/mattn/go-sqlite3"
	"github.com/johalputt/vayupress/internal/migrations"
)

func BenchmarkMigratorUpMemory(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		db, _ := sql.Open("sqlite3", ":memory:")
		m := migrations.New(db)
		_ = m.Up()
		db.Close()
	}
}
