package mail

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// DecryptHook optionally transforms a stored message before it is served to a
// client (e.g. transparent PGP decryption for the owning account). It returns
// the message to serve; on any failure it should return the original bytes.
type DecryptHook func(accountEmail string, raw []byte) []byte

// IMAPServer is an RFC 3501 IMAP4rev1 server providing standard mail clients
// (Gmail app, Apple Mail, Thunderbird, Outlook) full access to a VayuMail
// account's folders. Authentication is delegated to VayuPress accounts via the
// Bridge. UIDs/UIDVALIDITY are persisted (see UIDStore) so clients sync
// incrementally. It is started only when inbound mail is enabled.
//
// Supported: CAPABILITY, STARTTLS, LOGIN, AUTHENTICATE PLAIN, NAMESPACE,
// LIST/LSUB (all folders, SPECIAL-USE), STATUS, SELECT/EXAMINE, FETCH (+UID)
// with FLAGS/UID/RFC822.SIZE/INTERNALDATE/ENVELOPE/BODY/BODYSTRUCTURE/BODY[...],
// STORE (+UID) flag updates, SEARCH (+UID), COPY (+UID), MOVE (+UID, RFC 6851),
// APPEND (RFC 3502 multiappend not required), EXPUNGE, CLOSE, UNSELECT, IDLE,
// CHECK, NOOP, LOGOUT.
type IMAPServer struct {
	cfg     Config
	bridge  Bridge
	maildir *Maildir
	decrypt DecryptHook
	uids    *UIDStore

	tls         *tls.Config
	implicitTLS bool
	listenAddr  string

	ln     net.Listener
	wg     sync.WaitGroup
	mu     sync.Mutex
	closed bool
}

// NewIMAPServer creates an IMAP server bound to cfg.IMAPListen.
func NewIMAPServer(cfg Config, bridge Bridge, md *Maildir, decrypt DecryptHook) *IMAPServer {
	return &IMAPServer{cfg: cfg, bridge: bridge, maildir: md, decrypt: decrypt, listenAddr: cfg.IMAPListen}
}

// WithTLS enables the STARTTLS command on the plaintext (143) listener.
func (s *IMAPServer) WithTLS(t *tls.Config) *IMAPServer { s.tls = t; return s }

// WithImplicitTLS turns this into an implicit-TLS IMAPS listener bound to addr
// (993): connections are wrapped in TLS immediately, with no STARTTLS step.
func (s *IMAPServer) WithImplicitTLS(t *tls.Config, addr string) *IMAPServer {
	s.tls = t
	s.implicitTLS = true
	s.listenAddr = addr
	return s
}

// WithUIDStore attaches the persistent UID/UIDVALIDITY store. Without it the
// server falls back to ephemeral sequence-number UIDs (used only in unit tests).
func (s *IMAPServer) WithUIDStore(u *UIDStore) *IMAPServer { s.uids = u; return s }

// Addr returns the actual listen address (useful with :0 in tests).
func (s *IMAPServer) Addr() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ln == nil {
		return ""
	}
	return s.ln.Addr().String()
}

// Start begins listening.
func (s *IMAPServer) Start(_ context.Context) error {
	ln, err := net.Listen("tcp", s.listenAddr)
	if err != nil {
		return fmt.Errorf("vayumail: imap listen %s: %w", s.listenAddr, err)
	}
	s.mu.Lock()
	s.ln = ln
	s.mu.Unlock()
	s.wg.Add(1)
	go s.acceptLoop()
	return nil
}

// Stop shuts the listener down.
func (s *IMAPServer) Stop(_ context.Context) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	ln := s.ln
	s.mu.Unlock()
	if ln != nil {
		_ = ln.Close()
	}
	s.wg.Wait()
	return nil
}

func (s *IMAPServer) acceptLoop() {
	defer s.wg.Done()
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			s.mu.Lock()
			closed := s.closed
			s.mu.Unlock()
			if closed {
				return
			}
			continue
		}
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			c := conn
			if s.implicitTLS && s.tls != nil {
				c = tls.Server(conn, s.tls)
			}
			s.handle(c)
		}()
	}
}

type imapSession struct {
	authedUser string // local username
	authedMail string // full email
	selected   string // canonical folder name, "" if none selected
	readOnly   bool
	msgs       []imapMsg
}

func (sess *imapSession) authed() bool { return sess.authedUser != "" }

