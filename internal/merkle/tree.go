// Package merkle provides a SHA-256 Merkle tree for VayuPress content integrity.
package merkle

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
)

// Tree is a binary Merkle tree over a set of leaf hashes.
type Tree struct {
	leaves [][]byte
	layers [][][]byte
	root   []byte
}

// New builds a Merkle tree from a set of data items.
func New(items [][]byte) (*Tree, error) {
	if len(items) == 0 {
		return nil, errors.New("merkle: no items")
	}
	t := &Tree{}
	for _, item := range items {
		h := sha256.Sum256(item)
		t.leaves = append(t.leaves, h[:])
	}

	layer := hashCopy(t.leaves)
	t.layers = append(t.layers, layer)
	for len(layer) > 1 {
		layer = parentLayer(layer)
		t.layers = append(t.layers, hashCopy(layer))
	}
	t.root = make([]byte, len(layer[0]))
	copy(t.root, layer[0])
	return t, nil
}

// Root returns the hex-encoded Merkle root.
func (t *Tree) Root() string {
	return hex.EncodeToString(t.root)
}

// Proof returns the sibling hashes needed to prove inclusion of leaf at index.
func (t *Tree) Proof(index int) ([]string, error) {
	if index < 0 || index >= len(t.leaves) {
		return nil, errors.New("merkle: index out of range")
	}
	var proof []string
	idx := index
	for li := 0; li < len(t.layers)-1; li++ {
		layer := t.layers[li]
		sibling := idx ^ 1
		if sibling < len(layer) {
			proof = append(proof, hex.EncodeToString(layer[sibling]))
		}
		idx /= 2
	}
	return proof, nil
}

// Verify checks that leaf (raw data) at index is part of the tree with root rootHex.
func Verify(leaf []byte, index int, proof []string, rootHex string) bool {
	h := sha256.Sum256(leaf)
	cur := h[:]
	idx := index
	for _, sibHex := range proof {
		sib, err := hex.DecodeString(sibHex)
		if err != nil {
			return false
		}
		if idx%2 == 0 {
			cur = pairHash(cur, sib)
		} else {
			cur = pairHash(sib, cur)
		}
		idx /= 2
	}
	return hex.EncodeToString(cur) == rootHex
}

func parentLayer(layer [][]byte) [][]byte {
	var out [][]byte
	for i := 0; i < len(layer); i += 2 {
		if i+1 < len(layer) {
			out = append(out, pairHash(layer[i], layer[i+1]))
		} else {
			out = append(out, pairHash(layer[i], layer[i]))
		}
	}
	return out
}

func pairHash(left, right []byte) []byte {
	combined := make([]byte, len(left)+len(right))
	copy(combined, left)
	copy(combined[len(left):], right)
	h := sha256.Sum256(combined)
	return h[:]
}

func hashCopy(in [][]byte) [][]byte {
	out := make([][]byte, len(in))
	for i, h := range in {
		cp := make([]byte, len(h))
		copy(cp, h)
		out[i] = cp
	}
	return out
}
