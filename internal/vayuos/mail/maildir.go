package mail

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"
)

// Maildir is a minimal, standards-compliant Maildir store rooted at a base dir.
// Each account lives at <base>/<domain>/<username>/{tmp,new,cur}.
type Maildir struct {
	base    string
	counter uint64
}

// NewMaildir returns a Maildir rooted at base.
func NewMaildir(base string) *Maildir { return &Maildir{base: base} }

// safeSegment reduces an untrusted value (domain or username) to a single safe
// path segment. filepath.Base(filepath.Clean(...)) strips any directory
// separators and ".." components, so a hostile domain/username can never escape
// the Maildir base directory (defends against path traversal).
func safeSegment(s string) string {
	s = filepath.Base(filepath.Clean("/" + strings.TrimSpace(s)))
	if s == "." || s == string(filepath.Separator) || s == "" {
		return "_"
	}
	return s
}

func (m *Maildir) accountDir(domain, username string) string {
	return filepath.Join(m.base, safeSegment(domain), safeSegment(username))
}

// Create provisions the tmp/new/cur directories for an account.
func (m *Maildir) Create(domain, username string) error {
	for _, sub := range []string{"tmp", "new", "cur"} {
		if err := os.MkdirAll(filepath.Join(m.accountDir(domain, username), sub), 0o700); err != nil {
			return err
		}
	}
	return nil
}

// Deliver writes a message to an account, using the tmp→new atomic move that
// the Maildir specification requires.
func (m *Maildir) Deliver(domain, username string, raw []byte) (string, error) {
	if err := m.Create(domain, username); err != nil {
		return "", err
	}
	n := atomic.AddUint64(&m.counter, 1)
	host, _ := os.Hostname()
	if host == "" {
		host = "vayupress"
	}
	name := fmt.Sprintf("%d.%d_%d.%s", time.Now().Unix(), os.Getpid(), n, host)
	tmpPath := filepath.Join(m.accountDir(domain, username), "tmp", name)
	if err := os.WriteFile(tmpPath, raw, 0o600); err != nil {
		return "", err
	}
	newPath := filepath.Join(m.accountDir(domain, username), "new", name)
	if err := os.Rename(tmpPath, newPath); err != nil {
		return "", err
	}
	return name, nil
}

// Stats counts messages and bytes in an account's new+cur folders.
func (m *Maildir) Stats(domain, username string) (MailboxStats, error) {
	var st MailboxStats
	for _, sub := range []string{"new", "cur"} {
		dir := filepath.Join(m.accountDir(domain, username), sub)
		entries, err := os.ReadDir(dir)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return st, err
		}
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			st.Messages++
			if info, err := e.Info(); err == nil {
				st.Bytes += info.Size()
			}
		}
	}
	return st, nil
}
