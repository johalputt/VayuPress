package schema_test

import (
	"testing"

	"github.com/johalputt/vayupress/internal/events/schema"
)

func TestValidate_OK(t *testing.T) {
	payload := map[string]interface{}{
		"id":           "abc-123",
		"title":        "Hello World",
		"author_id":    "user-1",
		"published_at": "2026-01-01T00:00:00Z",
	}
	if err := schema.Global.Validate("article.created", "v1", payload); err != nil {
		t.Errorf("expected valid: %v", err)
	}
}

func TestValidate_MissingRequired(t *testing.T) {
	payload := map[string]interface{}{
		"id":    "abc-123",
		"title": "Hello World",
		// missing author_id, published_at
	}
	err := schema.Global.Validate("article.created", "v1", payload)
	if err == nil {
		t.Error("expected error for missing required field")
	}
}

func TestValidate_UnknownSchema(t *testing.T) {
	err := schema.Global.Validate("nonexistent.event", "v99", map[string]interface{}{})
	if err == nil {
		t.Error("expected error for unknown schema")
	}
}

func TestValidate_WrongType(t *testing.T) {
	payload := map[string]interface{}{
		"id":           123, // should be string
		"title":        "Hello",
		"author_id":    "user-1",
		"published_at": "2026-01-01T00:00:00Z",
	}
	err := schema.Global.Validate("article.created", "v1", payload)
	if err == nil {
		t.Error("expected type error")
	}
}
