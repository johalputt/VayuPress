package storage

import (
	"context"
	"fmt"
	"io"
)

// ArweaveBackend is a stub for Arweave permanent storage.
// Replace with the Arweave HTTP API (bundlr/turbo) for production use.
type ArweaveBackend struct {
	Gateway    string // e.g. "https://arweave.net"
	WalletPath string // path to Arweave JWK wallet file
}

// ErrArweaveNotConfigured signals the Arweave wallet is not configured.
var ErrArweaveNotConfigured = fmt.Errorf("storage: Arweave wallet not configured (stub)")

// Put is a stub — returns ErrArweaveNotConfigured.
func (b *ArweaveBackend) Put(_ context.Context, _ []byte, _ string) (string, error) {
	return "", ErrArweaveNotConfigured
}

// Get is a stub — returns ErrArweaveNotConfigured.
func (b *ArweaveBackend) Get(_ context.Context, _ string) (io.ReadCloser, error) {
	return nil, ErrArweaveNotConfigured
}

// Name returns "arweave".
func (b *ArweaveBackend) Name() string { return "arweave" }
