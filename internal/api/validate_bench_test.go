package api

import "testing"

func BenchmarkValidateArticleInput(b *testing.B) {
	tags := []string{"go", "web", "performance"}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = ValidateArticleInput("Benchmark Title", "bench-slug", "<p>content</p>", tags)
	}
}

func BenchmarkSplitTags(b *testing.B) {
	s := "go,web,performance,database,sqlite,search,cache,cdn,auth,api"
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = SplitTags(s)
	}
}

func BenchmarkIsValidSlug(b *testing.B) {
	for i := 0; i < b.N; i++ {
		_ = IsValidSlug("my-article-slug-2024")
	}
}
