// Command screenshot-proxy is a tiny reverse proxy used only by the screenshot
// pipeline. Headless Chrome cannot set custom request headers from the CLI, but
// the VayuPress operator console requires an X-API-Key header. This proxy sits
// in front of a running instance and injects that header on every forwarded
// request, so the capture script can point Chrome at the proxy and reach the
// authenticated /admin pages.
//
// It is intentionally NOT part of the production build — it lives under
// scripts/ as its own main package and is only invoked by CI.
//
// Usage:
//
//	UPSTREAM=http://localhost:8080 API_KEY=secret LISTEN=:8088 \
//	    go run ./scripts/screenshot-proxy
package main

import (
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
)

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func main() {
	upstream := env("UPSTREAM", "http://localhost:8080")
	listen := env("LISTEN", ":8088")
	apiKey := os.Getenv("API_KEY")
	if apiKey == "" {
		log.Fatal("screenshot-proxy: API_KEY is required")
	}

	target, err := url.Parse(upstream)
	if err != nil {
		log.Fatalf("screenshot-proxy: bad UPSTREAM %q: %v", upstream, err)
	}

	proxy := httputil.NewSingleHostReverseProxy(target)
	base := proxy.Director
	proxy.Director = func(r *http.Request) {
		base(r)
		r.Host = target.Host
		// Inject the credential Chrome headless cannot send itself. Local,
		// ephemeral, CI-only — the key never leaves the runner.
		r.Header.Set("X-API-Key", apiKey)
	}

	log.Printf("screenshot-proxy: %s -> %s (injecting X-API-Key)", listen, upstream)
	if err := http.ListenAndServe(listen, proxy); err != nil {
		log.Fatalf("screenshot-proxy: %v", err)
	}
}
