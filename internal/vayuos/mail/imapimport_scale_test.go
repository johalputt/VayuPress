// SPDX-License-Identifier: Apache-2.0

package mail

import (
	"reflect"
	"strings"
	"testing"
)

const mib = 1 << 20

// TestABatchStaysWithinItsBound — fifty messages of large attachments are
// never held at once: a batch stops before it passes the bound.
func TestABatchStaysWithinItsBound(t *testing.T) {
	uids := []uint64{1, 2, 3, 4}
	sizes := map[uint64]int64{1: 12 * mib, 2: 12 * mib, 3: 12 * mib, 4: 12 * mib}
	want, take, tooLarge := importBatch(uids, sizes, 25*mib, 32*mib)
	if !reflect.DeepEqual(want, []uint64{1, 2}) || take != 2 || tooLarge != 0 {
		t.Fatalf("12 MiB messages under a 32 MiB bound gave want %v, take %d, too large %d; want [1 2], 2, 0", want, take, tooLarge)
	}
	var held int64
	for _, u := range want {
		held += sizes[u]
	}
	if held > 32*mib {
		t.Errorf("the batch holds %d bytes, past its bound", held)
	}
}

// TestABigMessageStillComesAcross — a message larger than the bound but
// within what the server takes travels alone rather than never.
func TestABigMessageStillComesAcross(t *testing.T) {
	want, take, _ := importBatch([]uint64{7, 8}, map[uint64]int64{7: 40 * mib, 8: 1}, 64*mib, 32*mib)
	if !reflect.DeepEqual(want, []uint64{7}) || take != 1 {
		t.Fatalf("a 40 MiB message gave want %v, take %d; want it alone", want, take)
	}
}

// TestATooLargeMessageIsNotDownloaded — over the server's limit, a message is
// counted and skipped, never fetched.
func TestATooLargeMessageIsNotDownloaded(t *testing.T) {
	want, take, tooLarge := importBatch([]uint64{1, 2, 3}, map[uint64]int64{1: 100, 2: 30 * mib, 3: 100}, 25*mib, 32*mib)
	if !reflect.DeepEqual(want, []uint64{1, 3}) || take != 3 || tooLarge != 1 {
		t.Fatalf("got want %v, take %d, too large %d; want [1 3], 3, 1", want, take, tooLarge)
	}
}

// TestAnUnansweredSizeTravelsAlone — a server that gives no size for a
// message is not trusted to have sent a small one.
func TestAnUnansweredSizeTravelsAlone(t *testing.T) {
	want, take, _ := importBatch([]uint64{1, 2}, map[uint64]int64{}, 25*mib, 32*mib)
	if !reflect.DeepEqual(want, []uint64{1}) || take != 1 {
		t.Fatalf("with no sizes answered: want %v, take %d; want one at a time", want, take)
	}
}

// TestBringMailInCountsItsTotalAndSkipsWhatIsTooLarge — end to end against an
// IMAP server: the run counts what it will copy before it starts, a message
// over the limit is failed without being taken, and the rest arrive.
func TestBringMailInCountsItsTotalAndSkipsWhatIsTooLarge(t *testing.T) {
	remote, addr, trust := remoteProvider(t)
	remote.DeliverTo("example.com", "bob", "Inbox", remoteMessage("Small one", "a"))
	remote.DeliverTo("example.com", "bob", "Inbox", remoteMessage("Too big", strings.Repeat("x", 8192)))
	remote.DeliverTo("example.com", "bob", "Sent", remoteMessage("Small two", "b"))
	e := importInto(t, addr, trust)
	e.cfg.MaxMessageBytes = 4096
	st := runImportToEnd(t, e, bobAt)
	if st.State != ImportDone || st.Total != 3 || st.Copied != 2 || st.Failed != 1 {
		t.Fatalf("import ended %+v, want done, total 3, 2 copied, 1 failed", st)
	}
	inbox, _ := e.maildir.ListFolder("example.com", "alice", "Inbox")
	for _, m := range inbox {
		if strings.Contains(m.Subject, "Too big") {
			t.Error("the message over the limit was copied")
		}
	}
}

// TestAnOlderImportTableGainsTheTotal — an install whose import table was made
// before the total existed is given the column, keeping its rows.
func TestAnOlderImportTableGainsTheTotal(t *testing.T) {
	e := newLoopbackEngine(t, loopbackBridge{})
	if _, err := e.db.Exec(`DROP TABLE IF EXISTS vayumail_imports`); err != nil {
		t.Fatal(err)
	}
	if _, err := e.db.Exec(`CREATE TABLE vayumail_imports(
		id INTEGER PRIMARY KEY AUTOINCREMENT, mailbox TEXT NOT NULL, host TEXT NOT NULL, username TEXT NOT NULL,
		state TEXT NOT NULL, folder TEXT NOT NULL DEFAULT '', copied INTEGER NOT NULL DEFAULT 0,
		failed INTEGER NOT NULL DEFAULT 0, error TEXT NOT NULL DEFAULT '', updated_unix INTEGER NOT NULL,
		UNIQUE(mailbox, host, username));
		INSERT INTO vayumail_imports(mailbox,host,username,state,copied,updated_unix) VALUES('alice@example.com','mail.test','bob','done',9,1)`); err != nil {
		t.Fatal(err)
	}
	st, err := e.ImportStatusFor(ReadAsOwner("alice"))
	if err != nil {
		t.Fatalf("reading an import recorded before the total: %v", err)
	}
	if st == nil || st.Copied != 9 || st.Total != 0 {
		t.Fatalf("the older row read back as %+v, want its 9 copied and no total", st)
	}
	var n int
	if err := e.db.QueryRow(`SELECT COUNT(1) FROM pragma_table_info('vayumail_imports') WHERE name='total'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("the total column was not added (%d, %v)", n, err)
	}
}
