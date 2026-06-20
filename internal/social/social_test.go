package social

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDisabledWhenUnconfigured(t *testing.T) {
	p := New(MastodonConfig{}, nil)
	if p.Enabled() {
		t.Fatal("expected disabled poster")
	}
	if err := p.Share(context.Background(), "Title", "https://x/y"); err != nil {
		t.Fatalf("disabled Share should be nil, got %v", err)
	}
}

func TestShareToMastodon(t *testing.T) {
	var gotAuth, gotStatus string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		body, _ := io.ReadAll(r.Body)
		gotStatus = string(body)
		w.WriteHeader(200)
		w.Write([]byte(`{"id":"1"}`))
	}))
	defer srv.Close()

	p := New(MastodonConfig{Instance: srv.URL, Token: "tok123"}, srv.Client())
	if !p.Enabled() {
		t.Fatal("expected enabled poster")
	}
	if err := p.Share(context.Background(), "My Post", "https://site/my-post"); err != nil {
		t.Fatal(err)
	}
	if gotAuth != "Bearer tok123" {
		t.Errorf("auth header = %q", gotAuth)
	}
	if !strings.Contains(gotStatus, "My+Post") || !strings.Contains(gotStatus, "my-post") {
		t.Errorf("status body missing title/link: %q", gotStatus)
	}
}

func TestBuildStatusTrimsLongTitle(t *testing.T) {
	long := strings.Repeat("a", 500)
	s := buildStatus(long, "https://x/y")
	if len(s) > 400+len("https://x/y")+2 {
		t.Errorf("status not trimmed, len=%d", len(s))
	}
}
