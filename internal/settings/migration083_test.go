// SPDX-License-Identifier: Apache-2.0

package settings

import (
	"database/sql"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

// AUDIT FINDING (pre-release pass over ADR-0153).
//
// Phase 4 made a hosted domain render from its own scope. That is the fix — and
// it silently changed what an EXISTING unbranded secondary domain is called.
//
// Before, an unbranded domain inherited the primary's name at render time. After,
// an unset key resolves to the compiled-in default, which is the PRODUCT's name.
// So an install would have come back up with a client's live site titled
// "VayuPress", and the operator would have learned it from the client.
//
// The fix is not to reinstate inheritance — that is the defect the whole ADR
// removes. It is to pick a default that BELONGS to the domain: a site nobody has
// named is named after its own hostname.

func migrate083Fixture(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })

	execShipped(t, db, "../db/migrations/010-site-settings.up.sql")
	execShipped(t, db, "../db/migrations/082-settings-scope.up.sql")
	if _, err := db.Exec(`CREATE TABLE domains(id TEXT PRIMARY KEY,host TEXT NOT NULL UNIQUE,config_json TEXT NOT NULL DEFAULT '',is_primary INTEGER NOT NULL DEFAULT 0)`); err != nil {
		t.Fatal(err)
	}
	for _, row := range [][]any{
		{"d1", "branded.example", `{"brand":{"site_name":"Acme Ltd","accent_light":"#ff0000"}}`, 0},
		{"d2", "unbranded.example", ``, 0},
		{"d3", "broken.example", `{not json`, 0},
		{"p", "studio.example", ``, 1},
	} {
		if _, err := db.Exec(`INSERT INTO domains(id,host,config_json,is_primary) VALUES(?,?,?,?)`, row...); err != nil {
			t.Fatal(err)
		}
	}
	execShipped(t, db, "../db/migrations/083-brand-into-scope.up.sql")
	return db
}

func nameFor(t *testing.T, db *sql.DB, scope string) string {
	t.Helper()
	var v string
	_ = db.QueryRow(`SELECT value FROM site_settings WHERE scope=? AND key='site.name'`, scope).Scan(&v)
	return v
}

func TestAnUnbrandedDomainIsNamedAfterItselfNotAfterTheProduct(t *testing.T) {
	db := migrate083Fixture(t)
	if got := nameFor(t, db, "d2"); got != "unbranded.example" {
		t.Errorf("an unbranded hosted domain is called %q. Left unset it resolves to the "+
			"compiled-in default — the PRODUCT's name — so a client's live site would have "+
			"retitled itself on upgrade, and the operator would have heard about it from "+
			"the client", got)
	}
	// Malformed config must not stop the domain getting a name.
	if got := nameFor(t, db, "d3"); got != "broken.example" {
		t.Errorf("a domain with unparseable config was left unnamed (%q)", got)
	}
}

func TestABrandedDomainKeepsItsOwnNameThroughTheMove(t *testing.T) {
	db := migrate083Fixture(t)
	if got := nameFor(t, db, "d1"); got != "Acme Ltd" {
		t.Errorf("a branded domain's name became %q — the hostname fallback overwrote the "+
			"name the operator chose", got)
	}
	var accent string
	_ = db.QueryRow(`SELECT value FROM site_settings WHERE scope='d1' AND key='theme.accent_light'`).Scan(&accent)
	if accent != "#ff0000" {
		t.Errorf("the domain's accent did not move out of the blob (%q)", accent)
	}
}

// The migration must write NOTHING for the primary's registry row.
//
// The first version of this test checked scope "" and passed against a mutation
// that dropped the is_primary guard — because the primary's ROW ID is not "".
// settings.ForPrimary().key() is "", while its domains row carries a real id, so
// removing the guard does not rename the primary: it creates rows under a scope
// nothing ever reads, which is worse than a visible error because it looks like
// nothing happened. Assert on the row count for the primary's id.
func TestThePrimaryGetsNoRowsFromTheMove(t *testing.T) {
	db := migrate083Fixture(t)

	// Nothing under the primary's own settings scope.
	if got := nameFor(t, db, ""); got != "" {
		t.Errorf("the migration wrote a site name onto the PRIMARY scope (%q), overwriting "+
			"the operator's own site identity", got)
	}
	// And nothing under the primary registry row's id either — those rows would
	// be permanently orphaned, since no scope is ever built from that id.
	var n int
	if err := db.QueryRow(`SELECT COUNT(1) FROM site_settings WHERE scope='p'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("%d row(s) written under the primary registry row's id. Nothing reads that "+
			"scope — settings.ForPrimary() keys on \"\" — so they are invisible forever and "+
			"the operator has no way to find or clear them", n)
	}
}
