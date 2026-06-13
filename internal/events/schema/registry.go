// Package schema provides JSON Schema governance for VayuPress events.
// Each event type+version has a registered schema; emitters validate before
// publishing and the registry enforces backward compatibility.
package schema

import (
	"encoding/json"
	"fmt"
	"sync"
)

// Schema is a minimal JSON Schema descriptor for an event version.
type Schema struct {
	Type       string            `json:"type"`    // e.g. "article.created"
	Version    string            `json:"version"` // e.g. "v1"
	Required   []string          `json:"required"`
	Properties map[string]PropDef `json:"properties"`
}

// PropDef describes a single field in a schema.
type PropDef struct {
	Type   string `json:"type"`   // "string", "integer", "boolean", "object"
	Format string `json:"format"` // e.g. "date-time"
}

// Registry stores and validates event schemas.
type Registry struct {
	mu      sync.RWMutex
	schemas map[string]*Schema // key: "type@version"
}

// NewRegistry returns an initialized Registry.
func NewRegistry() *Registry {
	return &Registry{schemas: make(map[string]*Schema)}
}

// Global is the default registry used by VayuPress.
var Global = NewRegistry()

// Register adds a schema to the registry. Panics on duplicate.
func (r *Registry) Register(s *Schema) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.schemas == nil {
		r.schemas = make(map[string]*Schema)
	}
	key := s.Type + "@" + s.Version
	if _, exists := r.schemas[key]; exists {
		panic(fmt.Sprintf("schema: duplicate registration for %s", key))
	}
	r.schemas[key] = s
}

// Validate checks that payload satisfies the schema for eventType+version.
// Returns nil if valid, an error describing the first violation otherwise.
func (r *Registry) Validate(eventType, version string, payload map[string]interface{}) error {
	r.mu.RLock()
	s, ok := r.schemas[eventType+"@"+version]
	r.mu.RUnlock()
	if !ok {
		return fmt.Errorf("schema: no schema registered for %s@%s", eventType, version)
	}
	for _, req := range s.Required {
		if _, present := payload[req]; !present {
			return fmt.Errorf("schema: missing required field %q in %s@%s", req, eventType, version)
		}
	}
	for field, val := range payload {
		def, defined := s.Properties[field]
		if !defined {
			continue // unknown fields are allowed (forward-compat)
		}
		if err := checkType(field, val, def); err != nil {
			return fmt.Errorf("schema: %s@%s: %w", eventType, version, err)
		}
	}
	return nil
}

// MarshalJSON renders the schema registry state for diagnostics.
func (r *Registry) MarshalJSON() ([]byte, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return json.Marshal(r.schemas)
}

func checkType(field string, val interface{}, def PropDef) error {
	switch def.Type {
	case "string":
		if _, ok := val.(string); !ok {
			return fmt.Errorf("field %q: expected string, got %T", field, val)
		}
	case "integer":
		switch val.(type) {
		case int, int64, float64:
		default:
			return fmt.Errorf("field %q: expected integer, got %T", field, val)
		}
	case "boolean":
		if _, ok := val.(bool); !ok {
			return fmt.Errorf("field %q: expected boolean, got %T", field, val)
		}
	}
	return nil
}
