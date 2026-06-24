package mail

import (
	"bytes"
	"errors"
	"fmt"
	"net/mail"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
	"time"
)

// StandardFolders are the mailbox folders surfaced in the panel, in order.
var StandardFolders = []string{"Inbox", "Sent", "Drafts", "Junk", "Trash"}

// canonicalFolder returns the canonical folder name, defaulting to Inbox.
func canonicalFolder(name string) string {
	for _, f := range StandardFolders {
		if strings.EqualFold(f, name) {
			return f
		}
	}
	return "Inbox"
}

// folderDir returns the Maildir directory for a folder. Inbox is the account
// root; other folders are Maildir++ subfolders (.Sent, .Junk, …).
func (m *Maildir) folderDir(domain, username, folder string) string {
	base := m.accountDir(domain, username)
	if folder == "" || strings.EqualFold(folder, "Inbox") {
		return base
	}
	return filepath.Join(base, "."+canonicalFolder(folder))
}

// ensureFolder creates the tmp/new/cur dirs for a folder.
func (m *Maildir) ensureFolder(domain, username, folder string) error {
	for _, sub := range []string{"tmp", "new", "cur"} {
		if err := os.MkdirAll(filepath.Join(m.folderDir(domain, username, folder), sub), 0o700); err != nil {
			return err
		}
	}
	return nil
}

// CreateAll provisions the inbox plus all standard folders for an account.
func (m *Maildir) CreateAll(domain, username string) error {
	for _, f := range StandardFolders {
		if err := m.ensureFolder(domain, username, f); err != nil {
			return err
		}
	}
	return nil
}

// DeliverTo writes a message into a specific folder and returns its id.
func (m *Maildir) DeliverTo(domain, username, folder string, raw []byte) (string, error) {
	if err := m.ensureFolder(domain, username, folder); err != nil {
		return "", err
	}
	n := atomic.AddUint64(&m.counter, 1)
	host, _ := os.Hostname()
	if host == "" {
		host = "vayupress"
	}
	name := fmt.Sprintf("%d.%d_%d.%s", time.Now().Unix(), os.Getpid(), n, host)
	dir := m.folderDir(domain, username, folder)
	tmpPath := filepath.Join(dir, "tmp", name)
	if err := os.WriteFile(tmpPath, raw, 0o600); err != nil {
		return "", err
	}
	newPath := filepath.Join(dir, "new", name)
	if err := os.Rename(tmpPath, newPath); err != nil {
		return "", err
	}
	return "new/" + name, nil
}

// ListFolder returns the messages in a folder, newest first.
func (m *Maildir) ListFolder(domain, username, folder string) ([]StoredMessage, error) {
	var out []StoredMessage
	dir := m.folderDir(domain, username, folder)
	for _, sub := range []string{"new", "cur"} {
		entries, err := os.ReadDir(filepath.Join(dir, sub))
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			info, err := e.Info()
			if err != nil {
				continue
			}
			sm := StoredMessage{ID: sub + "/" + e.Name(), Size: info.Size(), Seen: sub == "cur", Date: info.ModTime()}
			if raw, err := os.ReadFile(filepath.Join(dir, sub, e.Name())); err == nil {
				if msg, perr := mail.ReadMessage(bytes.NewReader(raw)); perr == nil {
					sm.From = msg.Header.Get("From")
					sm.To = msg.Header.Get("To")
					sm.Subject = msg.Header.Get("Subject")
					if d, derr := msg.Header.Date(); derr == nil {
						sm.Date = d
					}
				}
			}
			out = append(out, sm)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Date.After(out[j].Date) })
	return out, nil
}

// ReadRawFolder returns the raw bytes of a message in a folder.
func (m *Maildir) ReadRawFolder(domain, username, folder, id string) ([]byte, error) {
	sub, name, ok := strings.Cut(id, "/")
	if !ok || (sub != "new" && sub != "cur") {
		return nil, errors.New("vayumail: invalid message id")
	}
	if name == "" || strings.ContainsAny(name, "/\\") || strings.Contains(name, "..") {
		return nil, errors.New("vayumail: invalid message id")
	}
	return os.ReadFile(filepath.Join(m.folderDir(domain, username, folder), sub, name))
}

// MoveBetween moves a message from one folder to another (e.g. Inbox→Junk).
func (m *Maildir) MoveBetween(domain, username, id, from, to string) error {
	raw, err := m.ReadRawFolder(domain, username, from, id)
	if err != nil {
		return err
	}
	if _, err := m.DeliverTo(domain, username, to, raw); err != nil {
		return err
	}
	return m.deleteMessage(domain, username, from, id)
}

// deleteMessage removes a message file from a folder.
func (m *Maildir) deleteMessage(domain, username, folder, id string) error {
	sub, name, ok := strings.Cut(id, "/")
	if !ok || (sub != "new" && sub != "cur") {
		return errors.New("vayumail: invalid message id")
	}
	if name == "" || strings.ContainsAny(name, "/\\") || strings.Contains(name, "..") {
		return errors.New("vayumail: invalid message id")
	}
	return os.Remove(filepath.Join(m.folderDir(domain, username, folder), sub, name))
}
