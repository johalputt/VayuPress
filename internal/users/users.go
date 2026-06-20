// Package users provides multi-author account management for VayuPress.
//
// Accounts authenticate with email + password. Passwords are never stored in
// the clear — they are hashed with Argon2id via the shared auth helpers (the
// same KDF used for API-secret hashing). Roles gate capability: "admin" users
// may manage other users and system settings; "author" users may write content.
//
// This package owns only persistence and credential verification; HTTP session
// issuance lives in internal/auth so that the auth layer has no dependency on
// the users schema (avoiding an import cycle).
package users

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"net/mail"
	"strings"
	"time"

	"github.com/johalputt/vayupress/internal/auth"
)

// Roles recognised by the authorization layer.
const (
	RoleAdmin  = "admin"
	RoleAuthor = "author"
)

// User is an account record. The password hash is never serialised to JSON.
type User struct {
	ID        string     `json:"id"`
	Email     string     `json:"email"`
	Name      string     `json:"name"`
	Role      string     `json:"role"`
	CreatedAt time.Time  `json:"created_at"`
	LastLogin *time.Time `json:"last_login,omitempty"`
}

// Store manages user accounts in SQLite.
type Store struct{ db *sql.DB }

// New creates a Store.
func New(db *sql.DB) *Store { return &Store{db: db} }

// Create inserts a new user with the given credentials. Role defaults to
// "author" when empty and must be a recognised role otherwise.
func (s *Store) Create(ctx context.Context, email, name, password, role string) (*User, error) {
	email = strings.TrimSpace(strings.ToLower(email))
	if _, err := mail.ParseAddress(email); err != nil {
		return nil, fmt.Errorf("invalid email: %w", err)
	}
	if len(password) < 8 {
		return nil, fmt.Errorf("password must be at least 8 characters")
	}
	if role == "" {
		role = RoleAuthor
	}
	if role != RoleAdmin && role != RoleAuthor {
		return nil, fmt.Errorf("invalid role %q (want admin or author)", role)
	}
	hash, err := auth.HashSecretArgon2id(password)
	if err != nil {
		return nil, fmt.Errorf("hash password: %w", err)
	}
	id := newID()
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO users(id,email,name,password_hash,role) VALUES(?,?,?,?,?)`,
		id, email, strings.TrimSpace(name), hash, role)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return nil, fmt.Errorf("a user with that email already exists")
		}
		return nil, fmt.Errorf("create user: %w", err)
	}
	return &User{ID: id, Email: email, Name: strings.TrimSpace(name), Role: role, CreatedAt: time.Now().UTC()}, nil
}

// Authenticate verifies email + password and returns the user on success. The
// Argon2id verification runs even when the email is unknown (against a decoy)
// to keep the response time independent of account existence.
func (s *Store) Authenticate(ctx context.Context, email, password string) (*User, error) {
	email = strings.TrimSpace(strings.ToLower(email))
	var u User
	var hash string
	var lastLogin sql.NullTime
	err := s.db.QueryRowContext(ctx,
		`SELECT id,email,name,password_hash,role,created_at,last_login FROM users WHERE email=?`, email).
		Scan(&u.ID, &u.Email, &u.Name, &hash, &u.Role, &u.CreatedAt, &lastLogin)
	if err == sql.ErrNoRows {
		// Constant-ish work to blunt user-enumeration timing.
		auth.VerifySecretArgon2id(password, decoyHash)
		return nil, fmt.Errorf("invalid email or password")
	}
	if err != nil {
		return nil, err
	}
	if !auth.VerifySecretArgon2id(password, hash) {
		return nil, fmt.Errorf("invalid email or password")
	}
	if lastLogin.Valid {
		u.LastLogin = &lastLogin.Time
	}
	return &u, nil
}

// GetByID returns the user with the given id.
func (s *Store) GetByID(ctx context.Context, id string) (*User, error) {
	var u User
	var lastLogin sql.NullTime
	err := s.db.QueryRowContext(ctx,
		`SELECT id,email,name,role,created_at,last_login FROM users WHERE id=?`, id).
		Scan(&u.ID, &u.Email, &u.Name, &u.Role, &u.CreatedAt, &lastLogin)
	if err != nil {
		return nil, err
	}
	if lastLogin.Valid {
		u.LastLogin = &lastLogin.Time
	}
	return &u, nil
}

// List returns all users ordered by creation time.
func (s *Store) List(ctx context.Context) ([]User, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id,email,name,role,created_at,last_login FROM users ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []User
	for rows.Next() {
		var u User
		var lastLogin sql.NullTime
		if err := rows.Scan(&u.ID, &u.Email, &u.Name, &u.Role, &u.CreatedAt, &lastLogin); err != nil {
			return nil, err
		}
		if lastLogin.Valid {
			u.LastLogin = &lastLogin.Time
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// SetPassword updates a user's password (after the same strength check as Create).
func (s *Store) SetPassword(ctx context.Context, email, password string) error {
	if len(password) < 8 {
		return fmt.Errorf("password must be at least 8 characters")
	}
	hash, err := auth.HashSecretArgon2id(password)
	if err != nil {
		return err
	}
	res, err := s.db.ExecContext(ctx,
		`UPDATE users SET password_hash=? WHERE email=?`, hash, strings.TrimSpace(strings.ToLower(email)))
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("no user with that email")
	}
	return nil
}

// TouchLastLogin records a successful login time.
func (s *Store) TouchLastLogin(ctx context.Context, id string) {
	_, _ = s.db.ExecContext(ctx,
		`UPDATE users SET last_login=? WHERE id=?`, time.Now().UTC().Format("2006-01-02 15:04:05"), id)
}

// Count returns the number of accounts (used to detect first-run bootstrap).
func (s *Store) Count(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM users`).Scan(&n)
	return n, err
}

// Delete removes a user by email.
func (s *Store) Delete(ctx context.Context, email string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM users WHERE email=?`, strings.TrimSpace(strings.ToLower(email)))
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("no user with that email")
	}
	return nil
}

// decoyHash is a valid Argon2id-encoded hash of a random value, used to spend
// comparable CPU time on unknown-email logins so timing does not reveal which
// emails are registered.
var decoyHash = func() string {
	h, _ := auth.HashSecretArgon2id("vayupress-decoy-password")
	return h
}()

func newID() string {
	b := make([]byte, 12)
	rand.Read(b)
	return hex.EncodeToString(b)
}