func (s *IMAPServer) handle(conn net.Conn) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(30 * time.Minute))
	var (
		br *bufio.Reader
		w  *bufio.Writer
	)
	setup := func(c net.Conn) {
		br = bufio.NewReader(c)
		w = bufio.NewWriter(c)
	}
	setup(conn)
	line := func(str string) { _, _ = w.WriteString(str + "\r\n"); _ = w.Flush() }

	onTLS := s.implicitTLS
	line("* OK [CAPABILITY " + s.capabilities(onTLS) + "] VayuMail IMAP ready")

	sess := &imapSession{}
	for {
		raw, err := br.ReadString('\n')
		if err != nil {
			return
		}
		raw = strings.TrimRight(raw, "\r\n")
		tag, rest := cutSpace(raw)
		cmd, arg := cutSpace(rest)
		cmd = strings.ToUpper(cmd)
		switch cmd {
		case "CAPABILITY":
			line("* CAPABILITY " + s.capabilities(onTLS))
			line(tag + " OK CAPABILITY completed")
		case "STARTTLS":
			if s.tls == nil || onTLS {
				line(tag + " NO STARTTLS not available")
				continue
			}
			line(tag + " OK Begin TLS negotiation now")
			tconn := tls.Server(conn, s.tls)
			if herr := tconn.Handshake(); herr != nil {
				return
			}
			conn = tconn
			_ = conn.SetDeadline(time.Now().Add(30 * time.Minute))
			setup(conn)
			onTLS = true
			sess = &imapSession{} // RFC 2595: discard prior session state
		case "NOOP", "CHECK":
			line(tag + " OK " + cmd + " completed")
		case "ID":
			line(`* ID ("name" "VayuMail")`)
			line(tag + " OK ID completed")
		case "NAMESPACE":
			line(`* NAMESPACE (("" "/")) NIL NIL`)
			line(tag + " OK NAMESPACE completed")
		case "LOGIN":
			s.doLogin(line, sess, tag, arg)
		case "AUTHENTICATE":
			s.doAuthenticate(br, w, line, sess, tag, arg)
		case "LIST", "LSUB":
			s.doList(line, sess, tag, cmd, arg)
		case "STATUS":
			s.doStatus(line, sess, tag, arg)
		case "SELECT", "EXAMINE":
			s.doSelect(line, sess, tag, arg, cmd == "EXAMINE")
		case "FETCH":
			s.doFetch(w, line, sess, tag, arg, false)
		case "STORE":
			s.doStore(w, line, sess, tag, arg, false)
		case "SEARCH":
			s.doSearch(line, sess, tag, arg, false)
		case "COPY":
			s.doCopy(line, sess, tag, arg, false)
		case "MOVE":
			s.doMove(w, line, sess, tag, arg, false)
		case "UID":
			s.doUID(w, line, sess, tag, arg)
		case "APPEND":
			s.doAppend(br, w, line, sess, tag, arg)
		case "EXPUNGE":
			s.doExpunge(w, line, sess, tag, "")
		case "CLOSE":
			if sess.selected != "" && !sess.readOnly {
				s.expungeDeleted(sess, nil)
			}
			sess.selected = ""
			sess.msgs = nil
			line(tag + " OK CLOSE completed")
		case "UNSELECT":
			sess.selected = ""
			sess.msgs = nil
			line(tag + " OK UNSELECT completed")
		case "IDLE":
			s.doIdle(br, w, line, sess, tag, &conn)
		case "LOGOUT":
			line("* BYE VayuMail logging out")
			line(tag + " OK LOGOUT completed")
			return
		default:
			line(tag + " BAD Command not supported")
		}
	}
}

func (s *IMAPServer) capabilities(onTLS bool) string {
	c := "IMAP4rev1 LITERAL+ SASL-IR AUTH=PLAIN IDLE NAMESPACE UIDPLUS MOVE SPECIAL-USE CHILDREN UNSELECT"
	if s.tls != nil && !onTLS {
		c += " STARTTLS"
	}
	return c
}

// ── Authentication ───────────────────────────────────────────────────────────

func (s *IMAPServer) doLogin(line func(string), sess *imapSession, tag, arg string) {
	user, pass := parseLoginArgs(arg)
	if user == "" {
		line(tag + " BAD LOGIN requires username and password")
		return
	}
	if !s.verify(user, pass) {
		line(tag + " NO [AUTHENTICATIONFAILED] Invalid credentials")
		return
	}
	s.setAuthed(sess, user)
	line(tag + " OK LOGIN completed")
}

// doAuthenticate implements SASL PLAIN (with optional SASL-IR initial response),
// which the major mobile/desktop clients prefer over the LOGIN command.
func (s *IMAPServer) doAuthenticate(br *bufio.Reader, w *bufio.Writer, line func(string), sess *imapSession, tag, arg string) {
	mech, ir := cutSpace(arg)
	if !strings.EqualFold(mech, "PLAIN") {
		line(tag + " NO Unsupported authentication mechanism")
		return
	}
	payload := ir
	if payload == "" {
		_, _ = w.WriteString("+ \r\n")
		_ = w.Flush()
		l, err := br.ReadString('\n')
		if err != nil {
			return
		}
		payload = strings.TrimSpace(l)
	}
	dec, err := base64.StdEncoding.DecodeString(strings.TrimSpace(payload))
	if err != nil {
		line(tag + " BAD Invalid base64")
		return
	}
	f := strings.Split(string(dec), "\x00") // authzid \0 authcid \0 passwd
	if len(f) != 3 || !s.verify(f[1], f[2]) {
		line(tag + " NO [AUTHENTICATIONFAILED] Invalid credentials")
		return
	}
	s.setAuthed(sess, f[1])
	line(tag + " OK AUTHENTICATE completed")
}

