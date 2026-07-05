package update

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestCompareVersions(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"v1.0.0", "v1.1.0", -1},
		{"v1.1.0", "v1.10.0", -1},
		{"v1.10.0", "v1.1.0", 1},
		{"1.2.3", "1.2.3", 0},
		{"v1.2.3", "1.2.3", 0},
		{"v2.0.0", "v1.9.9", 1},
		{"v1.0.0-rc1", "v1.0.0", 0}, // prerelease stripped
		{"v1.0", "v1.0.0", 0},
	}
	for _, c := range cases {
		if got := CompareVersions(c.a, c.b); got != c.want {
			t.Errorf("CompareVersions(%q,%q)=%d want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestUpdateAvailable(t *testing.T) {
	if !UpdateAvailable("v1.0.0", "v1.1.0") {
		t.Error("expected update available")
	}
	if UpdateAvailable("v1.1.0", "v1.0.0") {
		t.Error("expected no update (downgrade)")
	}
	if UpdateAvailable("v1.1.0", "v1.1.0") {
		t.Error("expected no update (equal)")
	}
}

func TestCheckLatest(t *testing.T) {
	body := `{
		"tag_name": "v1.2.0",
		"body": "## Changes\n- thing",
		"html_url": "https://example/releases/v1.2.0",
		"published_at": "2026-01-02T03:04:05Z",
		"assets": [
			{"name":"vayupress-linux-amd64.tar.gz","browser_download_url":"https://example/bin.tar.gz","size":1234}
		]
	}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Accept"); got != "application/vnd.github+json" {
			t.Errorf("Accept header = %q", got)
		}
		w.WriteHeader(200)
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	// Point CheckLatest at our test server by overriding via a custom transport.
	client := &http.Client{
		Timeout:   5 * time.Second,
		Transport: rewriteTransport{target: srv.URL},
	}
	rel, err := CheckLatest(context.Background(), client, "johalputt", "vayupress")
	if err != nil {
		t.Fatalf("CheckLatest: %v", err)
	}
	if rel.Version != "v1.2.0" {
		t.Errorf("version = %q", rel.Version)
	}
	if len(rel.Assets) != 1 || rel.Assets[0].Size != 1234 {
		t.Errorf("assets = %+v", rel.Assets)
	}
	if rel.Published.IsZero() {
		t.Error("published time not parsed")
	}
}

func TestCheckLatestChannel(t *testing.T) {
	// `releases/latest` 404s (GitHub never returns a pre-release there); the list
	// carries a newer pre-release (v1.3.0-rc1) above the newest stable (v1.2.0).
	list := `[
		{"tag_name":"v1.3.0-rc1","prerelease":true,"draft":false,"published_at":"2026-02-01T00:00:00Z",
		 "assets":[{"name":"vayupress-linux-amd64.tar.gz","browser_download_url":"https://example/rc.tar.gz","size":10}]},
		{"tag_name":"v1.2.0","prerelease":false,"draft":false,"published_at":"2026-01-01T00:00:00Z",
		 "assets":[{"name":"vayupress-linux-amd64.tar.gz","browser_download_url":"https://example/stable.tar.gz","size":20}]}
	]`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/releases/latest") {
			w.WriteHeader(404)
			return
		}
		w.WriteHeader(200)
		_, _ = w.Write([]byte(list))
	}))
	defer srv.Close()
	client := &http.Client{Timeout: 5 * time.Second, Transport: rewriteTransport{target: srv.URL}}

	// Stable channel skips the pre-release.
	rel, err := CheckLatestChannel(context.Background(), client, "o", "r", false)
	if err != nil {
		t.Fatalf("stable channel: %v", err)
	}
	if rel.Version != "v1.2.0" {
		t.Errorf("stable channel version = %q, want v1.2.0", rel.Version)
	}

	// Development channel offers the newer pre-release.
	rel, err = CheckLatestChannel(context.Background(), client, "o", "r", true)
	if err != nil {
		t.Fatalf("dev channel: %v", err)
	}
	if rel.Version != "v1.3.0-rc1" {
		t.Errorf("dev channel version = %q, want v1.3.0-rc1", rel.Version)
	}
}

// rewriteTransport redirects all requests to a fixed base URL (the test server).
type rewriteTransport struct{ target string }

func (rt rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	u, _ := req.URL.Parse(rt.target)
	req.URL.Scheme = u.Scheme
	req.URL.Host = u.Host
	return http.DefaultTransport.RoundTrip(req)
}
