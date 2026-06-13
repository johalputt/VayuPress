// Package semantic provides vector-embedding-based semantic search for VayuPress.
// Posts are indexed by their embedding vectors; queries find nearest neighbours
// by cosine similarity. Uses in-memory flat scan (replace with HNSW for scale).
package semantic

import (
	"fmt"
	"sort"
	"sync"

	"github.com/johalputt/vayupress/internal/ai"
)

// IndexedPost holds a post and its embedding vector.
type IndexedPost struct {
	ID        string
	Title     string
	Body      string
	Embedding ai.Embedding
}

// Result is a semantic search hit with similarity score.
type Result struct {
	ID         string  `json:"id"`
	Title      string  `json:"title"`
	Similarity float32 `json:"similarity"`
}

// Index is an in-memory semantic search index backed by an Embedder.
type Index struct {
	mu       sync.RWMutex
	embedder ai.Embedder
	posts    []*IndexedPost
}

// New creates a semantic Index with the given embedder.
func New(e ai.Embedder) *Index {
	return &Index{embedder: e}
}

// Add embeds and indexes a post.
func (idx *Index) Add(id, title, body string) error {
	text := title + " " + body
	emb, err := idx.embedder.Embed(text)
	if err != nil {
		return fmt.Errorf("semantic: embed %s: %w", id, err)
	}
	idx.mu.Lock()
	idx.posts = append(idx.posts, &IndexedPost{
		ID: id, Title: title, Body: body, Embedding: emb,
	})
	idx.mu.Unlock()
	return nil
}

// Search returns the top-k posts most similar to query.
func (idx *Index) Search(query string, k int) ([]Result, error) {
	qEmb, err := idx.embedder.Embed(query)
	if err != nil {
		return nil, fmt.Errorf("semantic: embed query: %w", err)
	}
	idx.mu.RLock()
	posts := make([]*IndexedPost, len(idx.posts))
	copy(posts, idx.posts)
	idx.mu.RUnlock()

	type scored struct {
		post *IndexedPost
		sim  float32
	}
	scores := make([]scored, 0, len(posts))
	for _, p := range posts {
		scores = append(scores, scored{p, ai.CosineSimilarity(qEmb, p.Embedding)})
	}
	sort.Slice(scores, func(i, j int) bool { return scores[i].sim > scores[j].sim })
	if k <= 0 || k > len(scores) {
		k = len(scores)
	}
	out := make([]Result, 0, k)
	for _, s := range scores[:k] {
		out = append(out, Result{ID: s.post.ID, Title: s.post.Title, Similarity: s.sim})
	}
	return out, nil
}

// Size returns the number of indexed posts.
func (idx *Index) Size() int {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	return len(idx.posts)
}
