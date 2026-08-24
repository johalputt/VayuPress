// SPDX-License-Identifier: Apache-2.0

// Package merkle provides a SHA-256 Merkle tree for VayuPress content integrity.
package merkle

import (
	"crypto/sha256"
	"crypto/subtle"
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
//
// Leaves and interior nodes are DOMAIN-SEPARATED (leaf = H(0x00‖data), node =
// H(0x01‖L‖R)): without the tags, H(item) is ambiguous with an interior hash of
// the same bytes, which is the classic second-preimage ambiguity that lets a
// leaf be re-interpreted as a subtree in proof verification.
func New(items [][]byte) (*Tree, error) {
	if len(items) == 0 {
		return nil, errors.New("merkle: no items")
	}
	t := &Tree{}
	for _, item := range items {
		h := leafHash(item)
		t.leaves = append(t.leaves, h)
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

// Verify checks that leaf (raw data) at index is part of the tree with root
// rootHex. The final comparison is constant-time: a proof verification whose
// outcome leaks through byte-at-a-time timing would let a prober forge
// inclusion evidence one nibble at a time.
func Verify(leaf []byte, index int, proof []string, rootHex string) bool {
	cur := leafHash(leaf)
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
	want, err := hex.DecodeString(rootHex)
	if err != nil || len(want) != len(cur) {
		return false
	}
	return subtle.ConstantTimeCompare(cur, want) == 1
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
	// 0x01 domain tag: interior node.
	combined := make([]byte, 0, 1+len(left)+len(right))
	combined = append(combined, 0x01)
	combined = append(combined, left...)
	combined = append(combined, right...)
	h := sha256.Sum256(combined)
	return h[:]
}

// leafHash hashes a data item with the 0x00 leaf domain tag.
func leafHash(item []byte) []byte {
	h := sha256.Sum256(append([]byte{0x00}, item...))
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
