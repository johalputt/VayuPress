// Package webhooks delivers outbound HTTP notifications when content changes,
// enabling integrations with automation platforms (Zapier, n8n, Make) and
// custom services without coupling VayuPress to any of them.
//
// Each registered endpoint subscribes to a set of event types. When a matching
// event fires, VayuPress POSTs a JSON payload signed with HMAC-SHA256 over the
// raw body (header X-VayuPress-Signature: sha256=<hex>), so receivers can verify
// authenticity using the per-hook shared secret. Delivery is attempted with a
// short bounded retry/backoff; every attempt is recorded for operator audit.
package webhooks

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Hook is a registered outbound endpoint.
type Hook struct {
	ID      string   `json:"id"`
	URL     string   `json:"url"`
	Secret  string   `json:"-"` // never serialised back to clients
	Events  []string `json:"events"`
	Active  bool     `json:"active"`
	Created string   `json:"created_at"`
}

// Store manages webhook registrations and delivery records.
type Store struct {
	db     *sql.DB
	client *http.Client
}

// New creates a Store. client should be the app's SSRF-safe outbound client.
func New(db *sql.DB, client *http.Client) *Store {
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	return &Store{db: db, client: client}
}

// Create registers a new webhook for the given event types. A random secret is
// generated when one is not supplied.
func (s *Store) Create(ctx context.Context, rawURL, secret string, events []string) (*Hook, error) {
	rawURL = strings.TrimSpace(rawURL)
	if !strings.HasPrefix(rawURL, "http://") && !strings.HasPrefix(rawURL, "https://") {
		return nil, fmt.Errorf("url must be http(s)")
	}
	if len(events) == 0 {
		return nil, fmt.Errorf("at least one event type is required")
	}
	if secret == "" {
		secret = randHex(24)
	}
	id := randHex(12)
	ev := strings.Join(events, ",")
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO webhooks(id,url,secret,events,active) VALUES(?,?,?,?,1)`, id, rawURL, secret, ev)
	if err != nil {
		return nil, fmt.Errorf("create webhook: %w", err)
	}
	return &Hook{ID: id, URL: rawURL, Secret: secret, Events: events, Active: true}, nil
}

// List returns all registered webhooks.
func (s *Store) List(ctx context.Context) ([]Hook, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id,url,events,active,created_at FROM webhooks ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Hook
	for rows.Next() {
		var h Hook
		var ev string
		var active int
		if err := rows.Scan(&h.ID, &h.URL, &ev, &active, &h.Created); err != nil {
			return nil, err
		}
		if ev != "" {
			h.Events = strings.Split(ev, ",")
		}
		h.Active = active == 1
		out = append(out, h)
	}
	return out, rows.Err()
}

// Delete removes a webhook by id.
func (s *Store) Delete(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM webhooks WHERE id=?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("webhook not found")
	}
	return nil
}

// Dispatch delivers event with payload to every active hook subscribed to it.
// It runs synchronously over the matching hooks but is intended to be called
// from a goroutine by the caller. Each hook gets a bounded retry.
func (s *Store) Dispatch(ctx context.Context, event string, payload interface{}) {
	hooks, err := s.subscribers(ctx, event)
	if err != nil || len(hooks) == 0 {
		return
	}
	body, err := json.Marshal(map[string]interface{}{
		"event":      event,
		"created_at": time.Now().UTC().Format(time.RFC3339),
		"data":       payload,
	})
	if err != nil {
		return
	}
	for _, h := range hooks {
		s.deliver(ctx, h, event, body)
	}
}

// deliver POSTs body to one hook with up to 3 attempts and exponential backoff,
// recording the outcome.
func (s *Store) deliver(ctx context.Context, h Hook, event string, body []byte) {
	sig := sign(h.Secret, body)
	var lastErr string
	var status int
	attempts := 0
	for attempts < 3 {
		attempts++
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, h.URL, bytes.NewReader(body))
		if err != nil {
			lastErr = err.Error()
			break
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("User-Agent", "VayuPress-Webhook/1")
		req.Header.Set("X-VayuPress-Event", event)
		req.Header.Set("X-VayuPress-Signature", "sha256="+sig)
		resp, err := s.client.Do(req)
		if err != nil {
			lastErr = err.Error()
		} else {
			status = resp.StatusCode
			resp.Body.Close()
			if status >= 200 && status < 300 {
				lastErr = ""
				break
			}
			lastErr = fmt.Sprintf("status %d", status)
		}
		if attempts < 3 {
			time.Sleep(time.Duration(attempts) * 500 * time.Millisecond)
		}
	}
	s.record(ctx, h.ID, event, status, attempts, lastErr)
}

// subscribers returns active hooks subscribed to event.
func (s *Store) subscribers(ctx context.Context, event string) ([]Hook, error) {
	all, err := s.List(ctx)
	if err != nil {
		return nil, err
	}
	var out []Hook
	for _, h := range all {
		if !h.Active {
			continue
		}
		for _, e := range h.Events {
			if e == event || e == "*" {
				// secret is needed for signing — reload it.
				if sec, err := s.secretOf(ctx, h.ID); err == nil {
					h.Secret = sec
				}
				out = append(out, h)
				break
			}
		}
	}
	return out, nil
}

func (s *Store) secretOf(ctx context.Context, id string) (string, error) {
	var sec string
	err := s.db.QueryRowContext(ctx, `SELECT secret FROM webhooks WHERE id=?`, id).Scan(&sec)
	return sec, err
}

func (s *Store) record(ctx context.Context, hookID, event string, status, attempts int, lastErr string) {
	_, _ = s.db.ExecContext(ctx,
		`INSERT INTO webhook_deliveries(id,webhook_id,event,status,attempts,last_error) VALUES(?,?,?,?,?,?)`,
		randHex(12), hookID, event, status, attempts, lastErr)
}

// Deliveries returns recent delivery records for a hook (newest first).
func (s *Store) Deliveries(ctx context.Context, hookID string, limit int) ([]map[string]interface{}, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT event,status,attempts,last_error,created_at FROM webhook_deliveries WHERE webhook_id=? ORDER BY created_at DESC LIMIT ?`,
		hookID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []map[string]interface{}
	for rows.Next() {
		var event, lastErr, created string
		var status, attempts int
		if err := rows.Scan(&event, &status, &attempts, &lastErr, &created); err != nil {
			return nil, err
		}
		out = append(out, map[string]interface{}{
			"event": event, "status": status, "attempts": attempts,
			"last_error": lastErr, "created_at": created,
		})
	}
	return out, rows.Err()
}

// sign returns the hex HMAC-SHA256 of body under secret.
func sign(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

func randHex(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return hex.EncodeToString(b)
}
