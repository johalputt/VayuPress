package mail

import (
	"database/sql"
	"errors"
	"strings"
	"sync"
	"time"
)

// uidstore.go — persistent IMAP UID / UIDVALIDITY assignment.
//
// RFC 3501 requires every message in a mailbox to have a Unique Identifier (UID)
// that is strictly ascending and stable for the life of the mailbox, plus a
// UIDVALIDITY value that only changes if those UIDs are ever reassigned. Real
// mail clients (Gmail app, Apple Mail, Thunderbird, Outlook) rely on this to
// sync incrementally — without stable UIDs a client re-downloads everything, or
// shows duplicates, on every reconnect.
//
// VayuMail stores messages as Maildir files whose names change when flags are
// set (a new→cur move appends ":2,S" etc.). We therefore key UIDs on the
// IMMUTABLE part of the filename (the segment before ":2,", see baseName), so a
// message keeps its UID across flag changes and folder reads. The mapping is
// persisted in SQLite so UIDs survive restarts; UIDVALIDITY is the row's
// creation time and never changes once set.

// UIDStore assigns and remembers IMAP UIDs per (account, folder).
type UIDStore struct {
	db *sql.DB
	mu sync.Mutex // serialises UID allocation (one writer; keeps uidnext consistent)
}

// NewUIDStore opens the store, creating its tables if needed. It mirrors the
// AccountStore pattern (lazy CREATE TABLE) so no global migration is required —
// these tables exist only when VayuMail is enabled.
func NewUIDStore(db *sql.DB) (*UIDStore, error) {
	s := &UIDStore{db: db}
	if db == nil {
		return s, nil
	}
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS vayumail_uidvalidity(
		account TEXT NOT NULL,
		folder TEXT NOT NULL,
		uidvalidity INTEGER NOT NULL,
		uidnext INTEGER NOT NULL DEFAULT 1,
		PRIMARY KEY(account, folder));`); err != nil {
		return s, err
	}
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS vayumail_uids(
		account TEXT NOT NULL,
		folder TEXT NOT NULL,
		basename TEXT NOT NULL,
		uid INTEGER NOT NULL,
		PRIMARY KEY(account, folder, basename));`); err != nil {
		return s, err
	}
	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_vayumail_uids_uid ON vayumail_uids(account, folder, uid);`); err != nil {
		return s, err
	}
	return s, nil
}

// baseName returns the immutable Maildir identity of a message file: the unique
// name without the ":2,<flags>" info suffix. This is what stays constant when a
// message moves new→cur or its flags change, so it is the correct UID key.
func baseName(name string) string {
	b, _ := splitMaildirFlags(name)
	return b
}

// idBaseName extracts the stable base name from a List() id ("new/<name>" or
// "cur/<name>").
func idBaseName(id string) string {
	if _, name, ok := strings.Cut(id, "/"); ok {
		return baseName(name)
	}
	return baseName(id)
}

// Validity returns the UIDVALIDITY for a mailbox, creating the row (with a
// stable creation-time value) on first use.
func (s *UIDStore) Validity(account, folder string) (uint32, error) {
	if s == nil || s.db == nil {
		return 1, nil
	}
	account, folder = normEmail(account), canonicalFolder(folder)
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ensureMailbox(account, folder); err != nil {
		return 1, err
	}
	var v uint32
	err := s.db.QueryRow(`SELECT uidvalidity FROM vayumail_uidvalidity WHERE account=? AND folder=?`, account, folder).Scan(&v)
	if err != nil {
		return 1, err
	}
	return v, nil
}

// ensureMailbox creates the uidvalidity row if absent. Caller holds s.mu.
func (s *UIDStore) ensureMailbox(account, folder string) error {
	_, err := s.db.Exec(
		`INSERT OR IGNORE INTO vayumail_uidvalidity(account, folder, uidvalidity, uidnext) VALUES(?,?,?,1)`,
		account, folder, uint32(time.Now().Unix()))
	return err
}

// UIDNext returns the predicted next UID for a mailbox.
func (s *UIDStore) UIDNext(account, folder string) (uint32, error) {
	if s == nil || s.db == nil {
		return 1, nil
	}
	account, folder = normEmail(account), canonicalFolder(folder)
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ensureMailbox(account, folder); err != nil {
		return 1, err
	}
	var n uint32
	err := s.db.QueryRow(`SELECT uidnext FROM vayumail_uidvalidity WHERE account=? AND folder=?`, account, folder).Scan(&n)
	if err != nil {
		return 1, err
	}
	return n, nil
}

// Assign returns the UID for a message (identified by its stable base name),
// allocating the next UID atomically on first sight. The same base name always
// returns the same UID, so a message keeps its UID across flag/state changes.
func (s *UIDStore) Assign(account, folder, msgBase string) (uint32, error) {
	if s == nil || s.db == nil {
		return 0, errors.New("vayumail: no uid store")
	}
	account, folder = normEmail(account), canonicalFolder(folder)
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.ensureMailbox(account, folder); err != nil {
		return 0, err
	}
	// Fast path: already assigned.
	var uid uint32
	err := s.db.QueryRow(`SELECT uid FROM vayumail_uids WHERE account=? AND folder=? AND basename=?`,
		account, folder, msgBase).Scan(&uid)
	if err == nil {
		return uid, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}
	// Allocate the next UID in a single transaction so uidnext stays consistent.
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	var next uint32
	if err := tx.QueryRow(`SELECT uidnext FROM vayumail_uidvalidity WHERE account=? AND folder=?`, account, folder).Scan(&next); err != nil {
		return 0, err
	}
	if next == 0 {
		next = 1
	}
	if _, err := tx.Exec(`INSERT INTO vayumail_uids(account, folder, basename, uid) VALUES(?,?,?,?)`,
		account, folder, msgBase, next); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(`UPDATE vayumail_uidvalidity SET uidnext=? WHERE account=? AND folder=?`,
		next+1, account, folder); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return next, nil
}
