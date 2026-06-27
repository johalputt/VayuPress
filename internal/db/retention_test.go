package db

import (
	"database/sql"
	"testing"

	"github.com/johalputt/vayupress/internal/config"
	_ "github.com/mattn/go-sqlite3"
)

func TestPruneWriteJobsOnce(t *testing.T) {
	d, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	// Keep a single shared in-memory connection so all statements hit one DB.
	d.SetMaxOpenConns(1)
	defer d.Close()

	old := DB
	DB = d
	defer func() { DB = old }()

	config.Cfg.JobRetentionHours = 24
	config.Cfg.DeadJobRetentionDays = 7

	if _, err := d.Exec(`CREATE TABLE write_jobs(
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		article_json TEXT NOT NULL DEFAULT '',
		status TEXT NOT NULL DEFAULT 'pending',
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP)`); err != nil {
		t.Fatal(err)
	}

	seed := []struct {
		status string
		age    string // datetime modifier
		keep   bool
	}{
		{"completed", "-2 days", false},    // old completed -> pruned
		{"completed", "-1 hours", true},    // recent completed -> kept
		{"pending", "-30 days", true},      // pending never pruned
		{"processing", "-30 days", true},   // processing never pruned
		{"failed", "-30 days", true},       // failed never pruned by this sweeper
		{"dead_letter", "-10 days", false}, // old dead -> pruned
		{"quarantined", "-1 days", true},   // recent quarantined -> kept
	}
	for _, s := range seed {
		if _, err := d.Exec(
			`INSERT INTO write_jobs(article_json,status,created_at) VALUES('{}', ?, datetime('now', ?))`,
			s.status, s.age); err != nil {
			t.Fatalf("seed %s: %v", s.status, err)
		}
	}

	n, err := PruneWriteJobsOnce()
	if err != nil {
		t.Fatalf("PruneWriteJobsOnce: %v", err)
	}
	if n != 2 {
		t.Errorf("pruned = %d, want 2 (old completed + old dead_letter)", n)
	}

	var remaining int
	if err := d.QueryRow(`SELECT COUNT(*) FROM write_jobs`).Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	if remaining != 5 {
		t.Errorf("remaining = %d, want 5", remaining)
	}
	// The pending/processing/failed rows and the recent terminal rows must survive.
	var pending int
	_ = d.QueryRow(`SELECT COUNT(*) FROM write_jobs WHERE status='pending'`).Scan(&pending)
	if pending != 1 {
		t.Errorf("pending count = %d, want 1", pending)
	}
}
