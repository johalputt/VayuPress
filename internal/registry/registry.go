// Package registry provides a sovereign plugin registry for VayuPress.
// Plugins are registered with name, version, and SHA-256 hash; the registry
// enforces integrity before installation.
package registry

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"sync"
)

// PluginMeta describes a registered plugin.
type PluginMeta struct {
	Name        string `json:"name"`
	Version     string `json:"version"`
	Description string `json:"description"`
	Author      string `json:"author"`
	SHA256      string `json:"sha256"`       // hex-encoded
	DownloadURL string `json:"download_url"` // must be HTTPS
	License     string `json:"license"`
}

// Registry stores plugin metadata and validates integrity on install.
type Registry struct {
	mu      sync.RWMutex
	plugins map[string]*PluginMeta // key: name@version
}

// New creates an empty Registry.
func New() *Registry {
	return &Registry{plugins: make(map[string]*PluginMeta)}
}

// Register adds a plugin to the registry.
func (r *Registry) Register(m *PluginMeta) error {
	if m.Name == "" || m.Version == "" || m.SHA256 == "" {
		return errors.New("registry: Name, Version, SHA256 are required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.plugins[m.Name+"@"+m.Version] = m
	return nil
}

// List returns all registered plugins.
func (r *Registry) List() []*PluginMeta {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*PluginMeta, 0, len(r.plugins))
	for _, m := range r.plugins {
		out = append(out, m)
	}
	return out
}

// Get returns the metadata for name@version.
func (r *Registry) Get(name, version string) (*PluginMeta, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	m, ok := r.plugins[name+"@"+version]
	return m, ok
}

// Install downloads a plugin to destPath and verifies its SHA-256 hash.
func (r *Registry) Install(name, version, destPath string) error {
	meta, ok := r.Get(name, version)
	if !ok {
		return fmt.Errorf("registry: plugin %s@%s not found", name, version)
	}
	// nolint:gosec — URL from registry, validated by operator
	resp, err := http.Get(meta.DownloadURL) //nolint:noctx
	if err != nil {
		return fmt.Errorf("registry: download %s: %w", meta.DownloadURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("registry: download %s: HTTP %d", meta.DownloadURL, resp.StatusCode)
	}

	f, err := os.CreateTemp("", "vp-plugin-*")
	if err != nil {
		return fmt.Errorf("registry: temp file: %w", err)
	}
	defer os.Remove(f.Name()) //nolint:errcheck

	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(f, h), resp.Body); err != nil {
		f.Close()
		return fmt.Errorf("registry: write: %w", err)
	}
	f.Close()

	got := hex.EncodeToString(h.Sum(nil))
	if got != meta.SHA256 {
		return fmt.Errorf("registry: hash mismatch for %s@%s: got %s want %s", name, version, got, meta.SHA256)
	}

	if err := os.Rename(f.Name(), destPath); err != nil {
		return fmt.Errorf("registry: install to %s: %w", destPath, err)
	}
	if err := os.Chmod(destPath, 0o755); err != nil {
		return fmt.Errorf("registry: chmod: %w", err)
	}
	return nil
}

// HTTPHandler returns an http.Handler exposing a JSON plugin list endpoint.
func (r *Registry) HTTPHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/plugins", func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(r.List())
	})
	return mux
}
