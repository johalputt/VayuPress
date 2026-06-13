// Package graph implements a sovereign knowledge graph for VayuPress.
// Posts and entities are nodes; references, translations, and updates are edges.
// Supports graph traversal and predecessor/successor queries.
package graph

import (
	"errors"
	"fmt"
	"sync"
)

// NodeType classifies a knowledge graph node.
type NodeType string

const (
	NodePost   NodeType = "post"
	NodeAuthor NodeType = "author"
	NodeTag    NodeType = "tag"
	NodeTopic  NodeType = "topic"
)

// EdgeType classifies a relationship between nodes.
type EdgeType string

const (
	EdgeReferences  EdgeType = "references"
	EdgeTranslates  EdgeType = "translates"
	EdgeUpdates     EdgeType = "updates"
	EdgeTagged      EdgeType = "tagged"
	EdgeAuthored    EdgeType = "authored"
)

// Node is a vertex in the knowledge graph.
type Node struct {
	ID    string            `json:"id"`
	Type  NodeType          `json:"type"`
	Attrs map[string]string `json:"attrs,omitempty"`
}

// Edge is a directed relationship between two nodes.
type Edge struct {
	From   string   `json:"from"`
	To     string   `json:"to"`
	Type   EdgeType `json:"type"`
	Weight float64  `json:"weight,omitempty"`
}

// Graph is an in-memory directed knowledge graph.
type Graph struct {
	mu    sync.RWMutex
	nodes map[string]*Node
	edges []Edge
	out   map[string][]Edge // adjacency: nodeID → outgoing edges
}

// New creates an empty Graph.
func New() *Graph {
	return &Graph{
		nodes: make(map[string]*Node),
		out:   make(map[string][]Edge),
	}
}

// AddNode adds a node. Returns error if ID already exists.
func (g *Graph) AddNode(n *Node) error {
	if n.ID == "" {
		return errors.New("graph: node ID required")
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if _, exists := g.nodes[n.ID]; exists {
		return fmt.Errorf("graph: node %s already exists", n.ID)
	}
	g.nodes[n.ID] = n
	return nil
}

// AddEdge adds a directed edge. Both endpoints must exist.
func (g *Graph) AddEdge(e Edge) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if _, ok := g.nodes[e.From]; !ok {
		return fmt.Errorf("graph: node %s not found", e.From)
	}
	if _, ok := g.nodes[e.To]; !ok {
		return fmt.Errorf("graph: node %s not found", e.To)
	}
	g.edges = append(g.edges, e)
	g.out[e.From] = append(g.out[e.From], e)
	return nil
}

// GetNode returns a node by ID.
func (g *Graph) GetNode(id string) (*Node, bool) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	n, ok := g.nodes[id]
	return n, ok
}

// Successors returns all nodes reachable from id via edges of type edgeType.
// Pass "" for edgeType to return all successors.
func (g *Graph) Successors(id string, edgeType EdgeType) []*Node {
	g.mu.RLock()
	defer g.mu.RUnlock()
	var out []*Node
	for _, e := range g.out[id] {
		if edgeType == "" || e.Type == edgeType {
			if n, ok := g.nodes[e.To]; ok {
				out = append(out, n)
			}
		}
	}
	return out
}

// NodesByType returns all nodes of the given type.
func (g *Graph) NodesByType(t NodeType) []*Node {
	g.mu.RLock()
	defer g.mu.RUnlock()
	var out []*Node
	for _, n := range g.nodes {
		if n.Type == t {
			out = append(out, n)
		}
	}
	return out
}

// Stats returns node and edge counts.
func (g *Graph) Stats() (nodes, edges int) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return len(g.nodes), len(g.edges)
}
