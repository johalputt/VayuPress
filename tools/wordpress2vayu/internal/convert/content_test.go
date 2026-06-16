package convert

import (
	"testing"
)

func TestCleanHTML(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "strips HTML comments",
			input: "<p>Hello</p><!-- this is a comment --><p>World</p>",
			want:  "<p>Hello</p><p>World</p>",
		},
		{
			name:  "strips multiline HTML comment",
			input: "<p>Before</p>\n<!-- \n multi\n line\n -->\n<p>After</p>",
			want:  "<p>Before</p>\n\n<p>After</p>",
		},
		{
			name:  "collapses 3+ blank lines to 2",
			input: "<p>A</p>\n\n\n\n<p>B</p>",
			want:  "<p>A</p>\n\n<p>B</p>",
		},
		{
			name:  "collapses exactly 3 blank lines to 2",
			input: "<p>A</p>\n\n\n<p>B</p>",
			want:  "<p>A</p>\n\n<p>B</p>",
		},
		{
			name:  "leaves 2 blank lines alone",
			input: "<p>A</p>\n\n<p>B</p>",
			want:  "<p>A</p>\n\n<p>B</p>",
		},
		{
			name:  "passthrough unchanged content",
			input: "<p>Hello World</p>",
			want:  "<p>Hello World</p>",
		},
		{
			name:  "strips comment and collapses",
			input: "<!-- comment -->\n\n\n\n<p>Content</p>",
			want:  "<p>Content</p>",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CleanHTML(tt.input)
			if got != tt.want {
				t.Errorf("CleanHTML(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