func (s *IMAPServer) verify(user, pass string) bool {
	if s.bridge == nil || user == "" {
		return false
	}
	ok, err := s.bridge.AuthUser(user, pass)
	return err == nil && ok
}

func (s *IMAPServer) setAuthed(sess *imapSession, user string) {
	local, mailAddr := user, user
	if i := strings.IndexByte(user, '@'); i >= 0 {
		local = user[:i]
	} else {
		mailAddr = user + "@" + s.cfg.Domain
	}
	sess.authedUser = local
	sess.authedMail = mailAddr
}

// ── Mailbox listing ──────────────────────────────────────────────────────────

// folderAttrs returns the SPECIAL-USE attribute for a folder (RFC 6154), so
// clients map Sent/Drafts/Junk/Trash/Archive to their standard icons.
func folderAttrs(folder string) string {
	switch folder {
	case "Sent":
		return `\Sent \HasNoChildren`
	case "Drafts":
		return `\Drafts \HasNoChildren`
	case "Junk":
		return `\Junk \HasNoChildren`
	case "Trash":
		return `\Trash \HasNoChildren`
	case "Archive":
		return `\Archive \HasNoChildren`
	default:
		return `\HasNoChildren`
	}
}

func (s *IMAPServer) doList(line func(string), sess *imapSession, tag, cmd, arg string) {
	if !sess.authed() {
		line(tag + " NO Not authenticated")
		return
	}
	args := tokenizeQuoted(arg)
	pattern := ""
	if len(args) >= 2 {
		pattern = args[1]
	}
	// `LIST "" ""` is a request for the hierarchy delimiter only.
	if pattern == "" {
		line(`* ` + cmd + ` (\Noselect) "/" ""`)
		line(tag + " OK " + cmd + " completed")
		return
	}
	for _, f := range StandardFolders {
		name := f
		if strings.EqualFold(f, "Inbox") {
			name = "INBOX"
		}
		if !mailboxMatches(pattern, name) {
			continue
		}
		line("* " + cmd + " (" + folderAttrs(f) + `) "/" "` + name + `"`)
	}
	line(tag + " OK " + cmd + " completed")
}

// mailboxMatches does a permissive IMAP wildcard match (* and % both match any
// run of characters here, since the namespace is flat).
func mailboxMatches(pattern, name string) bool {
	pattern = strings.Trim(pattern, `"`)
	if pattern == "*" || pattern == "%" || pattern == "" {
		return true
	}
	if !strings.ContainsAny(pattern, "*%") {
		return strings.EqualFold(pattern, name)
	}
	// Translate the leading literal prefix (before the first wildcard).
	if i := strings.IndexAny(pattern, "*%"); i >= 0 {
		return strings.HasPrefix(strings.ToUpper(name), strings.ToUpper(pattern[:i]))
	}
	return true
}

func (s *IMAPServer) doStatus(line func(string), sess *imapSession, tag, arg string) {
	if !sess.authed() {
		line(tag + " NO Not authenticated")
		return
	}
	mbox, items := cutSpace(arg)
	folder := canonicalFolder(strings.Trim(mbox, `"`))
	msgs, _ := s.maildir.ListFolder(s.cfg.Domain, sess.authedUser, folder)
	unseen := 0
	for _, m := range msgs {
		if len(imapFlagsForID(m.ID)) == 0 || !strings.Contains(strings.Join(imapFlagsForID(m.ID), " "), `\Seen`) {
			unseen++
		}
	}
	items = strings.ToUpper(items)
	var parts []string
	if strings.Contains(items, "MESSAGES") {
		parts = append(parts, fmt.Sprintf("MESSAGES %d", len(msgs)))
	}
	if strings.Contains(items, "RECENT") {
		parts = append(parts, fmt.Sprintf("RECENT %d", unseen))
	}
	if strings.Contains(items, "UIDNEXT") {
		parts = append(parts, fmt.Sprintf("UIDNEXT %d", s.uidNext(sess.authedMail, folder, len(msgs))))
	}
	if strings.Contains(items, "UIDVALIDITY") {
		parts = append(parts, fmt.Sprintf("UIDVALIDITY %d", s.uidValidity(sess.authedMail, folder)))
	}
	if strings.Contains(items, "UNSEEN") {
		parts = append(parts, fmt.Sprintf("UNSEEN %d", unseen))
	}
	line(fmt.Sprintf(`* STATUS "%s" (%s)`, mbox, strings.Join(parts, " ")))
	line(tag + " OK STATUS completed")
}

// ── Selection ────────────────────────────────────────────────────────────────

func (s *IMAPServer) uidValidity(account, folder string) uint32 {
	if s.uids != nil {
		if v, err := s.uids.Validity(account, folder); err == nil {
			return v
		}
	}
	return 1
}

func (s *IMAPServer) uidNext(account, folder string, snapshotLen int) uint32 {
	if s.uids != nil {
		if n, err := s.uids.UIDNext(account, folder); err == nil {
			return n
		}
	}
	return uint32(snapshotLen + 1)
}

