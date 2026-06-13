package signing

import (
	"crypto/ed25519"
	"crypto/rand"
	"testing"
)

func BenchmarkSign(b *testing.B) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		b.Fatalf("keygen: %v", err)
	}
	payload := ArticlePayload{
		ID:          "bench-id",
		Title:       "Benchmark Article",
		Body:        "This is a benchmark article body with sufficient content to measure signing overhead.",
		AuthorID:    "author-bench",
		PublishedAt: "2026-01-01T00:00:00Z",
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := Sign(priv, payload); err != nil {
			b.Fatalf("sign: %v", err)
		}
	}
}

func BenchmarkVerify(b *testing.B) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		b.Fatalf("keygen: %v", err)
	}
	payload := ArticlePayload{
		ID:          "bench-id",
		Title:       "Benchmark Article",
		Body:        "This is a benchmark article body with sufficient content to measure verification overhead.",
		AuthorID:    "author-bench",
		PublishedAt: "2026-01-01T00:00:00Z",
	}
	sa, err := Sign(priv, payload)
	if err != nil {
		b.Fatalf("sign: %v", err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := Verify(sa); err != nil {
			b.Fatalf("verify: %v", err)
		}
	}
}
