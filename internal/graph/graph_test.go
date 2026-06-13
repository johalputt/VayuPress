package graph_test

import (
	"testing"

	"github.com/johalputt/vayupress/internal/graph"
)

func TestGraphCRUD(t *testing.T) {
	g := graph.New()

	post1 := &graph.Node{ID: "post-1", Type: graph.NodePost, Attrs: map[string]string{"title": "Hello"}}
	post2 := &graph.Node{ID: "post-2", Type: graph.NodePost, Attrs: map[string]string{"title": "Follow-up"}}
	author := &graph.Node{ID: "author-1", Type: graph.NodeAuthor}

	for _, n := range []*graph.Node{post1, post2, author} {
		if err := g.AddNode(n); err != nil {
			t.Fatalf("AddNode %s: %v", n.ID, err)
		}
	}

	if err := g.AddEdge(graph.Edge{From: "post-2", To: "post-1", Type: graph.EdgeReferences}); err != nil {
		t.Fatalf("AddEdge: %v", err)
	}
	if err := g.AddEdge(graph.Edge{From: "post-1", To: "author-1", Type: graph.EdgeAuthored}); err != nil {
		t.Fatalf("AddEdge: %v", err)
	}

	succ := g.Successors("post-2", graph.EdgeReferences)
	if len(succ) != 1 || succ[0].ID != "post-1" {
		t.Errorf("expected post-1 as successor, got %v", succ)
	}

	posts := g.NodesByType(graph.NodePost)
	if len(posts) != 2 {
		t.Errorf("expected 2 posts, got %d", len(posts))
	}

	nodes, edges := g.Stats()
	if nodes != 3 || edges != 2 {
		t.Errorf("stats: got nodes=%d edges=%d", nodes, edges)
	}
}