// snapshot loads the selected folder's messages, assigning stable UIDs and
// ordering by UID ascending (so sequence numbers are stable too).
func (s *IMAPServer) snapshot(sess *imapSession, folder string) error {
	list, err := s.maildir.ListFolder(s.cfg.Domain, sess.authedUser, folder)
	if err != nil {
		return err
	}
	// Assign UIDs oldest-first so arrival order maps to ascending UIDs.
	sort.Slice(list, func(i, j int) bool { return list[i].Date.Before(list[j].Date) })
	msgs := make([]imapMsg, 0, len(list))
	for _, sm := range list {
		base := idBaseName(sm.ID)
		var uid uint32
		if s.uids != nil {
			uid, _ = s.uids.Assign(sess.authedMail, folder, base)
		}
		msgs = append(msgs, imapMsg{
			uid: uid, id: sm.ID, base: base, size: sm.Size,
			date: sm.Date, flags: flagSetForID(sm.ID),
		})
	}
	if s.uids != nil {
		sort.Slice(msgs, func(i, j int) bool { return msgs[i].uid < msgs[j].uid })
	}
	for i := range msgs {
		msgs[i].seq = i + 1
		if s.uids == nil {
			msgs[i].uid = uint32(i + 1)
		}
	}
	sess.msgs = msgs
	return nil
}

func (s *IMAPServer) doSelect(line func(string), sess *imapSession, tag, arg string, examine bool) {
	if !sess.authed() {
		line(tag + " NO Not authenticated")
		return
	}
	folder := canonicalFolder(strings.Trim(strings.TrimSpace(arg), `"`))
	if err := s.snapshot(sess, folder); err != nil {
		line(tag + " NO Cannot open mailbox")
		return
	}
	sess.selected = folder
	sess.readOnly = examine
	unseen, firstUnseen := 0, 0
	for i, m := range sess.msgs {
		if !m.flags['S'] {
			unseen++
			if firstUnseen == 0 {
				firstUnseen = i + 1
			}
		}
	}
	line(fmt.Sprintf("* %d EXISTS", len(sess.msgs)))
	line(fmt.Sprintf("* %d RECENT", unseen))
	if firstUnseen > 0 {
		line(fmt.Sprintf("* OK [UNSEEN %d] First unseen", firstUnseen))
	}
	line(`* FLAGS (\Seen \Answered \Flagged \Deleted \Draft)`)
	line(`* OK [PERMANENTFLAGS (\Seen \Answered \Flagged \Deleted \Draft \*)] Limited`)
	line(fmt.Sprintf("* OK [UIDVALIDITY %d] UIDs valid", s.uidValidity(sess.authedMail, folder)))
	line(fmt.Sprintf("* OK [UIDNEXT %d] Predicted next UID", s.uidNext(sess.authedMail, folder, len(sess.msgs))))
	if examine {
		line(tag + " OK [READ-ONLY] EXAMINE completed")
	} else {
		line(tag + " OK [READ-WRITE] SELECT completed")
	}
}

// ── FETCH ────────────────────────────────────────────────────────────────────

func (s *IMAPServer) doFetch(w *bufio.Writer, line func(string), sess *imapSession, tag, arg string, byUID bool) {
	if sess.selected == "" {
		line(tag + " NO No mailbox selected")
		return
	}
	seqPart, items := cutSpace(arg)
	itemList := splitFetchItems(items)
	willSeen := fetchWillSetSeen(itemList) && !sess.readOnly
	if willSeen && !containsFold(itemList, "FLAGS") {
		itemList = append(itemList, "FLAGS")
	}
	targets := s.resolveSet(sess, seqPart, byUID)
	for _, m := range targets {
		if willSeen && !m.flags['S'] {
			m.flags['S'] = true
			if newID, err := s.maildir.setMessageFlags(s.cfg.Domain, sess.authedUser, sess.selected, m.id, m.flags); err == nil {
				m.id = newID
			}
		}
		s.writeFetchResponse(w, sess, m, itemList, byUID)
	}
	line(tag + " OK FETCH completed")
}

// fetchWillSetSeen reports whether any requested item is a non-peek body fetch.
func fetchWillSetSeen(items []string) bool {
	for _, it := range items {
		up := strings.ToUpper(it)
		if up == "RFC822" || up == "RFC822.TEXT" {
			return true
		}
		if strings.HasPrefix(up, "BODY[") {
			return true
		}
	}
	return false
}

func containsFold(items []string, want string) bool {
	for _, it := range items {
		if strings.EqualFold(it, want) {
			return true
		}
	}
	return false
}

// ── STORE ────────────────────────────────────────────────────────────────────

