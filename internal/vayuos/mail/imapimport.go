// SPDX-License-Identifier: Apache-2.0

package mail

import (
	"context"
	"crypto/tls"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/johalputt/vayupress/internal/logging"
	"github.com/johalputt/vayupress/internal/pacedio"
	"github.com/johalputt/vayupress/internal/safefetch"
)

// Bring mail in: copy a mailbox from another provider over IMAP, folder by
// folder, into this one. It runs in the background at the host's pace, and
// what it has copied is recorded per folder by UID, so stopping, a restart
// or running it again later carries on from there, and a second run brings
// only what is new.
//
// The password is held in memory for the run and never stored: a run the
// server stopped waits, saying so, for the password to be given again.

// Import states.
const (
	ImportRunning     = "running"
	ImportDone        = "done"
	ImportStopped     = "stopped"
	ImportFailed      = "failed"
	ImportInterrupted = "interrupted"
)

// ImportRequest is where to bring mail in from.
type ImportRequest struct {
	Host               string
	Port               int
	Username, Password string
}

// ImportStatus is a mailbox's latest import, as the page shows it.
type ImportStatus struct {
	Host, Username, State string
	// Folder is the one being copied while running.
	Folder         string
	Copied, Failed int
	Error          string
	Updated        time.Time
}

// importRun is a running import's handle.
type importRun struct {
	cancel context.CancelFunc
	done   chan struct{}
}

// importer holds what is running, and the dialing, which a test points at
// its own server.
type importer struct {
	mu   sync.Mutex
	runs map[string]*importRun
	dial func(ctx context.Context, network, addr string) (net.Conn, error)
	tls  *tls.Config
}

func (e *Engine) imports() *importer {
	e.importOnce.Do(func() {
		e.importer = &importer{
			runs: map[string]*importRun{},
			// The install's guarded dialer: public addresses only, pinned at
			// dial time, and never a clearnet dial from a Tor install.
			dial: safefetch.SafeTransport(safefetch.TransportOptions{DialTimeout: 15 * time.Second}).DialContext,
		}
	})
	return e.importer
}

func (e *Engine) ensureImportTables() error {
	if e.db == nil {
		return errors.New("vayumail: no storage")
	}
	_, err := e.db.Exec(`CREATE TABLE IF NOT EXISTS vayumail_imports(
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		mailbox TEXT NOT NULL,
		host TEXT NOT NULL,
		username TEXT NOT NULL,
		state TEXT NOT NULL,
		folder TEXT NOT NULL DEFAULT '',
		copied INTEGER NOT NULL DEFAULT 0,
		failed INTEGER NOT NULL DEFAULT 0,
		error TEXT NOT NULL DEFAULT '',
		updated_unix INTEGER NOT NULL,
		UNIQUE(mailbox, host, username));
	CREATE TABLE IF NOT EXISTS vayumail_import_progress(
		import_id INTEGER NOT NULL,
		folder TEXT NOT NULL,
		uidvalidity INTEGER NOT NULL,
		last_uid INTEGER NOT NULL,
		PRIMARY KEY(import_id, folder));`)
	return err
}

// ErrImportRunning is a second import for a mailbox already importing.
var ErrImportRunning = errors.New("mail is already being brought into this mailbox")

// StartImport begins copying mail from req into rd's mailbox, in the
// background. pace is asked before each batch of messages.
func (e *Engine) StartImport(rd Reader, req ImportRequest, pace pacedio.Pacer) error {
	if err := e.writeAuthorised(rd); err != nil {
		return err
	}
	if e.maildir == nil {
		return errors.New("vayumail: not started")
	}
	if err := e.ensureImportTables(); err != nil {
		return err
	}
	req.Host = strings.ToLower(strings.TrimSpace(req.Host))
	req.Username = strings.TrimSpace(req.Username)
	if req.Host == "" || strings.ContainsAny(req.Host, " /:@") || req.Username == "" || req.Password == "" {
		return errors.New("a server, a user name and a password are all needed")
	}
	if err := importPortAllowed(req.Port); err != nil {
		return err
	}
	dom, local := e.mailboxKey(rd.Key())
	mailbox := local + "@" + dom
	im := e.imports()
	im.mu.Lock()
	defer im.mu.Unlock()
	if _, busy := im.runs[mailbox]; busy {
		return ErrImportRunning
	}
	// One row per source: running it again resumes from what it recorded.
	if _, err := e.db.Exec(`INSERT INTO vayumail_imports(mailbox,host,username,state,updated_unix) VALUES(?,?,?,?,?)
		ON CONFLICT(mailbox,host,username) DO UPDATE SET state=excluded.state, error='', folder='', updated_unix=excluded.updated_unix`,
		mailbox, req.Host, req.Username, ImportRunning, time.Now().Unix()); err != nil {
		return err
	}
	var id int64
	if err := e.db.QueryRow(`SELECT id FROM vayumail_imports WHERE mailbox=? AND host=? AND username=?`, mailbox, req.Host, req.Username).Scan(&id); err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(context.Background())
	run := &importRun{cancel: cancel, done: make(chan struct{})}
	im.runs[mailbox] = run
	go func() {
		defer close(run.done)
		err := e.runImport(ctx, id, dom, local, req, pace)
		state, msg := ImportDone, ""
		switch {
		case errors.Is(err, context.Canceled):
			state = ImportStopped
		case err != nil:
			state, msg = ImportFailed, err.Error()
		}
		e.importState(id, state, msg)
		im.mu.Lock()
		delete(im.runs, mailbox)
		im.mu.Unlock()
	}()
	return nil
}

