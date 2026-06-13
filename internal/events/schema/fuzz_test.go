package schema_test

import (
	"encoding/json"
	"testing"

	"github.com/johalputt/vayupress/internal/events/schema"
)

// FuzzValidate feeds arbitrary JSON payloads to schema validation.
// Must never panic.
func FuzzValidate(f *testing.F) {
	f.Add(`{"id":"1","title":"t","author_id":"u","published_at":"2026-01-01T00:00:00Z"}`)
	f.Add(`{}`)
	f.Add(`{"id":null}`)
	f.Fuzz(func(t *testing.T, data string) {
		var payload map[string]interface{}
		if err := json.Unmarshal([]byte(data), &payload); err != nil {
			return // skip invalid JSON — not our concern
		}
		_ = schema.Global.Validate("article.created", "v1", payload)
	})
}