func (s *IMAPServer) doStore(w *bufio.Writer, line func(string), sess *imapSession, tag, arg string, byUID bool) {
	if sess.selected == "" {
		line(tag + " NO No mailbox selected")
		return
	}
	if sess.readOnly {
		line(tag + " NO Mailbox is read-only")
		return
	}
	seqPart, rest := cutSpace(arg)
	op, flagsPart := cutSpace(rest)
	opU := strings.ToUpper(op)
	silent := strings.Contains(opU, ".SILENT")
	mode := byte('=') // replace
	if strings.HasPrefix(opU, "+") {
		mode = '+'
	} else if strings.HasPrefix(opU, "-") {
		mode = '-'
	}
	wantFlags := parseFlagList(flagsPart)
	targets := s.resolveSet(sess, seqPart, byUID)
	for _, m := range targets {
		newSet := map[byte]bool{}
		switch mode {
		case '+':
			for k, v := range m.flags {
				newSet[k] = v
			}
			for k := range wantFlags {
				newSet[k] = true
			}
		case '-':
			for k, v := range m.flags {
				newSet[k] = v
			}
			for k := range wantFlags {
				delete(newSet, k)
			}
		default:
			newSet = wantFlags
		}
		if newID, err := s.maildir.setMessageFlags(s.cfg.Domain, sess.authedUser, sess.selected, m.id, newSet); err == nil {
			m.id = newID
			m.flags = newSet
		}
		if !silent {
			if byUID {
				_, _ = w.WriteString(fmt.Sprintf("* %d FETCH (UID %d FLAGS (%s))\r\n", m.seq, m.uid, strings.Join(imapFlagTokens(m.flags), " ")))
			} else {
				_, _ = w.WriteString(fmt.Sprintf("* %d FETCH (FLAGS (%s))\r\n", m.seq, strings.Join(imapFlagTokens(m.flags), " ")))
			}
			_ = w.Flush()
		}
	}
	line(tag + " OK STORE completed")
}

// parseFlagList parses "(\Seen \Flagged)" into a Maildir letter set.
func parseFlagList(s string) map[byte]bool {
	set := map[byte]bool{}
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "(")
	s = strings.TrimSuffix(s, ")")
	for _, tok := range strings.Fields(s) {
		if c := parseIMAPFlag(tok); c != 0 {
			set[c] = true
		}
	}
	return set
}

// ── COPY / MOVE / EXPUNGE ────────────────────────────────────────────────────

func (s *IMAPServer) doCopy(line func(string), sess *imapSession, tag, arg string, byUID bool) {
	if sess.selected == "" {
		line(tag + " NO No mailbox selected")
		return
	}
	seqPart, mbox := cutSpace(arg)
	dest := canonicalFolder(strings.Trim(strings.TrimSpace(mbox), `"`))
	targets := s.resolveSet(sess, seqPart, byUID)
	srcUIDs, dstUIDs := []string{}, []string{}
	for _, m := range targets {
		raw, err := s.maildir.ReadRawFolder(s.cfg.Domain, sess.authedUser, sess.selected, m.id)
		if err != nil {
			continue
		}
		newID, err := s.maildir.DeliverTo(s.cfg.Domain, sess.authedUser, dest, raw)
		if err != nil {
			continue
		}
		srcUIDs = append(srcUIDs, strconv.FormatUint(uint64(m.uid), 10))
		if s.uids != nil {
			if u, err := s.uids.Assign(sess.authedMail, dest, idBaseName(newID)); err == nil {
				dstUIDs = append(dstUIDs, strconv.FormatUint(uint64(u), 10))
			}
		}
	}
	if s.uids != nil && len(dstUIDs) == len(srcUIDs) && len(dstUIDs) > 0 {
		line(fmt.Sprintf("%s OK [COPYUID %d %s %s] COPY completed",
			tag, s.uidValidity(sess.authedMail, dest), strings.Join(srcUIDs, ","), strings.Join(dstUIDs, ",")))
		return
	}
	line(tag + " OK COPY completed")
}

func (s *IMAPServer) doMove(w *bufio.Writer, line func(string), sess *imapSession, tag, arg string, byUID bool) {
	if sess.selected == "" {
		line(tag + " NO No mailbox selected")
		return
	}
	if sess.readOnly {
		line(tag + " NO Mailbox is read-only")
		return
	}
	seqPart, mbox := cutSpace(arg)
	dest := canonicalFolder(strings.Trim(strings.TrimSpace(mbox), `"`))
	targets := s.resolveSet(sess, seqPart, byUID)
	moved := map[string]bool{}
	for _, m := range targets {
		raw, err := s.maildir.ReadRawFolder(s.cfg.Domain, sess.authedUser, sess.selected, m.id)
		if err != nil {
			continue
		}
		if _, err := s.maildir.DeliverTo(s.cfg.Domain, sess.authedUser, dest, raw); err != nil {
			continue
		}
		if err := s.maildir.deleteMessage(s.cfg.Domain, sess.authedUser, sess.selected, m.id); err == nil {
			moved[m.id] = true
		}
	}
	s.emitExpunges(w, sess, moved)
	line(tag + " OK MOVE completed")
}

func (s *IMAPServer) doExpunge(w *bufio.Writer, line func(string), sess *imapSession, tag, _ string) {
	if sess.selected == "" {
		line(tag + " NO No mailbox selected")
		return
	}
	if sess.readOnly {
		line(tag + " NO Mailbox is read-only")
		return
	}
	s.expungeDeleted(sess, w)
	line(tag + " OK EXPUNGE completed")
}

