package substackparse

import (
	"strings"
	"testing"
	"time"
)

func TestSlugify(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"Hello World", "hello-world"},
		{"My_Article_Here", "my-article-here"},
		{"  Leading spaces  ", "leading-spaces"},
		{"multiple---hyphens", "multiple-hyphens"},
		{"Special! @#$ Characters", "special-characters"},
		{"123 Numbers", "123-numbers"},
		{"", ""},
	}
	for _, tt := range tests {
		got := Slugify(tt.input)
		if got != tt.want {
			t.Errorf("Slugify(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestExtractSlugFromURL(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"https://author.substack.com/p/my-slug", "my-slug"},
		{"https://author.substack.com/p/hello-world-post", "hello-world-post"},
		{"https://author.substack.com/about", ""},
		{"not-a-url", ""},
		{"", ""},
	}
	for _, tt := range tests {
		got := extractSlugFromURL(tt.input)
		if got != tt.want {
			t.Errorf("extractSlugFromURL(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestParseCSVReader(t *testing.T) {
	csvData := `post_id,title,subtitle,url,type,draft,paid,publish_date,audience,body_html
post1,First Post,A subtitle,https://author.substack.com/p/first-post,newsletter,false,false,2024-01-15,everyone,<p>Content one</p>
post2,Second Post,Another,https://author.substack.com/p/second-post,newsletter,false,false,2024-03-20T10:00:00Z,everyone,<p>Content two</p>
post3,Draft Post,Draft sub,https://author.substack.com/p/draft-post,newsletter,true,false,2024-06-01,everyone,<p>Draft content</p>
`

	t.Run("skip drafts", func(t *testing.T) {
		articles, err := ParseCSVReader(strings.NewReader(csvData), true)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(articles) != 2 {
			t.Fatalf("expected 2 articles, got %d", len(articles))
		}

		// Check first article
		a := articles[0]
		if a.ID != "post1" {
			t.Errorf("expected ID=post1, got %q", a.ID)
		}
		if a.Title != "First Post" {
			t.Errorf("expected Title='First Post', got %q", a.Title)
		}
		if a.Slug != "first-post" {
			t.Errorf("expected Slug='first-post', got %q", a.Slug)
		}
		if a.Content != "<p>Content one</p>" {
			t.Errorf("expected Content='<p>Content one</p>', got %q", a.Content)
		}
		expectedDate := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)
		if !a.CreatedAt.Equal(expectedDate) {
			t.Errorf("expected CreatedAt=%v, got %v", expectedDate, a.CreatedAt)
		}
		if a.IsDraft {
			t.Error("expected IsDraft=false")
		}

		// Check second article (RFC3339 date)
		a2 := articles[1]
		if a2.ID != "post2" {
			t.Errorf("expected ID=post2, got %q", a2.ID)
		}
		if a2.Slug != "second-post" {
			t.Errorf("expected Slug='second-post', got %q", a2.Slug)
		}
		expectedDate2 := time.Date(2024, 3, 20, 10, 0, 0, 0, time.UTC)
		if !a2.CreatedAt.Equal(expectedDate2) {
			t.Errorf("expected CreatedAt=%v, got %v", expectedDate2, a2.CreatedAt)
		}
	})

	t.Run("include drafts", func(t *testing.T) {
		articles, err := ParseCSVReader(strings.NewReader(csvData), false)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(articles) != 3 {
			t.Fatalf("expected 3 articles (including draft), got %d", len(articles))
		}
		draft := articles[2]
		if draft.ID != "post3" {
			t.Errorf("expected ID=post3, got %q", draft.ID)
		}
		if !draft.IsDraft {
			t.Error("expected IsDraft=true for draft post")
		}
	})
}
