// SPDX-License-Identifier: Apache-2.0

package mail

import (
	"bufio"
	"fmt"
	netmail "net/mail"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Maildir is a minimal, standards-compliant Maildir store rooted at a base dir.
// Each account lives at <base>/<domain>/<username>/{tmp,new,cur}.
type Maildir struct {
	base    string
	counter uint64

	// hdrFolders remembers each message file's parsed headers, validated by the
	// file's (size, mtime), grouped by the directory the file lives in. Listing a
	// folder used to re-read and re-parse every message on every poll, folder
	// switch and row action, so the console got slower exactly as the mailbox got
	// more useful. The file identity is the entry's integrity check: a rewritten
	// message has a new mtime. See ADR-0162 for why this is a cache and not an
	// index, and for the numbers.
	//
	// Grouped by directory so the bound can evict what nobody is looking at. A
	// single bound over every message on the install, reset whole when full, made
	// a warm listing of a 60,000-message folder cost exactly as much as a cold one.
	hdrMu      sync.Mutex
	hdrFolders map[string]*folderHeaders
	hdrClock   uint64 // advances on every lookup; the LRU order of folders
	hdrTotal   int    // entries across every folder
	hdrLimit   int    // 0 means maxHeaderEntries; set by tests on their own Maildir
}

// folderHeaders is one directory's cached message summaries.
type folderHeaders struct {
	byPath map[string]cachedHeaders
	used   uint64
}

// maxHeaderEntries bounds the cache across the install (a summary is a few
// hundred bytes, so this is tens of megabytes at most). Past it, whole folders
// are dropped least-recently-listed first — never the one being listed.
const maxHeaderEntries = 250000

// cachedHeaders is a message's parsed summary plus the file identity it came
// from. hasDate is separate from date so an absent/unparseable Date header falls
// back to the file mtime rather than to the zero time.
type cachedHeaders struct {
	size      int64
	modTime   time.Time
	from      string
	to        string
	subject   string
	date      time.Time
	hasDate   bool
	messageID string   // Message-Id, de-bracketed (threading)
	inReplyTo string   // In-Reply-To, de-bracketed
	refs      []string // References, de-bracketed, in order
}

// cleanMessageID normalises a Message-Id / References token for comparison:
// surrounding angle brackets removed, whitespace trimmed, lower-cased. Message
// ids are case-sensitive per RFC 5322 in theory and case-insensitive in practice
// across real mail servers, so grouping on the case-folded form matches what users
// expect (one conversation) and never splits one over a case difference.
func cleanMessageID(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "<")
	s = strings.TrimSuffix(s, ">")
	return strings.ToLower(strings.TrimSpace(s))
}

// splitMessageIDs parses a References header (a whitespace-separated list of
// angle-bracketed ids) into clean ids.
func splitMessageIDs(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	var out []string
	for _, f := range strings.Fields(s) {
		if id := cleanMessageID(f); id != "" {
			out = append(out, id)
		}
	}
	return out
}

// headersFor returns a message file's parsed headers, reading and caching them
// only when the file is new or has changed since last time.
func (m *Maildir) headersFor(path string, size int64, mod time.Time) cachedHeaders {
	dir := filepath.Dir(path)
	m.hdrMu.Lock()
	m.hdrClock++
	if fh := m.hdrFolders[dir]; fh != nil {
		fh.used = m.hdrClock
		if h, ok := fh.byPath[path]; ok && h.size == size && h.modTime.Equal(mod) {
			m.hdrMu.Unlock()
			return h
		}
	}
	m.hdrMu.Unlock()

	h, ok := readHeaders(path, size, mod)
	if !ok {
		// Not remembered: a read that failed once (a descriptor limit, a file
		// mid-move) would otherwise list this message with no sender or subject
		// until the file changed, which is never for a delivered message.
		return h
	}
	m.hdrMu.Lock()
	defer m.hdrMu.Unlock()
	if m.hdrFolders == nil {
		m.hdrFolders = make(map[string]*folderHeaders)
	}
	fh := m.hdrFolders[dir]
	if fh == nil {
		fh = &folderHeaders{byPath: make(map[string]cachedHeaders)}
		m.hdrFolders[dir] = fh
	}
	fh.used = m.hdrClock
	if _, had := fh.byPath[path]; !had {
		m.hdrTotal++
	}
	fh.byPath[path] = h
	m.evictFoldersLocked(dir)
	return h
}

// evictFoldersLocked drops least-recently-listed folders until the cache is
// within its bound, never the folder being listed. Caller holds hdrMu.
func (m *Maildir) evictFoldersLocked(keep string) {
	limit := m.hdrLimit
	if limit <= 0 {
		limit = maxHeaderEntries
	}
	for m.hdrTotal > limit {
		oldest, oldestUsed := "", uint64(0)
		for dir, fh := range m.hdrFolders {
			if dir != keep && (oldest == "" || fh.used < oldestUsed) {
				oldest, oldestUsed = dir, fh.used
			}
		}
		if oldest == "" {
			return // only the folder being listed is left; it may exceed the bound
		}
		m.hdrTotal -= len(m.hdrFolders[oldest].byPath)
		delete(m.hdrFolders, oldest)
	}
}

