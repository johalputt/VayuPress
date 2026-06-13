// Package storage defines the sovereign storage backend interface for VayuPress.
// Implementations include local filesystem, IPFS, and Arweave.
// All backends are content-addressed — store by hash, retrieve by CID/txid.
package storage

import (
	"context"
	"io"
)

// Backend is the interface for a sovereign storage backend.
type Backend interface {
	// Put stores data and returns a content identifier (CID, hash, txid, etc.)
	Put(ctx context.Context, data []byte, contentType string) (id string, err error)
	// Get retrieves data by content identifier.
	Get(ctx context.Context, id string) (io.ReadCloser, error)
	// Name returns the backend identifier (e.g. "local", "ipfs", "arweave").
	Name() string
}

// FallbackBackend tries primary first, falls back to secondary on Get failure.
type FallbackBackend struct {
	Primary   Backend
	Secondary Backend
}

// Put stores to the primary backend.
func (f *FallbackBackend) Put(ctx context.Context, data []byte, contentType string) (string, error) {
	return f.Primary.Put(ctx, data, contentType)
}

// Get tries primary, falls back to secondary on error.
func (f *FallbackBackend) Get(ctx context.Context, id string) (io.ReadCloser, error) {
	rc, err := f.Primary.Get(ctx, id)
	if err == nil {
		return rc, nil
	}
	return f.Secondary.Get(ctx, id)
}

// Name returns a composite name.
func (f *FallbackBackend) Name() string {
	return f.Primary.Name() + "+" + f.Secondary.Name()
}
