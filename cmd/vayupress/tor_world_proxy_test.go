package main

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/vayuos/torspace"
)

func TestInTorWorldView(t *testing.T) {
	r := httptest.NewRequest("GET", "/os", nil)
	if inTorWorldView(r) {
		t.Error("no cookie should mean not viewing Tor")
	}
	r.AddCookie(&http.Cookie{Name: worldCookie, Value: "tor"})
	if !inTorWorldView(r) {
		t.Error("vp_world=tor cookie should mean viewing Tor")
	}
	r2 := httptest.NewRequest("GET", "/os", nil)
	r2.AddCookie(&http.Cookie{Name: worldCookie, Value: "clearnet"})
	if inTorWorldView(r2) {
		t.Error("vp_world=clearnet must NOT be treated as Tor view")
	}
}

// TestProxyToTorWorld verifies the proxy forwards into the Tor-world instance and
// authenticates with the child's OWN API key (never a clearnet session).
func TestProxyToTorWorld(t *testing.T) {
	var gotAuth, gotPath string
	child := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		_, _ = io.WriteString(w, "TOR-WORLD-DASHBOARD")
	}))
	defer child.Close()
	port := child.Listener.Addr().(*net.TCPAddr).Port

	a := &App{torSpace: torspace.New("", t.TempDir()+"/vayupress.db", "child-secret-key", port)}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/os/posts", nil)
	a.proxyToTorWorld(rec, req)

	if body := rec.Body.String(); !strings.Contains(body, "TOR-WORLD-DASHBOARD") {
		t.Errorf("expected the Tor world's response, got %q", body)
	}
	if gotAuth != "Bearer child-secret-key" {
		t.Errorf("proxy must authenticate with the child's API key, got %q", gotAuth)
	}
	if gotPath != "/os/posts" {
		t.Errorf("proxy must preserve the path, got %q", gotPath)
	}
}

// TestProxyToTorWorldUnavailable: when the child port/key is missing, the operator
// gets the friendly "starting" fallback, never a broken proxy error.
func TestProxyToTorWorldUnavailable(t *testing.T) {
	a := &App{torSpace: torspace.New("", t.TempDir()+"/vayupress.db", "", 0)} // no key/port
	rec := httptest.NewRecorder()
	a.proxyToTorWorld(rec, httptest.NewRequest("GET", "/os", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("missing child must yield 503 fallback, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Back to Clearnet") {
		t.Error("fallback must offer a way back to clearnet")
	}
}

// TestTorWorldMiddlewarePassthrough: without the Tor view cookie, the middleware
// is transparent (serves clearnet); and /os/world is always parent-handled.
func TestTorWorldMiddlewarePassthrough(t *testing.T) {
	nextCalled := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { nextCalled = true })
	a := &App{}
	mw := a.torWorldMiddleware(next)

	// No cookie → passthrough to clearnet.
	nextCalled = false
	mw.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/os", nil))
	if !nextCalled {
		t.Error("without the Tor view, the middleware must be transparent")
	}

	// /os/world is always handled by the parent route (passed through), even with
	// the Tor cookie set.
	nextCalled = false
	r := httptest.NewRequest("GET", "/os/world", nil)
	r.AddCookie(&http.Cookie{Name: worldCookie, Value: "tor"})
	mw.ServeHTTP(httptest.NewRecorder(), r)
	if !nextCalled {
		t.Error("/os/world must always reach the parent handler so the operator can flip back")
	}
}
