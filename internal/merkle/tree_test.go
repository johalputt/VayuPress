package merkle_test

import (
	"testing"

	"github.com/johalputt/vayupress/internal/merkle"
)

func TestMerkleRootConsistent(t *testing.T) {
	items := [][]byte{[]byte("post1"), []byte("post2"), []byte("post3")}
	t1, err := merkle.New(items)
	if err != nil {
		t.Fatal(err)
	}
	t2, _ := merkle.New(items)
	if t1.Root() != t2.Root() {
		t.Error("root not deterministic")
	}
}

func TestMerkleProofVerify(t *testing.T) {
	items := [][]byte{[]byte("a"), []byte("b"), []byte("c"), []byte("d")}
	tree, err := merkle.New(items)
	if err != nil {
		t.Fatal(err)
	}
	for i, item := range items {
		proof, err := tree.Proof(i)
		if err != nil {
			t.Fatalf("Proof(%d): %v", i, err)
		}
		if !merkle.Verify(item, i, proof, tree.Root()) {
			t.Errorf("Verify failed for index %d", i)
		}
	}
}

func TestTamperedLeaf(t *testing.T) {
	items := [][]byte{[]byte("a"), []byte("b")}
	tree, _ := merkle.New(items)
	proof, _ := tree.Proof(0)
	if merkle.Verify([]byte("TAMPERED"), 0, proof, tree.Root()) {
		t.Error("tampered leaf should not verify")
	}
}
