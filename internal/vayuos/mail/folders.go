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
var StandardFolders = []string{"Inbox", "Sent", "Drafts", "Archive", "Junk", "Trash"}

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
			_, fl := splitMaildirFlags(e.Name())
			sm := StoredMessage{
				ID: sub + "/" + e.Name(), Size: info.Size(), Date: info.ModTime(),
				Seen:    strings.ContainsRune(fl, 'S'),
				Flagged: strings.ContainsRune(fl, 'F'),
			}
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

// ReadRawFolder returns the raw bytes of a message in a folder, tolerating a
// stale id (sub/flags changed since it was issued) via resolveMessage.
func (m *Maildir) ReadRawFolder(domain, username, folder, id string) ([]byte, error) {
	sub, name, err := m.resolveMessage(domain, username, folder, id)
	if err != nil {
		return nil, err
	}
	return os.ReadFile(filepath.Join(m.folderDir(domain, username, folder), sub, name))
}

// SearchResult is a message matched by Search, tagged with its folder.
type SearchResult struct {
	StoredMessage
	Folder string `json:"folder"`
}

// Search scans an account's folders for messages whose From/To/Subject (and, as
// a fallback, body) contain q (case-insensitive). It is bounded by maxScan
// files so it stays cheap on a low-resource VPS — no external index, fully
// local. Header matches avoid re-reading the message; only non-header matches
// touch the body.
func (m *Maildir) Search(domain, username, q string, limit int) ([]SearchResult, error) {
	q = strings.ToLower(strings.TrimSpace(q))
	if q == "" {
		return nil, nil
	}
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	const maxScan = 5000
	scanned := 0
	out := []SearchResult{}
	for _, folder := range StandardFolders {
		msgs, err := m.ListFolder(domain, username, folder)
		if err != nil {
			continue
		}
		for _, sm := range msgs {
			if scanned >= maxScan {
				return out, nil
			}
			scanned++
			matched := strings.Contains(strings.ToLower(sm.From+" "+sm.To+" "+sm.Subject), q)
			if !matched {
				if raw, rerr := m.ReadRawFolder(domain, username, folder, sm.ID); rerr == nil {
					matched = strings.Contains(strings.ToLower(string(raw)), q)
				}
			}
			if matched {
				out = append(out, SearchResult{StoredMessage: sm, Folder: folder})
				if len(out) >= limit {
					return out, nil
				}
			}
		}
	}
	return out, nil
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
	sub, name, err := m.resolveMessage(domain, username, folder, id)
	if err != nil {
		return err
	}
	return os.Remove(filepath.Join(m.folderDir(domain, username, folder), sub, name))
}

// splitMaildirFlags separates a Maildir filename into its base name and the
// flag string after the ":2," info marker (empty when none).
func splitMaildirFlags(name string) (base, flags string) {
	if i := strings.Index(name, ":2,"); i >= 0 {
		return name[:i], name[i+3:]
	}
	return name, ""
}

// addFlag returns flags with f present, keeping the set unique and ASCII-sorted
// as the Maildir spec requires.
func addFlag(flags string, f rune) string {
	return sortFlags(strings.Map(dropRune(f), flags) + string(f))
}

// removeFlag returns flags with every occurrence of f removed.
func removeFlag(flags string, f rune) string { return sortFlags(strings.Map(dropRune(f), flags)) }

func dropRune(f rune) func(rune) rune {
	return func(r rune) rune {
		if r == f {
			return -1
		}
		return r
	}
}

func sortFlags(flags string) string {
	rs := []rune(flags)
	sort.Slice(rs, func(i, j int) bool { return rs[i] < rs[j] })
	return string(rs)
}

