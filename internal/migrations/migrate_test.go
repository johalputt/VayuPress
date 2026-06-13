package migrations_test

import (
	"database/sql"
	"testing"

	"github.com/johalputt/vayupress/internal/migrations"
	_ "github.com/mattn/go-sqlite3"
)

func TestMigratorUp(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	m := migrations.New(db)
	if err := m.Up(); err != nil {
		t.Fatalf("Up: %v", err)
	}

	records, err := m.Applied()
	if err != nil {
		t.Fatalf("Applied: %v", err)
	}
	if len(records) == 0 {
		t.Error("expected at least one applied migration")
	}
}

func TestMigratorIdempotent(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	m := migrations.New(db)
	if err := m.Up(); err != nil {
		t.Fatalf("first Up: %v", err)
	}
	if err := m.Up(); err != nil {
		t.Fatalf("second Up (idempotent): %v", err)
	}
}

func TestMigratorDown(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	m := migrations.New(db)
	if err := m.Up(); err != nil {
		t.Fatalf("Up: %v", err)
	}
	if err := m.Down(); err != nil {
		t.Fatalf("Down: %v", err)
	}
}
