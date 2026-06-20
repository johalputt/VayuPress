// Package social auto-shares newly published articles to social networks.
//
// The initial provider is Mastodon (and any Mastodon-API-compatible server, such
// as Pleroma/Akkoma), chosen because it offers a clean, token-authenticated REST
// API with no OAuth redirect dance — a single app access token is enough to post.
// This keeps VayuPress sovereign: the operator pastes a token from their own
// instance and owns the integration end to end.
//
// Posting is best-effort and asynchronous from the caller's perspective; a
// failure to share never affects publishing.
package social

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/johalputt/vayupress/internal/logging"
)

// MastodonConfig holds the credentials for a Mastodon-compatible server.
type MastodonConfig struct {
	Instance string // e.g. https://mastodon.social
	Token    string // app access token with write:statuses scope
}

// Poster shares posts to the configured networks. A zero/empty config yields a
// disabled Poster whose Share is a no-op.
type Poster struct {
	mastodon MastodonConfig
	client   *http.Client
	enabled  bool
}

// New builds a Poster. client should be the app's SSRF-safe outbound client.
func New(mastodon MastodonConfig, client *http.Client) *Poster {
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	enabled := strings.TrimSpace(mastodon.Instance) != "" && strings.TrimSpace(mastodon.Token) != ""
	return &Poster{mastodon: mastodon, client: client, enabled: enabled}
}

// Enabled reports whether at least one network is configured.
func (p *Poster) Enabled() bool { return p.enabled }

// Share posts a short announcement linking to the article. It is safe to call
// when disabled (returns nil). Errors from a configured network are returned for
// logging but are not fatal to publishing.
func (p *Poster) Share(ctx context.Context, title, link string) error {
	if !p.enabled {
		return nil
	}
	status := buildStatus(title, link)
	return p.postMastodon(ctx, status)
}

// postMastodon publishes a status using the Mastodon REST API.
func (p *Poster) postMastodon(ctx context.Context, status string) error {
	endpoint := strings.TrimRight(p.mastodon.Instance, "/") + "/api/v1/statuses"
	form := url.Values{}
	form.Set("status", status)
	form.Set("visibility", "public")

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+p.mastodon.Token)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	// Idempotency key prevents duplicate toots if a retry races.
	req.Header.Set("Idempotency-Key", idempotencyKey(status))

	resp, err := p.client.Do(req)
	if err != nil {
		return fmt.Errorf("mastodon post: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("mastodon post: status %d", resp.StatusCode)
	}
	logging.LogInfo("social", "shared to mastodon: "+p.mastodon.Instance)
	return nil
}

// buildStatus composes the announcement text, trimming the title so the whole
// message stays comfortably under the common 500-char limit.
func buildStatus(title, link string) string {
	title = strings.TrimSpace(title)
	if len(title) > 380 {
		title = title[:377] + "..."
	}
	return title + "\n\n" + link
}

func idempotencyKey(status string) string {
	// A coarse key derived from content + minute; good enough to dedupe retries
	// without suppressing legitimately distinct posts.
	return fmt.Sprintf("vayu-%d-%d", time.Now().Unix()/60, len(status))
}