// expungeDeleted removes every message flagged \Deleted, emitting EXPUNGE
// responses (descending seq) when w is non-nil, then re-snapshots.
func (s *IMAPServer) expungeDeleted(sess *imapSession, w *bufio.Writer) {
	del := map[string]bool{}
	for _, m := range sess.msgs {
		if m.flags['T'] {
			if err := s.maildir.deleteMessage(s.cfg.Domain, sess.authedUser, sess.selected, m.id); err == nil {
				del[m.id] = true
			}
		}
	}
	s.emitExpunges(w, sess, del)
}

// emitExpunges sends `* n EXPUNGE` for removed ids (highest seq first) and
// refreshes the snapshot so sequence numbers stay correct.
func (s *IMAPServer) emitExpunges(w *bufio.Writer, sess *imapSession, removed map[string]bool) {
	if len(removed) == 0 {
		return
	}
	var seqs []int
	for _, m := range sess.msgs {
		if removed[m.id] {
			seqs = append(seqs, m.seq)
		}
	}
	sort.Sort(sort.Reverse(sort.IntSlice(seqs)))
	if w != nil {
		for _, sq := range seqs {
			_, _ = w.WriteString(fmt.Sprintf("* %d EXPUNGE\r\n", sq))
		}
		_ = w.Flush()
	}
	_ = s.snapshot(sess, sess.selected)
}

// ── SEARCH ───────────────────────────────────────────────────────────────────

func (s *IMAPServer) doSearch(line func(string), sess *imapSession, tag, arg string, byUID bool) {
	if sess.selected == "" {
		line(tag + " NO No mailbox selected")
		return
	}
	matches := s.search(sess, arg)
	var ids []string
	for _, m := range matches {
		if byUID {
			ids = append(ids, strconv.FormatUint(uint64(m.uid), 10))
		} else {
			ids = append(ids, strconv.Itoa(m.seq))
		}
	}
	if len(ids) == 0 {
		line("* SEARCH")
	} else {
		line("* SEARCH " + strings.Join(ids, " "))
	}
	line(tag + " OK SEARCH completed")
}

// search applies a bounded subset of SEARCH criteria. Unknown criteria are
// ignored (treated as ALL), so a client never gets an error — at worst an
// over-broad result it filters itself.
func (s *IMAPServer) search(sess *imapSession, criteria string) []*imapMsg {
	toks := strings.Fields(criteria)
	var out []*imapMsg
	for i := range sess.msgs {
		m := &sess.msgs[i]
		if s.matchesCriteria(sess, m, toks) {
			out = append(out, m)
		}
	}
	return out
}

func (s *IMAPServer) matchesCriteria(sess *imapSession, m *imapMsg, toks []string) bool {
	var hdr string
	loadHdr := func() string {
		if hdr == "" {
			if raw, err := s.maildir.ReadRawFolder(s.cfg.Domain, sess.authedUser, sess.selected, m.id); err == nil {
				hb, _ := splitHeaderBody(raw)
				hdr = strings.ToLower(string(hb))
			}
		}
		return hdr
	}
	for i := 0; i < len(toks); i++ {
		t := strings.ToUpper(toks[i])
		switch t {
		case "ALL":
		case "UNSEEN", "NEW":
			if m.flags['S'] {
				return false
			}
		case "SEEN":
			if !m.flags['S'] {
				return false
			}
		case "FLAGGED":
			if !m.flags['F'] {
				return false
			}
		case "UNFLAGGED":
			if m.flags['F'] {
				return false
			}
		case "ANSWERED":
			if !m.flags['R'] {
				return false
			}
		case "DELETED":
			if !m.flags['T'] {
				return false
			}
		case "DRAFT":
			if !m.flags['D'] {
				return false
			}
		case "RECENT":
			if m.flags['S'] {
				return false
			}
		case "FROM", "TO", "CC", "SUBJECT", "BODY", "TEXT", "HEADER":
			needle := ""
			if t == "HEADER" && i+2 < len(toks) {
				needle = strings.ToLower(strings.Trim(toks[i+2], `"`))
				i += 2
			} else if i+1 < len(toks) {
				needle = strings.ToLower(strings.Trim(toks[i+1], `"`))
				i++
			}
			if needle == "" {
				continue
			}
			hay := loadHdr()
			if t == "BODY" || t == "TEXT" {
				if raw, err := s.maildir.ReadRawFolder(s.cfg.Domain, sess.authedUser, sess.selected, m.id); err == nil {
					hay = strings.ToLower(string(raw))
				}
			}
			if !strings.Contains(hay, needle) {
				return false
			}
		case "LARGER":
			if i+1 < len(toks) {
				n, _ := strconv.ParseInt(toks[i+1], 10, 64)
				i++
				if m.size <= n {
					return false
				}
			}
		case "SMALLER":
			if i+1 < len(toks) {
				n, _ := strconv.ParseInt(toks[i+1], 10, 64)
				i++
				if m.size >= n {
					return false
				}
			}
		case "UID":
			if i+1 < len(toks) {
				set := toks[i+1]
				i++
				if !uidInSet(m.uid, set) {
					return false
				}
			}
		}
	}
	return true
}

