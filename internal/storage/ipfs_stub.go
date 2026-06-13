package storage

import (
	"context"
	"fmt"
	"io"
)

// IPFSBackend is a stub for IPFS content-addressed storage.
// Replace with a real IPFS HTTP client (e.g. kubo RPC API) for production use.
// The stub returns ErrNotImplemented so FallbackBackend falls through to local.
type IPFSBackend struct {
	Gateway string // e.g. "http://localhost:5001"
}

// ErrNotImplemented signals that the backend is a stub.
var ErrNotImplemented = fmt.Errorf("storage: IPFS backend not configured (stub)")

// Put is a stub — returns ErrNotImplemented.
func (b *IPFSBackend) Put(_ context.Context, _ []byte, _ string) (string, error) {
	return "", ErrNotImplemented
}

// Get is a stub — returns ErrNotImplemented.
func (b *IPFSBackend) Get(_ context.Context, _ string) (io.ReadCloser, error) {
	return nil, ErrNotImplemented
}

// Name returns "ipfs".
func (b *IPFSBackend) Name() string { return "ipfs" }
