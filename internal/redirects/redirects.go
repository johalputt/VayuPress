// SPDX-License-Identifier: Apache-2.0

// Package redirects provides HTTP redirect management for VayuPress.
// Rules are stored in SQLite and evaluated as middleware before each request.
package redirects

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Rule maps a from-path to a to-path with an HTTP status code.
type Rule struct {
	ID        int64     `json:"id"`
	FromPath  string    `json:"from_path"`
	ToPath    string    `json:"to_path"`
	Code      int       `json:"code"`
	CreatedAt time.Time `json:"created_at"`
}

// allowedRedirectCodes is the closed set of redirect statuses a rule may use.
// Anything else (200, 404, 5xx…) would turn the middleware into a way to
// answer normal routes with nonsense.
var allowedRedirectCodes = map[int]bool{301: true, 302: true, 303: true, 307: true, 308: true}

// validateRedirectRule enforces the invariants Create relies on (audit: rules
// were stored with zero validation, so a settings-write key could plant a
// site-wide "/" hijack, an open redirect through an absolute target URL, or a
// scheme-bearing from-path that could never match a request anyway):
//
//   - both sides are same-site PATHS: they must start with "/" and never with
//     "//" (protocol-relative), contain a scheme/host, or smuggle control or
//     whitespace characters that different HTTP stacks parse differently;
//   - from_path is never "/" itself — the site root cannot be redirected;
//   - the status code is one of the real redirect codes (301 default).
func validateRedirectRule(from, to string, code int) error {
	if code == 0 {
		code = 301
	}
	if !allowedRedirectCodes[code] {
		return fmt.Errorf("redirects: unsupported status code %d", code)
	}
	if len(from) == 0 || len(from) > 2048 || len(to) > 2048 {
		return fmt.Errorf("redirects: path missing or too long")
	}
	for _, p := range []string{from, to} {
		if !strings.HasPrefix(p, "/") || strings.HasPrefix(p, "//") {
			return fmt.Errorf("redirects: %q is not a same-site path", p)
		}
		if strings.ContainsAny(p, " \t\r\n\"'\\") ||
			strings.Contains(p, "://") || strings.Contains(p, "@") {
			return fmt.Errorf("redirects: %q contains characters not allowed in a site path", p)
		}
	}
	if from == "/" {
		return fmt.Errorf("redirects: refusing to redirect the site root")
	}
	return nil
}

// Manager holds cached redirect rules and serves the redirect middleware.
type Manager struct {
	db    *sql.DB
	mu    sync.RWMutex
	rules map[string]Rule // from_path → Rule
}

// New creates a Manager and loads all rules from DB.
func New(db *sql.DB) (*Manager, error) {
	m := &Manager{db: db, rules: make(map[string]Rule)}
	if err := m.reload(context.Background()); err != nil {
		return nil, err
	}
	return m, nil
}

// reload refreshes the in-memory cache from the database.
func (m *Manager) reload(ctx context.Context) error {
	rows, err := m.db.QueryContext(ctx, `SELECT id,from_path,to_path,code,created_at FROM redirects`)
	if err != nil {
		return fmt.Errorf("redirects reload: %w", err)
	}
	defer rows.Close()
	fresh := make(map[string]Rule)
	for rows.Next() {
		var r Rule
		var createdRaw string
		if err := rows.Scan(&r.ID, &r.FromPath, &r.ToPath, &r.Code, &createdRaw); err != nil {
			return err
		}
		r.CreatedAt, _ = time.Parse("2006-01-02 15:04:05", createdRaw)
		fresh[r.FromPath] = r
	}
	if err := rows.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	m.rules = fresh
	m.mu.Unlock()
	return nil
}

// Middleware intercepts requests that match a redirect rule.
func (m *Manager) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m.mu.RLock()
		rule, ok := m.rules[r.URL.Path]
		m.mu.RUnlock()
		if ok {
			http.Redirect(w, r, rule.ToPath, rule.Code)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// Create adds a new redirect rule.
func (m *Manager) Create(ctx context.Context, from, to string, code int) (*Rule, error) {
	if err := validateRedirectRule(from, to, code); err != nil {
		return nil, err
	}
	res, err := m.db.ExecContext(ctx,
		`INSERT INTO redirects(from_path,to_path,code) VALUES(?,?,?)`, from, to, code)
	if err != nil {
		return nil, fmt.Errorf("redirects create: %w", err)
	}
	id, _ := res.LastInsertId()
	r := &Rule{ID: id, FromPath: from, ToPath: to, Code: code, CreatedAt: time.Now()}
	m.mu.Lock()
	m.rules[from] = *r
	m.mu.Unlock()
	return r, nil
}

// Delete removes a redirect rule by ID.
func (m *Manager) Delete(ctx context.Context, id int64) error {
	// find from_path first so we can evict cache
	var from string
	err := m.db.QueryRowContext(ctx, `SELECT from_path FROM redirects WHERE id=?`, id).Scan(&from)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return err
	}
	if _, err := m.db.ExecContext(ctx, `DELETE FROM redirects WHERE id=?`, id); err != nil {
		return fmt.Errorf("redirects delete: %w", err)
	}
	m.mu.Lock()
	delete(m.rules, from)
	m.mu.Unlock()
	return nil
}

// List returns all redirect rules.
func (m *Manager) List(ctx context.Context) ([]Rule, error) {
	m.mu.RLock()
	out := make([]Rule, 0, len(m.rules))
	for _, r := range m.rules {
		out = append(out, r)
	}
	m.mu.RUnlock()
	return out, nil
}
