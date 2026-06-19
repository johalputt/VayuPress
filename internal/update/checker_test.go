package update

import (
	"context"
	"net/http"
	"net/http/httptest"
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

// rewriteTransport redirects all requests to a fixed base URL (the test server).
type rewriteTransport struct{ target string }

func (rt rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	u, _ := req.URL.Parse(rt.target)
	req.URL.Scheme = u.Scheme
	req.URL.Host = u.Host
	return http.DefaultTransport.RoundTrip(req)
}
