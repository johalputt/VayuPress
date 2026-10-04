// SPDX-License-Identifier: Apache-2.0

package mail

import (
	"archive/zip"
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/johalputt/vayupress/internal/pacedio"
)

// remoteProvider is another provider's IMAP server: this engine's own, over
// implicit TLS with its self-signed certificate for mail.test, holding bob's
// mailbox. It returns the server's maildir and the TLS config that trusts it.
func remoteProvider(t *testing.T) (*Maildir, string, *tls.Config) {
	t.Helper()
	srvTLS := testTLSConfig(t)
	cfg := DefaultConfig()
	cfg.Domain = "example.com"
	md := NewMaildir(t.TempDir())
	if err := md.CreateAll("example.com", "bob"); err != nil {
		t.Fatal(err)
	}
	srv := NewIMAPServer(cfg, stubBridge{}, md, nil).WithImplicitTLS(srvTLS, "127.0.0.1:0")
	if err := srv.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Stop(context.Background()) })
	leaf, err := x509.ParseCertificate(srvTLS.Certificates[0].Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(leaf)
	return md, srv.Addr(), &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}
}

func remoteMessage(subject, body string) []byte {
	return []byte("From: someone@elsewhere.test\r\nTo: bob@example.com\r\nSubject: " + subject + "\r\nMessage-ID: <" + strings.ReplaceAll(subject, " ", "") + "@elsewhere.test>\r\n\r\n" + body + "\r\n")
}

// importInto starts an engine for alice and points its importer at addr.
func importInto(t *testing.T, addr string, trust *tls.Config) *Engine {
	t.Helper()
	e := newLoopbackEngine(t, loopbackBridge{})
	if err := e.maildir.CreateAll("example.com", "alice"); err != nil {
		t.Fatal(err)
	}
	im := e.imports()
	im.dial = func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, addr)
	}
	im.tls = trust
	return e
}

