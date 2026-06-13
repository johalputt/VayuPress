package ai_test

import (
	"testing"

	"github.com/johalputt/vayupress/internal/ai"
)

func BenchmarkEmbed(b *testing.B) {
	e := ai.NewLocalEmbedder(384)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = e.Embed("benchmark text for embedding performance")
	}
}

func BenchmarkCosineSimilarity(b *testing.B) {
	e := ai.NewLocalEmbedder(384)
	v1, _ := e.Embed("hello world")
	v2, _ := e.Embed("hi there")
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = ai.CosineSimilarity(v1, v2)
	}
}
