// SPDX-License-Identifier: Apache-2.0

package members

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"
)

// Every article view asks GetAccess, cached pages included. On the one write
// connection each view queued behind every write on the site; on the read pool
// it answers while that connection is held.
func TestTheAccessLookupDoesNotQueueBehindWrites(t *testing.T) {
	path := filepath.Join(t.TempDir(), "site.db")
	w, err := sql.Open("sqlite3", path+"?_journal_mode=WAL")
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	w.SetMaxOpenConns(1) // as the site's write connection
	if _, err := w.Exec(`CREATE TABLE article_access(slug TEXT PRIMARY KEY,level TEXT NOT NULL DEFAULT 'public',price_cents INTEGER NOT NULL DEFAULT 0);
		INSERT INTO article_access(slug,level) VALUES('gated','members')`); err != nil {
		t.Fatal(err)
	}
	r, err := sql.Open("sqlite3", path+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	s := New(w).WithReader(r)

	held, err := w.Conn(context.Background()) // a long write holds the connection
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if got := s.GetAccess(ctx, "gated"); got != AccessMembers {
		t.Fatalf("with the write connection held the lookup answered %q (it waited for it)", got)
	}
}

// A lookup that fails must not publish the post: an article read as public is
// rendered whole and written to the page cache for every later visitor.
func TestAnAccessLookupThatFailsKeepsThePostGated(t *testing.T) {
	s := newTestStore(t)
	broken, err := sql.Open("sqlite3", ":memory:") // no article_access table
	if err != nil {
		t.Fatal(err)
	}
	defer broken.Close()
	s.WithReader(broken)
	if got := s.GetAccess(context.Background(), "any-post"); got != AccessPaid {
		t.Fatalf("a failed lookup answered %q", got)
	}
}
