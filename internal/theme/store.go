package theme

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

// Save persists t to the theme_tokens table (upsert on id=1).
func Save(ctx context.Context, db *sql.DB, t Tokens) error {
	data, err := json.Marshal(t)
	if err != nil {
		return err
	}
	_, err = db.ExecContext(ctx, `
		INSERT INTO theme_tokens (id, name, tokens, updated_at)
		VALUES (1, ?, ?, strftime('%Y-%m-%dT%H:%M:%SZ','now'))
		ON CONFLICT(id) DO UPDATE SET
			name       = excluded.name,
			tokens     = excluded.tokens,
			updated_at = excluded.updated_at
	`, t.Name, string(data))
	return err
}

// Load retrieves the active tokens from the database.
// If no row exists it returns Default() so callers always get a usable value.
func Load(ctx context.Context, db *sql.DB) (Tokens, error) {
	var raw string
	err := db.QueryRowContext(ctx, `SELECT tokens FROM theme_tokens WHERE id = 1`).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return Default(), nil
	}
	if err != nil {
		return Default(), err
	}
	var t Tokens
	if err := json.Unmarshal([]byte(raw), &t); err != nil {
		return Default(), err
	}
	return t, nil
}
