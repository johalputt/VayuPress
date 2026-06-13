package schema_test

import (
	"testing"

	"github.com/johalputt/vayupress/internal/events/schema"
)

func BenchmarkValidate(b *testing.B) {
	payload := map[string]interface{}{
		"id":           "abc-123",
		"title":        "Hello World",
		"author_id":    "user-1",
		"published_at": "2026-01-01T00:00:00Z",
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = schema.Global.Validate("article.created", "v1", payload)
	}
}
