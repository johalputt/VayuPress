// Package update implements VayuPress's secure, check-only + signature-verified
// self-update system. The HTTP layer is read-only (check for updates); the
// binary-apply path is CLI-only, mode-gated, opt-in, and requires Ed25519
// signature verification against a pinned public key (see apply.go).
package update

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Release describes a single GitHub release.
type Release struct {
	Version   string // tag_name, e.g. "v1.1.0"
	Notes     string // body (changelog)
	URL       string // html_url
	Assets    []Asset
	Published time.Time
}

// Asset is a downloadable artifact attached to a release.
type Asset struct {
	Name        string
	DownloadURL string
	Size        int64
}

// ghRelease mirrors the subset of the GitHub releases API we consume.
type ghRelease struct {
	TagName     string    `json:"tag_name"`
	Body        string    `json:"body"`
	HTMLURL     string    `json:"html_url"`
	PublishedAt time.Time `json:"published_at"`
	Assets      []struct {
		Name               string `json:"name"`
		BrowserDownloadURL string `json:"browser_download_url"`
		Size               int64  `json:"size"`
	} `json:"assets"`
}

// CheckLatest queries the GitHub releases API for owner/repo and returns the
// latest release. It uses the provided *http.Client (which should carry a
// timeout). There are NO global/network calls at package init.
func CheckLatest(ctx context.Context, client *http.Client, owner, repo string) (*Release, error) {
	if client == nil {
		return nil, fmt.Errorf("update: nil http client")
	}
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/releases/latest", owner, repo)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("update: build request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "vayupress-updater")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("update: request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("update: github returned status %d", resp.StatusCode)
	}

	var gr ghRelease
	if err := json.NewDecoder(resp.Body).Decode(&gr); err != nil {
		return nil, fmt.Errorf("update: decode: %w", err)
	}

	rel := &Release{
		Version:   gr.TagName,
		Notes:     gr.Body,
		URL:       gr.HTMLURL,
		Published: gr.PublishedAt,
	}
	for _, a := range gr.Assets {
		rel.Assets = append(rel.Assets, Asset{
			Name:        a.Name,
			DownloadURL: a.BrowserDownloadURL,
			Size:        a.Size,
		})
	}
	return rel, nil
}

// CompareVersions returns -1, 0, or +1 comparing semver-ish versions. It
// tolerates a leading "v" and pre-release/build suffixes (which are stripped).
func CompareVersions(a, b string) int {
	pa := parseVersion(a)
	pb := parseVersion(b)
	for i := 0; i < 3; i++ {
		if pa[i] < pb[i] {
			return -1
		}
		if pa[i] > pb[i] {
			return 1
		}
	}
	return 0
}

// UpdateAvailable reports whether latest is strictly newer than current.
func UpdateAvailable(current, latest string) bool {
	return CompareVersions(current, latest) < 0
}

// parseVersion extracts [major, minor, patch] from a version string, tolerating
// a leading "v" and ignoring any pre-release/build metadata.
func parseVersion(v string) [3]int {
	v = strings.TrimSpace(v)
	v = strings.TrimPrefix(v, "v")
	v = strings.TrimPrefix(v, "V")
	// Drop pre-release / build metadata.
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i]
	}
	var out [3]int
	parts := strings.Split(v, ".")
	for i := 0; i < 3 && i < len(parts); i++ {
		n, err := strconv.Atoi(strings.TrimSpace(parts[i]))
		if err != nil {
			n = 0
		}
		out[i] = n
	}
	return out
}
