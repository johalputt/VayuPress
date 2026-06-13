// Package ai provides local AI inference for VayuPress.
// All inference runs in-process using local models — no external API calls.
package ai

import (
	"crypto/sha256"
	"encoding/binary"
	"math"
)

// Embedding is a dense float32 vector representation of text.
type Embedding []float32

// Embedder generates vector embeddings from text.
type Embedder interface {
	Embed(text string) (Embedding, error)
	Dimensions() int
}

// LocalEmbedder is a deterministic local embedder that uses
// a hash-based projection as a lightweight stand-in for a real model.
// Replace with an ONNX Runtime backend for production use.
type LocalEmbedder struct {
	dims int
}

// NewLocalEmbedder creates a LocalEmbedder with the given output dimensions.
func NewLocalEmbedder(dims int) *LocalEmbedder {
	if dims <= 0 {
		dims = 384 // all-MiniLM-L6-v2 compatible
	}
	return &LocalEmbedder{dims: dims}
}

// Embed generates a deterministic embedding vector for text.
func (e *LocalEmbedder) Embed(text string) (Embedding, error) {
	vec := make(Embedding, e.dims)
	// Use repeated SHA-256 to fill the embedding dimensions.
	// This is a hash projection — semantically meaningless but deterministic.
	// Replace with ONNX model inference for real semantic search.
	seed := []byte(text)
	for i := 0; i < e.dims; i += 8 {
		h := sha256.Sum256(append(seed, byte(i>>8), byte(i)))
		for j := 0; j < 8 && i+j < e.dims; j++ {
			bits := binary.LittleEndian.Uint32(h[j*4:])
			// Map to [-1, 1]
			vec[i+j] = (float32(bits)/float32(math.MaxUint32))*2 - 1
		}
	}
	normalize(vec)
	return vec, nil
}

// Dimensions returns the embedding vector size.
func (e *LocalEmbedder) Dimensions() int { return e.dims }

// CosineSimilarity computes the cosine similarity of two equal-length embeddings.
func CosineSimilarity(a, b Embedding) float32 {
	if len(a) != len(b) {
		return 0
	}
	var dot, normA, normB float32
	for i := range a {
		dot += a[i] * b[i]
		normA += a[i] * a[i]
		normB += b[i] * b[i]
	}
	if normA == 0 || normB == 0 {
		return 0
	}
	return dot / (float32(math.Sqrt(float64(normA))) * float32(math.Sqrt(float64(normB))))
}

func normalize(v Embedding) {
	var norm float32
	for _, x := range v {
		norm += x * x
	}
	if norm == 0 {
		return
	}
	scale := float32(1.0 / math.Sqrt(float64(norm)))
	for i := range v {
		v[i] *= scale
	}
}