// forgetMissing drops cached entries for files no longer in dir. A flag change
// renames a message file and a delete removes it; without this a long-lived
// folder's cache would keep every name it ever had.
func (m *Maildir) forgetMissing(dir string, present map[string]struct{}) {
	m.hdrMu.Lock()
	defer m.hdrMu.Unlock()
	fh := m.hdrFolders[dir]
	if fh == nil {
		return
	}
	for path := range fh.byPath {
		if _, ok := present[path]; !ok {
			delete(fh.byPath, path)
			m.hdrTotal--
		}
	}
}

// readHeaders parses a message file's header block. Only the header block is
// read: net/mail stops at the blank line, so a message carrying a 20 MB
// attachment costs a few kilobytes to list rather than 20 MB.
func readHeaders(path string, size int64, mod time.Time) (cachedHeaders, bool) {
	h := cachedHeaders{size: size, modTime: mod}
	f, err := os.Open(path)
	if err != nil {
		return h, false
	}
	defer f.Close()
	if msg, perr := netmail.ReadMessage(bufio.NewReader(f)); perr == nil {
		h.from = msg.Header.Get("From")
		h.to = msg.Header.Get("To")
		h.subject = msg.Header.Get("Subject")
		if d, derr := msg.Header.Date(); derr == nil {
			h.date, h.hasDate = d, true
		}
		// Threading evidence travels with the cached summary, so grouping a
		// folder costs nothing beyond the stat it already did.
		h.messageID = cleanMessageID(msg.Header.Get("Message-Id"))
		h.inReplyTo = cleanMessageID(msg.Header.Get("In-Reply-To"))
		h.refs = splitMessageIDs(msg.Header.Get("References"))
	}
	return h, true
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
	// AUDIT FINDING (Section 2). Every identity lookup in this system folds case
	// -- normEmail, RoleFor, QuotaFor, HashFor, ResolveAlias -- and this path did
	// not, so the two disagreed about who a mailbox belongs to.
	//
	// Delivery was the serious half: RCPT TO:<ALICE@example.com> resolved through
	// the case-insensitive lookups, earned a 250 (so the sending server recorded
	// it delivered and would never retry or bounce), and then landed in
	// <base>/example.com/ALICE/ while its owner read .../alice/. Mail accepted and
	// then unreachable forever, with nothing reporting a failure -- and it fired
	// by accident, whenever a correspondent's address book held the display-cased
	// form. Login was the other half: POP3 and IMAP derive the directory from the
	// string the person typed, so the same holder reached a different mailbox
	// depending on how they capitalised their own address.
	//
	// Folded HERE rather than at each caller because this is the single function
	// every Maildir path component passes through -- delivery, POP3 login, IMAP
	// login and the retention sweep all arrive at it. Normalising at four call
	// sites would leave the fifth to be forgotten.
	return strings.ToLower(s)
}

func (m *Maildir) accountDir(domain, username string) string {
	return filepath.Join(m.base, safeSegment(domain), safeSegment(username))
}

// retiredBase is where a deleted account's mail is set aside.
//
// It is a SIBLING of the Maildir base, never a directory inside it. Every path
// component under the base passes through safeSegment, which reduces a domain to
// one lowercased segment — so a dot-directory inside the base could be named by a
// hostile or merely unlucky domain and delivered into. Nothing routed by
// accountDir can reach a sibling.
func (m *Maildir) retiredBase() string { return m.base + "-retired" }

// Retire moves an account's mail out of the delivery tree and returns where it
// went, or "" when the account had no directory at all.
//
// It MOVES rather than deletes on purpose. The hole being closed is inheritance
// — a reissued address handing its new holder the previous holder's mail — and a
// rename closes that completely. Erasing the messages would close it too, and
// would also destroy the only copy of a mailbox deleted by mistake or held for
// retention. stamp distinguishes successive retirements of the same address; the
// caller supplies it so this stays a pure filesystem move.
func (m *Maildir) Retire(domain, username, stamp string) (string, error) {
	src := m.accountDir(domain, username)
	if _, err := os.Stat(src); err != nil {
		if os.IsNotExist(err) {
			return "", nil // never received mail; nothing to set aside
		}
		return "", err
	}
	dstDir := filepath.Join(m.retiredBase(), safeSegment(domain))
	if err := os.MkdirAll(dstDir, 0o700); err != nil {
		return "", err
	}
	dst := filepath.Join(dstDir, safeSegment(username)+"."+safeSegment(stamp))
	// A same-second second retirement of the same address must not land on the
	// first and destroy it. Rename onto an existing directory is refused rather
	// than merged, so suffix until the name is free.
	for i := 1; ; i++ {
		if _, err := os.Stat(dst); os.IsNotExist(err) {
			break
		}
		dst = filepath.Join(dstDir, fmt.Sprintf("%s.%s.%d", safeSegment(username), safeSegment(stamp), i))
	}
	if err := os.Rename(src, dst); err != nil {
		return "", err
	}
	return dst, nil
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

// AccountSize returns the total bytes used by an account across ALL folders
// (Inbox, Sent, Drafts, Archive, Junk, Trash) — used for quota accounting.
func (m *Maildir) AccountSize(domain, username string) int64 {
	var total int64
	for _, folder := range StandardFolders {
		dir := m.folderDir(domain, username, folder)
		for _, sub := range []string{"new", "cur"} {
			entries, err := os.ReadDir(filepath.Join(dir, sub))
			if err != nil {
				continue
			}
			for _, e := range entries {
				if e.IsDir() {
					continue
				}
				if info, err := e.Info(); err == nil {
					total += info.Size()
				}
			}
		}
	}
	return total
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
