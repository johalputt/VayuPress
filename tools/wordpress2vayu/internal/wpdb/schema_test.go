package wpdb

import (
	"testing"
)

func TestSlugify(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"Hello World", "hello-world"},
		{"Hello  World", "hello-world"},
		{"  leading and trailing  ", "leading-and-trailing"},
		{"Special! Characters@#$", "special-characters"},
		{"already-slugified", "already-slugified"},
		{"UPPERCASE TITLE", "uppercase-title"},
		{"Numbers 123 Here", "numbers-123-here"},
		{"", ""},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := slugify(tt.input)
			if got != tt.want {
				t.Errorf("slugify(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestBuildBaseQueryCount(t *testing.T) {
	query, args := buildBaseQuery("wp_", "publish", "post", true, "", 0)
	if query == "" {
		t.Fatal("expected non-empty query")
	}
	// Should have status and post_type args.
	if len(args) != 2 {
		t.Errorf("expected 2 args, got %d: %v", len(args), args)
	}
}

func TestBuildBaseQueryFetch(t *testing.T) {
	query, args := buildBaseQuery("wp_", "publish", "post", false, "42", 50)
	if query == "" {
		t.Fatal("expected non-empty query")
	}
	// args: status, post_type, afterID, limit = 4
	if len(args) != 4 {
		t.Errorf("expected 4 args, got %d: %v", len(args), args)
	}
}

func TestBuildBaseQueryAllStatus(t *testing.T) {
	_, args := buildBaseQuery("wp_", "all", "both", true, "", 0)
	// No args for status or post_type when using IN() literals.
	if len(args) != 0 {
		t.Errorf("expected 0 args for all/both, got %d: %v", len(args), args)
	}
}