func uidInSet(uid uint32, set string) bool {
	for _, part := range strings.Split(set, ",") {
		if lo, hi, ok := parseUIDRange(part); ok {
			if uid >= lo && uid <= hi {
				return true
			}
		}
	}
	return false
}

// ── UID dispatch ─────────────────────────────────────────────────────────────

func (s *IMAPServer) doUID(w *bufio.Writer, line func(string), sess *imapSession, tag, arg string) {
	sub, rest := cutSpace(arg)
	switch strings.ToUpper(sub) {
	case "FETCH":
		s.doFetch(w, line, sess, tag, rest, true)
	case "STORE":
		s.doStore(w, line, sess, tag, rest, true)
	case "SEARCH":
		s.doSearch(line, sess, tag, rest, true)
	case "COPY":
		s.doCopy(line, sess, tag, rest, true)
	case "MOVE":
		s.doMove(w, line, sess, tag, rest, true)
	case "EXPUNGE":
		s.doExpunge(w, line, sess, tag, rest)
	default:
		line(tag + " BAD Unknown UID command")
	}
}

// resolveSet maps a sequence-set or UID-set spec to the matching messages.
func (s *IMAPServer) resolveSet(sess *imapSession, spec string, byUID bool) []*imapMsg {
	spec = strings.TrimSpace(spec)
	var out []*imapMsg
	maxUID := uint32(0)
	for i := range sess.msgs {
		if sess.msgs[i].uid > maxUID {
			maxUID = sess.msgs[i].uid
		}
	}
	for _, segment := range strings.Split(spec, ",") {
		if byUID {
			lo, hi, ok := parseUIDRangeStar(segment, maxUID)
			if !ok {
				continue
			}
			for i := range sess.msgs {
				if sess.msgs[i].uid >= lo && sess.msgs[i].uid <= hi {
					out = append(out, &sess.msgs[i])
				}
			}
		} else {
			lo, hi := parseSeqSet(segment, len(sess.msgs))
			for i := range sess.msgs {
				seq := sess.msgs[i].seq
				if seq >= lo && seq <= hi {
					out = append(out, &sess.msgs[i])
				}
			}
		}
	}
	return out
}

func parseUIDRange(seg string) (lo, hi uint32, ok bool) {
	return parseUIDRangeStar(seg, 0xffffffff)
}

func parseUIDRangeStar(seg string, star uint32) (lo, hi uint32, ok bool) {
	seg = strings.TrimSpace(seg)
	if seg == "" {
		return 0, 0, false
	}
	parse := func(s string) (uint32, bool) {
		if s == "*" {
			return star, true
		}
		n, err := strconv.ParseUint(s, 10, 32)
		if err != nil {
			return 0, false
		}
		return uint32(n), true
	}
	if i := strings.IndexByte(seg, ':'); i >= 0 {
		a, ok1 := parse(seg[:i])
		b, ok2 := parse(seg[i+1:])
		if !ok1 || !ok2 {
			return 0, 0, false
		}
		if a > b {
			a, b = b, a
		}
		return a, b, true
	}
	n, ok1 := parse(seg)
	if !ok1 {
		return 0, 0, false
	}
	return n, n, true
}

// ── APPEND ───────────────────────────────────────────────────────────────────