func runImportToEnd(t *testing.T, e *Engine, req ImportRequest) *ImportStatus {
	t.Helper()
	rd := ReadAsOwner("alice")
	if err := e.StartImport(rd, req, pacedio.FullSpeed{}); err != nil {
		t.Fatalf("start: %v", err)
	}
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		st, err := e.ImportStatusFor(rd)
		if err != nil {
			t.Fatal(err)
		}
		if st != nil && st.State != ImportRunning {
			return st
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("the import did not finish")
	return nil
}

var bobAt = ImportRequest{Host: "mail.test", Port: 993, Username: "bob", Password: "pw"}

// Every folder comes across into its counterpart here, a folder of the
// provider's own becomes one of yours, and read and pinned marks come too.
func TestBringMailInCopiesFoldersAndMarks(t *testing.T) {
	remote, addr, trust := remoteProvider(t)
	id, _ := remote.DeliverTo("example.com", "bob", "Inbox", remoteMessage("First", "one"))
	if _, err := remote.setFlagFolder("example.com", "bob", "Inbox", id, 'S', true); err != nil {
		t.Fatal(err)
	}
	remote.DeliverTo("example.com", "bob", "Inbox", remoteMessage("Second", "two"))
	remote.DeliverTo("example.com", "bob", "Sent", remoteMessage("Mine", "sent"))
	if err := remote.CreateFolder("example.com", "bob", "Projects"); err != nil {
		t.Fatal(err)
	}
	remote.DeliverTo("example.com", "bob", "Projects", remoteMessage("Plan", "p"))

	e := importInto(t, addr, trust)
	st := runImportToEnd(t, e, bobAt)
	if st.State != ImportDone || st.Copied != 4 || st.Failed != 0 {
		t.Fatalf("import ended %+v, want done with 4 copied", st)
	}
	inbox, _ := e.maildir.ListFolder("example.com", "alice", "Inbox")
	if len(inbox) != 2 {
		t.Fatalf("Inbox holds %d, want 2", len(inbox))
	}
	seen := 0
	for _, m := range inbox {
		if m.Seen {
			seen++
		}
	}
	if seen != 1 {
		t.Fatalf("%d of the copied inbox are read, want the one read there", seen)
	}
	if sent, _ := e.maildir.ListFolder("example.com", "alice", "Sent"); len(sent) != 1 {
		t.Fatalf("Sent holds %d, want 1", len(sent))
	}
	if got, _ := e.maildir.ListFolder("example.com", "alice", "Projects"); len(got) != 1 {
		t.Fatalf("Projects holds %d, want the provider's folder copied as one of yours", len(got))
	}
}

// A mailbox that fills stops the copy, saying so, rather than filling past
// its quota.
func TestBringMailInStopsWhenTheMailboxIsFull(t *testing.T) {
	remote, addr, trust := remoteProvider(t)
	remote.DeliverTo("example.com", "bob", "Inbox", remoteMessage("Big", strings.Repeat("x", 4096)))
	e := importInto(t, addr, trust)
	if err := e.accounts.Create(context.Background(), "alice@example.com", "x", "Alice", RoleMailbox); err != nil {
		t.Fatal(err)
	}
	e.maildir.DeliverTo("example.com", "alice", "Inbox", []byte("Subject: here\r\n\r\n"+strings.Repeat("y", 4096)))
	if err := e.accounts.SetQuota(context.Background(), "alice@example.com", 1024); err != nil {
		t.Fatal(err)
	}
	st := runImportToEnd(t, e, bobAt)
	if st.State != ImportFailed || !strings.Contains(st.Error, "this mailbox is full") || st.Copied != 0 {
		t.Fatalf("into a full mailbox: %+v", st)
	}
}

// Running it again brings only what is new.
func TestBringingMailInAgainCopiesOnlyWhatIsNew(t *testing.T) {
	remote, addr, trust := remoteProvider(t)
	remote.DeliverTo("example.com", "bob", "Inbox", remoteMessage("Old", "o"))
	e := importInto(t, addr, trust)
	runImportToEnd(t, e, bobAt)
	remote.DeliverTo("example.com", "bob", "Inbox", remoteMessage("New", "n"))
	runImportToEnd(t, e, bobAt)
	if inbox, _ := e.maildir.ListFolder("example.com", "alice", "Inbox"); len(inbox) != 2 {
		t.Fatalf("after two runs Inbox holds %d, want the old message once and the new one", len(inbox))
	}
}

// The password is used for the run and kept nowhere.
func TestBringMailInStoresNoPassword(t *testing.T) {
	remote, addr, trust := remoteProvider(t)
	remote.DeliverTo("example.com", "bob", "Inbox", remoteMessage("Any", "a"))
	e := importInto(t, addr, trust)
	runImportToEnd(t, e, ImportRequest{Host: "mail.test", Port: 993, Username: "bob", Password: "pw"})
	for _, table := range []string{"vayumail_imports", "vayumail_import_progress"} {
		rows, err := e.db.Query(`SELECT * FROM ` + table)
		if err != nil {
			t.Fatal(err)
		}
		cols, _ := rows.Columns()
		for rows.Next() {
			vals := make([]any, len(cols))
			ptrs := make([]any, len(cols))
			for i := range vals {
				ptrs[i] = &vals[i]
			}
			_ = rows.Scan(ptrs...)
			for _, v := range vals {
				if s, ok := v.(string); ok && strings.Contains(s, "pw") {
					t.Fatalf("the password is stored in %s", table)
				}
			}
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		rows.Close()
	}
}

// The install's guarded dialer refuses a private address: Bring mail in is
// not a way to reach the server's own network.
func TestBringMailInRefusesAPrivateAddress(t *testing.T) {
	e := newLoopbackEngine(t, loopbackBridge{})
	if err := e.maildir.CreateAll("example.com", "alice"); err != nil {
		t.Fatal(err)
	}
	st := runImportToEnd(t, e, ImportRequest{Host: "127.0.0.1", Port: 993, Username: "bob", Password: "pw"})
	// The guard's own refusal, not merely a failed dial: nothing listens on
	// 127.0.0.1:993 here, so a dial the guard let through fails as well.
	if st.State != ImportFailed || !strings.Contains(st.Error, "refusing to connect to blocked address") {
		t.Fatalf("an import from 127.0.0.1 ended %+v", st)
	}
}

// Only IMAP's ports: any other would make it a way to probe services.
func TestBringMailInTakesOnlyIMAPPorts(t *testing.T) {
	dialled := false
	dial := func(context.Context, string, string) (net.Conn, error) { dialled = true; return nil, io.EOF }
	if _, err := dialIMAP(context.Background(), dial, nil, "mail.test", 25, "u", "p"); err == nil || dialled {
		t.Fatalf("port 25: err %v, dialled %v", err, dialled)
	}
}

// A read-only mailbox may not bring mail in.
func TestAReadOnlyMailboxCannotBringMailIn(t *testing.T) {
	e := newLoopbackEngine(t, loopbackBridge{})
	if err := e.accounts.Create(context.Background(), "alice@example.com", "x", "Alice", RoleReviewer); err != nil {
		t.Fatal(err)
	}
	if err := e.StartImport(ReadAsOwner("alice"), bobAt, pacedio.FullSpeed{}); err == nil {
		t.Fatal("a read-only mailbox started an import")
	}
}

// A run the server stopped says so after the restart, and waits for the
// password again.
func TestAnImportCutShortByARestartSaysSo(t *testing.T) {
	dir := t.TempDir()
	first := scheduledEngine(t, dir)
	if err := first.ensureImportTables(); err != nil {
		t.Fatal(err)
	}
	if _, err := first.db.Exec(`INSERT INTO vayumail_imports(mailbox,host,username,state,updated_unix) VALUES('alice@example.com','mail.test','bob',?,1)`, ImportRunning); err != nil {
		t.Fatal(err)
	}
	_ = first.Stop(context.Background())
	again := scheduledEngine(t, dir)
	st, _ := again.ImportStatusFor(ReadAsOwner("alice"))
	if st == nil || st.State != ImportInterrupted || !strings.Contains(st.Error, "Give the password again") {
		t.Fatalf("after a restart: %+v", st)
	}
}

// Where each provider folder goes; one seed per rule.
func TestImportTargets(t *testing.T) {
	for _, c := range []struct {
		f    remoteFolder
		want string
	}{
		{remoteFolder{name: "[Gmail]/All Mail", attrs: `\hasnochildren \all`, delim: "/"}, ""},
		{remoteFolder{name: "[Gmail]/Starred", attrs: `\flagged`, delim: "/"}, ""},
		{remoteFolder{name: "[Gmail]", attrs: `\noselect \haschildren`, delim: "/"}, ""},
		{remoteFolder{name: "INBOX", delim: "/"}, "Inbox"},
		{remoteFolder{name: "Gesendet", attrs: `\sent`, delim: "/"}, "Sent"},
		{remoteFolder{name: "Deleted Items", delim: "/"}, "Trash"},
		{remoteFolder{name: "Spam", delim: "."}, "Junk"},
		{remoteFolder{name: "Work/Clients", delim: "/"}, "Work - Clients"},
		{remoteFolder{name: "Scheduled", delim: "/"}, "Scheduled imported"},
		{remoteFolder{name: "R&AOk-sum&AOk-s", delim: "/"}, "Résumés"},
		{remoteFolder{name: "a.b", delim: "/"}, "a-b"},
	} {
		if got := importTarget(c.f); got != c.want {
			t.Errorf("importTarget(%q, %q) = %q, want %q", c.f.name, c.f.attrs, got, c.want)
		}
	}
	if got := ownFolderName(strings.Repeat("x", 60), "/"); !ValidFolderName(got) {
		t.Errorf("a long name became %q, not a valid folder name", got)
	}
}

func TestModifiedUTF7(t *testing.T) {
	for in, want := range map[string]string{"&AOk-t&AOk-": "été", "a&-b": "a&b", "plain": "plain", "&broken": "&broken", "&A,A-": "ϰ"} {
		if got := decodeMUTF7(in); got != want {
			t.Errorf("decodeMUTF7(%q) = %q, want %q", in, got, want)
		}
	}
}

// Download all mail: one mbox per folder with mail, From lines quoted so a
// reader cannot mistake a line of a message for the start of the next.
func TestTheMailboxDownloadsAsMbox(t *testing.T) {
	e := newLoopbackEngine(t, loopbackBridge{})
	if err := e.maildir.CreateAll("example.com", "alice"); err != nil {
		t.Fatal(err)
	}
	e.maildir.DeliverTo("example.com", "alice", "Inbox", []byte("From: a@b.test\r\nSubject: one\r\n\r\nFrom here on\r\n>From quoted\r\n"))
	e.maildir.DeliverTo("example.com", "alice", "Sent", []byte("From: alice@example.com\r\nSubject: two\r\n\r\nbody\r\n"))
	var buf bytes.Buffer
	n, err := e.ArchiveMailbox(context.Background(), ReadAsOwner("alice"), &buf)
	if err != nil || n != 2 {
		t.Fatalf("archive: %d messages, %v", n, err)
	}
	z, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]string{}
	for _, f := range z.File {
		r, _ := f.Open()
		b, _ := io.ReadAll(r)
		files[f.Name] = string(b)
	}
	if len(files) != 2 || files["Inbox.mbox"] == "" || files["Sent.mbox"] == "" {
		t.Fatalf("files %v, want Inbox.mbox and Sent.mbox only", len(files))
	}
	in := files["Inbox.mbox"]
	if !strings.HasPrefix(in, "From a@b.test ") || !strings.Contains(in, "\n>From here on\n") || !strings.Contains(in, "\n>>From quoted\n") || strings.Contains(in, "\r") {
		t.Fatalf("Inbox.mbox:\n%s", in)
	}
}

// "n:*" names the last message even when its UID is below n, as RFC 3501
// says and real servers do: the answer is filtered, so a run with nothing new
// copies nothing again.
func TestUIDsAfterLeavesOutWhatWasCopied(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	go func() {
		defer server.Close()
		buf := make([]byte, 256)
		n, _ := server.Read(buf)
		tag := strings.Fields(string(buf[:n]))[0]
		_, _ = io.WriteString(server, "* SEARCH 5\r\n"+tag+" OK done\r\n")
	}()
	c := &imapClient{conn: client, r: bufio.NewReader(client)}
	uids, err := c.uidsAfter(5)
	if err != nil || len(uids) != 0 {
		t.Fatalf("uidsAfter(5) with the server naming 5: %v, %v", uids, err)
	}
}
