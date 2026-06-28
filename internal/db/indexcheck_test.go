package db

import (
	"database/sql"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

func TestFirstFullScan(t *testing.T) {
	cases := []struct {
		plan []string
		want bool
	}{
		{[]string{"SEARCH articles USING INDEX idx_articles_pagefeed (is_page=?)"}, false},
		{[]string{"SCAN articles USING INDEX idx_articles_created"}, false},
		{[]string{"SCAN t USING COVERING INDEX idx_article_tags_tag"}, false},
		{[]string{"SEARCH a USING INTEGER PRIMARY KEY (rowid=?)"}, false},
		{[]string{"SEARCH t USING INDEX x", "USE TEMP B-TREE FOR ORDER BY"}, false},
		{[]string{"SCAN articles"}, true},
		{[]string{"SEARCH t USING INDEX x", "SCAN articles"}, true},
	}
	for i, c := range cases {
		if _, got := firstFullScan(c.plan); got != c.want {
			t.Errorf("case %d: firstFullScan(%v) = %v, want %v", i, c.plan, got, c.want)
		}
	}
}

// TestRunIndexSelfCheckClean applies the real migrations to an in-memory DB and
// asserts that every curated hot query is index-backed (zero full-scan warnings).
// This locks in the index audit: adding a hot query that scans, or dropping an
// index it relies on, fails here.
func TestRunIndexSelfCheckClean(t *testing.T) {
	d, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	d.SetMaxOpenConns(1)
	t.Cleanup(func() { d.Close() })

	old, oldR := DB, RDB
	DB = d
	RDB = nil // Reader() falls back to the single shared connection
	t.Cleanup(func() { DB, RDB = old, oldR })

	if err := runMigrations(); err != nil {
		t.Fatalf("runMigrations: %v", err)
	}

	if n := RunIndexSelfCheck(); n != 0 {
		t.Errorf("RunIndexSelfCheck reported %d full-scan warning(s); every hot query must be index-backed", n)
	}
}

// TestExplainDetectsUnindexedScan proves the checker actually flags a real full
// table scan, not just that the curated queries happen to pass.
func TestExplainDetectsUnindexedScan(t *testing.T) {
	d, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	d.SetMaxOpenConns(1)
	t.Cleanup(func() { d.Close() })

	old, oldR := DB, RDB
	DB = d
	RDB = nil
	t.Cleanup(func() { DB, RDB = old, oldR })

	if _, err := d.Exec(`CREATE TABLE noidx(id TEXT, k TEXT)`); err != nil {
		t.Fatal(err)
	}
	plan, err := explainQueryPlan(`SELECT id FROM noidx WHERE k=?`, "v")
	if err != nil {
		t.Fatalf("explainQueryPlan: %v", err)
	}
	if _, scan := firstFullScan(plan); !scan {
		t.Errorf("expected a full-scan detection for an unindexed query; plan=%v", plan)
	}
}