// resolveMessage finds the on-disk file for a message id within a folder,
// tolerating a stale sub (new/ vs cur/) or a stale flag suffix. A client's id
// goes stale the instant a message is read (new→cur), flagged, or moved, and a
// naive path join would then fail with ENOENT — which surfaced as a 500 on
// "mark as read". This matches by the Maildir base name (the unique part before
// ":2,"), checking the exact name first, then scanning cur/ and new/.
func (m *Maildir) resolveMessage(domain, username, folder, id string) (sub, name string, err error) {
	raw := id
	if _, after, ok := strings.Cut(id, "/"); ok {
		raw = after
	}
	raw = filepath.Base(raw)
	if raw == "" || raw == "." || strings.Contains(raw, "..") {
		return "", "", errors.New("vayumail: invalid message id")
	}
	dir := m.folderDir(domain, username, folder)
	for _, s := range []string{"cur", "new"} {
		if _, e := os.Stat(filepath.Join(dir, s, raw)); e == nil {
			return s, raw, nil
		}
	}
	base, _ := splitMaildirFlags(raw)
	for _, s := range []string{"cur", "new"} {
		entries, e := os.ReadDir(filepath.Join(dir, s))
		if e != nil {
			continue
		}
		for _, ent := range entries {
			if ent.IsDir() {
				continue
			}
			if b, _ := splitMaildirFlags(ent.Name()); b == base {
				return s, ent.Name(), nil
			}
		}
	}
	return "", "", errors.New("vayumail: message not found")
}

// setFlagFolder adds or removes a single Maildir flag (e.g. 'F' for pinned) on a
// message, preserving its other flags and its seen state. Flagged messages live
// in cur/ (where the info part is allowed); clearing the last flag returns the
// file to new/.
func (m *Maildir) setFlagFolder(domain, username, folder, id string, flag rune, on bool) (string, error) {
	sub, name, err := m.resolveMessage(domain, username, folder, id)
	if err != nil {
		return id, err
	}
	base, flags := splitMaildirFlags(name)
	var nf string
	if on {
		nf = addFlag(flags, flag)
	} else {
		nf = removeFlag(flags, flag)
	}
	dir := m.folderDir(domain, username, folder)
	if nf == "" {
		if err := os.MkdirAll(filepath.Join(dir, "new"), 0o700); err != nil {
			return id, err
		}
		if err := os.Rename(filepath.Join(dir, sub, name), filepath.Join(dir, "new", base)); err != nil {
			return id, err
		}
		return "new/" + base, nil
	}
	if err := os.MkdirAll(filepath.Join(dir, "cur"), 0o700); err != nil {
		return id, err
	}
	newName := base + ":2," + nf
	if err := os.Rename(filepath.Join(dir, sub, name), filepath.Join(dir, "cur", newName)); err != nil {
		return id, err
	}
	return "cur/" + newName, nil
}

// markSeenFolder moves a message from new/ to cur/ within a folder and sets the
// Maildir ":2,S" (Seen) flag, returning the new id. Already-seen messages are
// returned unchanged.
func (m *Maildir) markSeenFolder(domain, username, folder, id string) (string, error) {
	sub, name, err := m.resolveMessage(domain, username, folder, id)
	if err != nil {
		return id, err
	}
	base, flags := splitMaildirFlags(name)
	if sub == "cur" && strings.ContainsRune(flags, 'S') {
		return "cur/" + name, nil
	}
	dir := m.folderDir(domain, username, folder)
	if err := os.MkdirAll(filepath.Join(dir, "cur"), 0o700); err != nil {
		return id, err
	}
	newName := base + ":2," + addFlag(flags, 'S') // preserve other flags (e.g. F = pinned)
	if err := os.Rename(filepath.Join(dir, sub, name), filepath.Join(dir, "cur", newName)); err != nil {
		return id, err
	}
	return "cur/" + newName, nil
}

// markUnseenFolder clears the Seen flag, keeping any other flags (so a pinned
// message stays pinned) — back to new/ only when no flags remain.
func (m *Maildir) markUnseenFolder(domain, username, folder, id string) (string, error) {
	sub, name, err := m.resolveMessage(domain, username, folder, id)
	if err != nil {
		return id, err
	}
	base, flags := splitMaildirFlags(name)
	if sub == "new" || !strings.ContainsRune(flags, 'S') {
		// Already unseen; normalise the returned id to the resolved file.
		return sub + "/" + name, nil
	}
	nf := removeFlag(flags, 'S')
	dir := m.folderDir(domain, username, folder)
	if nf == "" {
		if err := os.MkdirAll(filepath.Join(dir, "new"), 0o700); err != nil {
			return id, err
		}
		if err := os.Rename(filepath.Join(dir, sub, name), filepath.Join(dir, "new", base)); err != nil {
			return id, err
		}
		return "new/" + base, nil
	}
	newName := base + ":2," + nf
	if err := os.Rename(filepath.Join(dir, sub, name), filepath.Join(dir, "cur", newName)); err != nil {
		return id, err
	}
	return "cur/" + newName, nil
}
