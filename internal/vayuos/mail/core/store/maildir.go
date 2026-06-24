// Package store provides Maildir-format message storage.
//
// Based on Mox message store (MIT license). Messages are stored in the
// standard Maildir format under VayuPress storage paths. Each mailbox is
// a directory with cur/, new/, and tmp/ subdirectories.
package store

import (
	"fmt"
	"os"
	"path/filepath"
)

// MaildirStore manages Maildir-format mailboxes.
type MaildirStore struct {
	basePath string
}

func New(basePath string) *MaildirStore {
	return &MaildirStore{basePath: basePath}
}

func (s *MaildirStore) InitMailbox(domain, username string) error {
	path := s.MaildirPath(domain, username)
	for _, sub := range []string{"cur", "new", "tmp"} {
		if err := os.MkdirAll(filepath.Join(path, sub), 0700); err != nil {
			return fmt.Errorf("create %s: %w", sub, err)
		}
	}
	return nil
}

func (s *MaildirStore) MaildirPath(domain, username string) string {
	return filepath.Join(s.basePath, "domains", domain, username)
}

// StoreMessage writes a raw message to the Maildir.
func (s *MaildirStore) StoreMessage(domain, username string, data []byte) (string, error) {
	path := s.MaildirPath(domain, username)
	// Maildir filename format: <time>.<pid>.<hostname>
	filename := filepath.Join(path, "new", fmt.Sprintf("%d.vayupress", os.Getpid()))
	if err := os.WriteFile(filename, data, 0600); err != nil {
		return "", fmt.Errorf("write message: %w", err)
	}
	return filename, nil
}