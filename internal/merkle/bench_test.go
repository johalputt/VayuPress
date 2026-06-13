package merkle

import (
	"crypto/rand"
	"testing"
)

func BenchmarkMerkleNew1024(b *testing.B) {
	items := make([][]byte, 1024)
	for i := range items {
		items[i] = make([]byte, 32)
		rand.Read(items[i]) //nolint:errcheck
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := New(items); err != nil {
			b.Fatalf("New: %v", err)
		}
	}
}

func BenchmarkMerkleProof(b *testing.B) {
	items := make([][]byte, 256)
	for i := range items {
		items[i] = make([]byte, 32)
		rand.Read(items[i]) //nolint:errcheck
	}
	tree, err := New(items)
	if err != nil {
		b.Fatalf("New: %v", err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := tree.Proof(i % 256); err != nil {
			b.Fatalf("Proof: %v", err)
		}
	}
}
