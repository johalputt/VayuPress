package secwatch

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDisabledMakesNoNetworkCalls(t *testing.T) {
	t.Parallel()
	hit := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit = true
	}))
	defer srv.Close()
	w := New(false)
	w.apiBase = srv.URL
	rep, err := w.Check(context.Background())
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if rep.Enabled {
		t.Fatalf("report should be disabled")
	}
	if hit {
		t.Fatalf("disabled watcher must not call the network")
	}
}

func TestDetectsUpdate(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"tag_name":"v99.0.0"}`))
	}))
	defer srv.Close()
	w := New(true)
	w.apiBase = srv.URL
	// Inject one synthetic component so the test does not depend on build info.
	rep := &Report{Enabled: true, Components: []Component{{Name: "go-crypto", Module: "github.com/ProtonMail/go-crypto", Repo: "ProtonMail/go-crypto", Current: "v1.4.1"}}}
	for i := range rep.Components {
		c := &rep.Components[i]
		latest, err := w.latestRelease(context.Background(), c.Repo)
		if err != nil {
			t.Fatalf("latest: %v", err)
		}
		c.Latest = latest
		if normalizeVer(latest) != normalizeVer(c.Current) {
			c.UpdateAvailable = true
		}
	}
	if !rep.Components[0].UpdateAvailable {
		t.Fatalf("expected update available for v1.4.1 -> v99.0.0")
	}
}

func TestShortNameSkipsMajorSuffix(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"github.com/go-chi/chi/v5":           "chi",
		"github.com/ProtonMail/go-crypto":    "go-crypto",
		"github.com/cloudflare/circl":        "circl",
		"github.com/mattn/go-sqlite3":        "go-sqlite3",
		"github.com/microcosm-cc/bluemonday": "bluemonday",
		"example.com/foo/v10":                "foo",
	}
	for module, want := range cases {
		if got := shortName(module); got != want {
			t.Errorf("shortName(%q) = %q, want %q", module, got, want)
		}
	}
}

func TestIsNewer(t *testing.T) {
	t.Parallel()
	cases := []struct {
		latest, current string
		want            bool
	}{
		{"v1.4.2", "v1.4.1", true},
		{"v2.0.0", "v1.9.9", true},
		{"v1.14.47", "v1.14.47", false}, // equal → not newer
		{"v1.4.0", "v1.4.1", false},     // older → not newer
		{"v1.6.4", "v1.6.4", false},
		{"v5.3.0", "v5.3.0", false},
	}
	for _, c := range cases {
		if got := isNewer(c.latest, c.current); got != c.want {
			t.Errorf("isNewer(%q,%q) = %v, want %v", c.latest, c.current, got, c.want)
		}
	}
}

func TestNormalizeVer(t *testing.T) {
	t.Parallel()
	if normalizeVer("v1.4.1") != normalizeVer("1.4.1") {
		t.Fatalf("v-prefix should normalize")
	}
	if normalizeVer("v1.4.1-rc1") != "1.4.1" {
		t.Fatalf("prerelease should strip")
	}
}
