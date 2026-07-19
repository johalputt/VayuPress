package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// usePlainHTMLFetch swaps the SSRF-safe page fetcher (which blocks the loopback
// httptest server) for a plain GET, for the duration of a test.
func usePlainHTMLFetch(t *testing.T) {
	t.Helper()
	prev := pageHTMLFetch
	pageHTMLFetch = func(ctx context.Context, rawURL string) ([]byte, bool) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
		if err != nil {
			return nil, false
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, false
		}
		defer resp.Body.Close()
		b, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, false
		}
		return b, true
	}
	t.Cleanup(func() { pageHTMLFetch = prev })
}

func TestLooksLikeDirectImage(t *testing.T) {
	yes := []string{
		"https://cdn.pixabay.com/photo/x.jpg",
		"https://images.unsplash.com/photo-123.PNG?w=800",
		"https://x/y.webp",
		"/media/a.gif",
	}
	no := []string{
		"https://pixabay.com/photos/cat-123/",
		"https://unsplash.com/photos/abcDEF",
		"https://example.com/page",
	}
	for _, u := range yes {
		if !looksLikeDirectImage(u) {
			t.Errorf("looksLikeDirectImage(%q) = false, want true", u)
		}
	}
	for _, u := range no {
		if looksLikeDirectImage(u) {
			t.Errorf("looksLikeDirectImage(%q) = true, want false", u)
		}
	}
}

// TestResolveImageLinkFromPage confirms a *page* URL is resolved to the direct
// image it advertises via og:image, and a direct image / site-relative URL is
// left untouched (no fetch, no rewrite).
func TestResolveImageLinkFromPage(t *testing.T) {
	usePlainHTMLFetch(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<html><head><meta property="og:image" content="` +
			"https://cdn.example.com/real/photo.jpg" + `"></head><body>hi</body></html>`))
	}))
	defer srv.Close()

	a := &App{}
	ctx := context.Background()

	got := a.resolveImageLink(ctx, srv.URL+"/photos/cat-123/")
	if got != "https://cdn.example.com/real/photo.jpg" {
		t.Errorf("page URL should resolve to og:image, got %q", got)
	}
	// A direct image link is returned unchanged (and does no fetch).
	direct := "https://cdn.example.com/x.png"
	if got := a.resolveImageLink(ctx, direct); got != direct {
		t.Errorf("direct image URL should pass through, got %q", got)
	}
	// Site-relative stays put.
	if got := a.resolveImageLink(ctx, "/media/a.jpg"); got != "/media/a.jpg" {
		t.Errorf("site-relative URL should pass through, got %q", got)
	}
}

// TestResolveBlockImagesRewritesPageURLs confirms image/gallery block URLs that
// are page links get rewritten to their og:image while other fields and blocks
// are preserved.
func TestResolveBlockImagesRewritesPageURLs(t *testing.T) {
	usePlainHTMLFetch(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<meta property="og:image" content="https://cdn.x/resolved.jpg">`))
	}))
	defer srv.Close()

	a := &App{}
	blocks := `[{"type":"paragraph","text":"hi"},` +
		`{"type":"image","url":"` + srv.URL + `/photos/1/","alt":"cat"},` +
		`{"type":"image","url":"https://cdn.x/direct.png"}]`
	out := a.resolveBlockImages(context.Background(), blocks)
	if !strings.Contains(out, "https://cdn.x/resolved.jpg") {
		t.Errorf("page image URL should be rewritten to og:image, got:\n%s", out)
	}
	if !strings.Contains(out, `"alt":"cat"`) {
		t.Errorf("other block fields must be preserved, got:\n%s", out)
	}
	if !strings.Contains(out, "https://cdn.x/direct.png") {
		t.Errorf("direct image URL must be left untouched, got:\n%s", out)
	}
	if !strings.Contains(out, `"text":"hi"`) {
		t.Errorf("non-image blocks must be preserved, got:\n%s", out)
	}
}
