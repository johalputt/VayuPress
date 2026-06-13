package storage_test

import (
	"bytes"
	"context"
	"io"
	"testing"

	"github.com/johalputt/vayupress/internal/storage"
)

func TestLocalBackendRoundTrip(t *testing.T) {
	dir := t.TempDir()
	b, err := storage.NewLocal(dir)
	if err != nil {
		t.Fatal(err)
	}
	data := []byte("hello sovereign storage")
	ctx := context.Background()
	id, err := b.Put(ctx, data, "text/plain")
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	rc, err := b.Get(ctx, id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer rc.Close()
	got, _ := io.ReadAll(rc)
	if !bytes.Equal(got, data) {
		t.Errorf("data mismatch: got %q want %q", got, data)
	}
}

func TestLocalDeduplication(t *testing.T) {
	dir := t.TempDir()
	b, _ := storage.NewLocal(dir)
	ctx := context.Background()
	id1, _ := b.Put(ctx, []byte("same"), "text/plain")
	id2, _ := b.Put(ctx, []byte("same"), "text/plain")
	if id1 != id2 {
		t.Error("same content should produce same ID")
	}
}

func TestFallbackBackend(t *testing.T) {
	dir := t.TempDir()
	local, _ := storage.NewLocal(dir)
	ipfs := &storage.IPFSBackend{}
	fb := &storage.FallbackBackend{Primary: ipfs, Secondary: local}
	ctx := context.Background()

	// Put falls through to primary (IPFS stub fails), but FallbackBackend.Put uses primary only
	// So just test that Name() is correct
	if fb.Name() != "ipfs+local" {
		t.Errorf("got name %q", fb.Name())
	}
	// Store something in local, verify fallback Get works
	id, _ := local.Put(ctx, []byte("fallback data"), "text/plain")
	rc, err := fb.Get(ctx, id)
	if err != nil {
		t.Fatalf("fallback Get: %v", err)
	}
	rc.Close()
}