// StopImport stops rd's mailbox's running import; what it copied stays.
func (e *Engine) StopImport(rd Reader) error {
	if err := e.writeAuthorised(rd); err != nil {
		return err
	}
	dom, local := e.mailboxKey(rd.Key())
	im := e.imports()
	im.mu.Lock()
	run := im.runs[local+"@"+dom]
	im.mu.Unlock()
	if run == nil {
		return nil
	}
	run.cancel()
	<-run.done
	return nil
}

// ImportStatusFor is rd's mailbox's most recent import, or nil for none.
func (e *Engine) ImportStatusFor(rd Reader) (*ImportStatus, error) {
	if err := e.readAuthorised(rd); err != nil {
		return nil, err
	}
	if err := e.ensureImportTables(); err != nil {
		return nil, err
	}
	dom, local := e.mailboxKey(rd.Key())
	var s ImportStatus
	var updated int64
	err := e.db.QueryRow(`SELECT host,username,state,folder,copied,failed,error,updated_unix FROM vayumail_imports WHERE mailbox=? ORDER BY updated_unix DESC, id DESC LIMIT 1`,
		local+"@"+dom).Scan(&s.Host, &s.Username, &s.State, &s.Folder, &s.Copied, &s.Failed, &s.Error, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	s.Updated = time.Unix(updated, 0)
	return &s, err
}

func (e *Engine) importState(id int64, state, msg string) {
	if _, err := e.db.Exec(`UPDATE vayumail_imports SET state=?, error=?, folder='', updated_unix=? WHERE id=?`, state, msg, time.Now().Unix(), id); err != nil {
		logging.LogError("vayumail", "recording an import's outcome", err.Error())
	}
}

// failInterruptedImports marks imports a stopped server left running. They
// resume from what they recorded once the password is given again.
func (e *Engine) failInterruptedImports() {
	if e.ensureImportTables() != nil {
		return
	}
	if _, err := e.db.Exec(`UPDATE vayumail_imports SET state=?, error=?, folder='' WHERE state=?`,
		ImportInterrupted, "The server restarted while this was running. Give the password again to carry on from where it stopped.", ImportRunning); err != nil {
		logging.LogError("vayumail", "recovering imports after a restart", err.Error())
	}
}

// runImport copies every folder it maps, oldest message first.
func (e *Engine) runImport(ctx context.Context, id int64, dom, local string, req ImportRequest, pace pacedio.Pacer) error {
	im := e.imports()
	c, err := dialIMAP(ctx, im.dial, im.tls, req.Host, req.Port, req.Username, req.Password)
	if err != nil {
		return err
	}
	defer c.close()
	folders, err := c.list()
	if err != nil {
		return fmt.Errorf("listing the folders: %w", err)
	}
	for _, rf := range folders {
		target := importTarget(rf)
		if target == "" {
			continue
		}
		if !isStandardFolder(target) {
			if err := e.maildir.CreateFolder(dom, local, target); err != nil && !errors.Is(err, ErrFolderExists) {
				return fmt.Errorf("making the folder %s: %w", target, err)
			}
		}
		if err := e.importFolder(ctx, c, id, dom, local, rf.name, target, pace); err != nil {
			return err
		}
	}
	return nil
}

// importFolder copies one remote folder's messages after the last recorded
// UID. A changed UIDVALIDITY means the server renumbered the folder, so it
// is copied again from the start.
func (e *Engine) importFolder(ctx context.Context, c *imapClient, id int64, dom, local, remote, target string, pace pacedio.Pacer) error {
	validity, err := c.examine(remote)
	if err != nil {
		return fmt.Errorf("opening %s: %w", decodeMUTF7(remote), err)
	}
	var lastValidity, last uint64
	_ = e.db.QueryRow(`SELECT uidvalidity,last_uid FROM vayumail_import_progress WHERE import_id=? AND folder=?`, id, remote).Scan(&lastValidity, &last)
	if lastValidity != validity {
		last = 0
	}
	uids, err := c.uidsAfter(last)
	if err != nil {
		return fmt.Errorf("listing %s: %w", decodeMUTF7(remote), err)
	}
	if _, err := e.db.Exec(`UPDATE vayumail_imports SET folder=?, updated_unix=? WHERE id=?`, target, time.Now().Unix(), id); err != nil {
		return err
	}
	for len(uids) > 0 {
		n, err := pace.Next(ctx)
		if err != nil {
			return err
		}
		n = min(max(n, 1), 50, len(uids))
		if e.MailboxOverQuota(local + "@" + dom) {
			return errors.New("this mailbox is full; raise its quota or make room, then bring mail in again to carry on")
		}
		got, err := c.fetch(uids[:n])
		if err != nil {
			return fmt.Errorf("reading %s: %w", decodeMUTF7(remote), err)
		}
		copied, failed := 0, 0
		for _, m := range got {
			if e.copyImported(dom, local, target, m) {
				copied++
			} else {
				failed++
			}
		}
		// The batch is recorded as done only once written, so a stop in the
		// middle copies the batch again rather than losing it.
		last = uids[n-1]
		uids = uids[n:]
		if _, err := e.db.Exec(`INSERT INTO vayumail_import_progress(import_id,folder,uidvalidity,last_uid) VALUES(?,?,?,?)
			ON CONFLICT(import_id,folder) DO UPDATE SET uidvalidity=excluded.uidvalidity, last_uid=excluded.last_uid`, id, remote, validity, last); err != nil {
			return err
		}
		if _, err := e.db.Exec(`UPDATE vayumail_imports SET copied=copied+?, failed=failed+?, updated_unix=? WHERE id=?`, copied, failed, time.Now().Unix(), id); err != nil {
			return err
		}
	}
	return nil
}

// copyImported files one fetched message, with its read and pinned marks.
func (e *Engine) copyImported(dom, local, folder string, m fetched) bool {
	if int64(len(m.body)) > e.cfg.MaxMessageBytes {
		return false
	}
	mid, err := e.maildir.DeliverTo(dom, local, folder, m.body)
	if err != nil {
		return false
	}
	if m.seen {
		if mid, err = e.maildir.setFlagFolder(dom, local, folder, mid, 'S', true); err != nil {
			return true // filed, unread
		}
	}
	if m.flagged {
		_, _ = e.maildir.setFlagFolder(dom, local, folder, mid, 'F', true)
	}
	return true
}

// importTarget names the folder here a remote folder is copied into, or ""
// for one not copied: a folder that cannot be opened, and the views a
// provider builds from other folders (Gmail's All Mail and Starred), whose
// messages are copied from where they actually are.
func importTarget(rf remoteFolder) string {
	a := rf.attrs
	switch {
	case strings.Contains(a, `\noselect`), strings.Contains(a, `\nonexistent`),
		strings.Contains(a, `\all`), strings.Contains(a, `\flagged`), strings.Contains(a, `\important`):
		return ""
	case strings.EqualFold(rf.name, "INBOX"):
		return "Inbox"
	case strings.Contains(a, `\sent`):
		return "Sent"
	case strings.Contains(a, `\drafts`):
		return "Drafts"
	case strings.Contains(a, `\junk`):
		return "Junk"
	case strings.Contains(a, `\trash`):
		return "Trash"
	case strings.Contains(a, `\archive`):
		return "Archive"
	}
	name := decodeMUTF7(rf.name)
	switch strings.ToLower(name) {
	case "sent", "sent items", "sent messages", "sent mail":
		return "Sent"
	case "drafts":
		return "Drafts"
	case "junk", "spam", "junk e-mail", "junk email", "bulk mail":
		return "Junk"
	case "trash", "deleted items", "deleted messages", "bin":
		return "Trash"
	case "archive", "archives":
		return "Archive"
	}
	return ownFolderName(name, rf.delim)
}

// ownFolderName turns a remote folder's name into one of your own: its
// levels joined with " - ", anything a folder name cannot hold made a '-',
// cut to the length allowed. A name that would be a standard folder's or
// Scheduled is kept apart with " imported".
func ownFolderName(name, delim string) string {
	if delim != "" {
		name = strings.Join(strings.Split(name, delim), " - ")
	}
	name = strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == ' ' || r == '-' || r == '_' {
			return r
		}
		return '-'
	}, name)
	name = strings.Join(strings.Fields(name), " ")
	if utf8.RuneCountInString(name) > maxFolderName-len(" imported") {
		name = strings.TrimSpace(string([]rune(name)[:maxFolderName-len(" imported")]))
	}
	if name == "" {
		name = "Imported"
	}
	if !ValidFolderName(name) {
		name += " imported"
	}
	return name
}
