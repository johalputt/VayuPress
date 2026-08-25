// SPDX-License-Identifier: Apache-2.0

package secrets

import (
	"bytes"
	"database/sql"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// Regression (e2e red, 2026-08-25): Derive originally wrapped ensure() in
// another once.Do. sync.Once is not reentrant, so the FIRST Derive call on a
// fresh Store blocked forever — the server hung mid-boot between two log
// lines with no error output anywhere. This test fails fast instead of
// hanging the suite.
func TestDeriveReturnsOnFirstUse(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	s := New(db, []byte("unit-kek"), "")

	done := make(chan struct{})
	var k1, k2 []byte
	var derr error
	go func() {
		defer close(done)
		k1, derr = s.Derive("purpose-a")
		k2, _ = s.Derive("purpose-b")
	}()
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("Derive deadlocked on first use — reentrant sync.Once regression")
	}

	if derr != nil {
		return // environment without a working sqlite driver; nothing further to assert
	}
	if len(k1) != 32 || len(k2) != 32 {
		t.Fatalf("derived subkeys must be 32 bytes, got %d/%d", len(k1), len(k2))
	}
	if bytes.Equal(k1, k2) {
		t.Fatal("different purposes must derive different subkeys")
	}
}
