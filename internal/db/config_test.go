package db_test

import (
	"testing"

	"github.com/johalputt/vayupress/internal/db"
	_ "github.com/mattn/go-sqlite3"
)

func TestOpenDefaults(t *testing.T) {
	cfg := db.DefaultConfig(":memory:")
	d, err := db.Open(cfg)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer d.Close()
	if err := d.Ping(); err != nil {
		t.Fatalf("Ping: %v", err)
	}
}

func TestWALStats(t *testing.T) {
	cfg := db.DefaultConfig(":memory:")
	d, err := db.Open(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	_, err = db.WALStats(d)
	if err != nil {
		t.Fatalf("WALStats: %v", err)
	}
}