// doAppend stores a client-supplied message into a folder (clients use this to
// save Sent and Draft copies). It reads the {n} literal that terminates the
// command line.
func (s *IMAPServer) doAppend(br *bufio.Reader, w *bufio.Writer, line func(string), sess *imapSession, tag, arg string) {
	if !sess.authed() {
		line(tag + " NO Not authenticated")
		return
	}
	// arg: <mailbox> [(flags)] ["date"] {n}
	lb := strings.LastIndexByte(arg, '{')
	rb := strings.LastIndexByte(arg, '}')
	if lb < 0 || rb < 0 || rb < lb {
		line(tag + " BAD APPEND expects a literal")
		return
	}
	numStr := strings.TrimSuffix(arg[lb+1:rb], "+")
	n, err := strconv.Atoi(numStr)
	if err != nil || n < 0 || int64(n) > s.cfg.MaxMessageBytes {
		line(tag + " NO APPEND size invalid or too large")
		return
	}
	head := strings.TrimSpace(arg[:lb])
	folder := "Inbox"
	flags := map[byte]bool{}
	if fields := tokenizeAppendHead(head); len(fields) > 0 {
		folder = canonicalFolder(strings.Trim(fields[0], `"`))
	}
	if i := strings.IndexByte(head, '('); i >= 0 {
		if j := strings.IndexByte(head, ')'); j > i {
			flags = parseFlagList(head[i : j+1])
		}
	}
	// Non-synchronizing literal ({n+}) sends data immediately; otherwise prompt.
	if !strings.HasSuffix(arg[:rb+1], "+}") {
		_, _ = w.WriteString("+ Ready for literal data\r\n")
		_ = w.Flush()
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(br, buf); err != nil {
		return
	}
	_, _ = br.ReadString('\n') // trailing CRLF after the literal

	newID, err := s.maildir.DeliverTo(s.cfg.Domain, sess.authedUser, folder, buf)
	if err != nil {
		line(tag + " NO APPEND failed")
		return
	}
	if len(flags) > 0 {
		if id2, ferr := s.maildir.setMessageFlags(s.cfg.Domain, sess.authedUser, folder, newID, flags); ferr == nil {
			newID = id2
		}
	}
	if s.uids != nil {
		if u, aerr := s.uids.Assign(sess.authedMail, folder, idBaseName(newID)); aerr == nil {
			line(fmt.Sprintf("%s OK [APPENDUID %d %d] APPEND completed", tag, s.uidValidity(sess.authedMail, folder), u))
			return
		}
	}
	line(tag + " OK APPEND completed")
}

// tokenizeAppendHead splits the APPEND head into whitespace tokens, keeping a
// parenthesised flag group and quoted strings intact.
func tokenizeAppendHead(head string) []string {
	var out []string
	var cur strings.Builder
	depth := 0
	inQ := false
	flush := func() {
		if cur.Len() > 0 {
			out = append(out, cur.String())
			cur.Reset()
		}
	}
	for _, r := range head {
		switch {
		case r == '"':
			inQ = !inQ
			cur.WriteRune(r)
		case r == '(' && !inQ:
			depth++
			cur.WriteRune(r)
		case r == ')' && !inQ:
			depth--
			cur.WriteRune(r)
		case r == ' ' && depth == 0 && !inQ:
			flush()
		default:
			cur.WriteRune(r)
		}
	}
	flush()
	return out
}

// ── IDLE ─────────────────────────────────────────────────────────────────────

// doIdle implements RFC 2177 IDLE with poll-based change notification: while the
// client idles we periodically re-scan the selected folder and push an EXISTS
// update when new mail arrives, until the client sends DONE.
func (s *IMAPServer) doIdle(br *bufio.Reader, w *bufio.Writer, line func(string), sess *imapSession, tag string, conn *net.Conn) {
	if sess.selected == "" {
		line(tag + " NO No mailbox selected")
		return
	}
	_, _ = w.WriteString("+ idling\r\n")
	_ = w.Flush()
	lastCount := len(sess.msgs)
	doneCh := make(chan struct{})
	// Reader goroutine waits for the "DONE" line.
	go func() {
		l, err := br.ReadString('\n')
		_ = l
		_ = err
		close(doneCh)
	}()
	ticker := time.NewTicker(20 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-doneCh:
			line(tag + " OK IDLE terminated")
			return
		case <-ticker.C:
			msgs, err := s.maildir.ListFolder(s.cfg.Domain, sess.authedUser, sess.selected)
			if err == nil && len(msgs) != lastCount {
				lastCount = len(msgs)
				_, _ = w.WriteString(fmt.Sprintf("* %d EXISTS\r\n", lastCount))
				_ = w.Flush()
			}
			_ = conn
		}
	}
}

// ── shared parsing helpers ───────────────────────────────────────────────────

func cutSpace(s string) (head, rest string) {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, ' '); i >= 0 {
		return s[:i], strings.TrimSpace(s[i+1:])
	}
	return s, ""
}

func parseLoginArgs(arg string) (user, pass string) {
	parts := tokenizeQuoted(arg)
	switch {
	case len(parts) >= 2:
		return parts[0], parts[1]
	case len(parts) == 1:
		return parts[0], ""
	default:
		return "", ""
	}
}

// tokenizeQuoted splits a string into space-separated tokens, treating
// double-quoted spans as single tokens and PRESERVING empty quoted strings
// (e.g. the `""` reference in `LIST "" "*"`).
func tokenizeQuoted(s string) []string {
	var out []string
	i, n := 0, len(s)
	for i < n {
		for i < n && s[i] == ' ' {
			i++
		}
		if i >= n {
			break
		}
		if s[i] == '"' {
			i++
			start := i
			for i < n && s[i] != '"' {
				i++
			}
			out = append(out, s[start:i])
			if i < n {
				i++ // skip closing quote
			}
		} else {
			start := i
			for i < n && s[i] != ' ' {
				i++
			}
			out = append(out, s[start:i])
		}
	}
	return out
}

func parseSeqSet(s string, count int) (lo, hi int) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 1, count
	}
	if i := strings.IndexByte(s, ':'); i >= 0 {
		lo = atoiDefault(s[:i], 1)
		if h := s[i+1:]; h == "*" {
			hi = count
		} else {
			hi = atoiDefault(h, count)
		}
		if lo > hi {
			lo, hi = hi, lo
		}
		return lo, hi
	}
	if s == "*" {
		return count, count
	}
	n := atoiDefault(s, 0)
	return n, n
}

func atoiDefault(s string, def int) int {
	if s == "" {
		return def
	}
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return def
		}
		n = n*10 + int(r-'0')
	}
	return n
}
