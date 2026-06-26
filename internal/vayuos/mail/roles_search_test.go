package mail

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

func newAcctStore(t *testing.T) *AccountStore {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	db.SetMaxOpenConns(1) // :memory: is per-connection; pin the pool so migrations & queries share one DB
	t.Cleanup(func() { db.Close() })
	s, err := NewAccountStore(db)
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	return s
}

func TestAccountRoles(t *testing.T) {
	t.Parallel()
	s := newAcctStore(t)
	ctx := context.Background()

	// Create with explicit roles + a default (empty -> author) + a custom role.
	cases := map[string]string{
		"boss@x.test":   RoleAdministrator,
		"ed@x.test":     RoleEditor,
		"writer@x.test": "", // -> author
		"ro@x.test":     RoleReviewer,
		"bot@x.test":    "automation", // custom
	}
	for email, role := range cases {
		if err := s.Create(ctx, email, "hash", "Name", role); err != nil {
			t.Fatalf("create %s: %v", email, err)
		}
	}

	want := map[string]string{
		"boss@x.test": RoleAdministrator, "ed@x.test": RoleEditor,
		"writer@x.test": RoleAuthor, "ro@x.test": RoleReviewer, "bot@x.test": "automation",
	}
	for email, exp := range want {
		if got := s.RoleFor(ctx, email); got != exp {
			t.Errorf("RoleFor(%s)=%q want %q", email, got, exp)
		}
	}

	// List includes role.
	list, _ := s.List(ctx)
	if len(list) != 5 {
		t.Fatalf("want 5 accounts, got %d", len(list))
	}

	// SetRole changes it.
	if err := s.SetRole(ctx, "writer@x.test", RoleEditor); err != nil {
		t.Fatalf("setrole: %v", err)
	}
	if s.RoleFor(ctx, "writer@x.test") != RoleEditor {
		t.Fatalf("role not updated")
	}
	if err := s.SetRole(ctx, "nobody@x.test", RoleEditor); err == nil {
		t.Fatalf("SetRole on missing account should error")
	}
}

func TestRolePermissionHelpers(t *testing.T) {
	t.Parallel()
	// CanSend: everyone except reviewer.
	for _, r := range []string{RoleAdministrator, RoleEditor, RoleAuthor, "custom"} {
		if !RoleCanSend(r) {
			t.Errorf("RoleCanSend(%s) should be true", r)
		}
	}
	if RoleCanSend(RoleReviewer) {
		t.Errorf("reviewer must not send")
	}
	// CanDelete: admin + editor only.
	if !RoleCanDelete(RoleAdministrator) || !RoleCanDelete(RoleEditor) {
		t.Errorf("admin/editor should delete")
	}
	for _, r := range []string{RoleAuthor, RoleReviewer, "custom"} {
		if RoleCanDelete(r) {
			t.Errorf("RoleCanDelete(%s) should be false", r)
		}
	}
	// CanManageAccounts: admin only.
	if !RoleCanManageAccounts(RoleAdministrator) {
		t.Errorf("admin should manage accounts")
	}
	for _, r := range []string{RoleEditor, RoleAuthor, RoleReviewer} {
		if RoleCanManageAccounts(r) {
			t.Errorf("RoleCanManageAccounts(%s) should be false", r)
		}
	}
	// normRole / IsBuiltinRole.
	if normRole("  EDITOR ") != RoleEditor {
		t.Errorf("normRole should lowercase/trim")
	}
	if normRole("bad role!") != RoleAuthor {
		t.Errorf("invalid custom role should fall back to author")
	}
	if !IsBuiltinRole("Author") || IsBuiltinRole("automation") {
		t.Errorf("IsBuiltinRole wrong")
	}
}

func TestArchiveFolderPresent(t *testing.T) {
	t.Parallel()
	found := false
	for _, f := range StandardFolders {
		if f == "Archive" {
			found = true
		}
	}
	if !found {
		t.Fatalf("Archive should be a standard folder: %v", StandardFolders)
	}
	// Archive round-trips like any other folder.
	md := NewMaildir(t.TempDir())
	if _, err := md.DeliverTo("x.test", "bob", "Inbox", []byte("Subject: hi\r\n\r\nbody")); err != nil {
		t.Fatalf("deliver: %v", err)
	}
	inbox, _ := md.ListFolder("x.test", "bob", "Inbox")
	if len(inbox) != 1 {
		t.Fatalf("want 1 in inbox")
	}
	if err := md.MoveBetween("x.test", "bob", inbox[0].ID, "Inbox", "Archive"); err != nil {
		t.Fatalf("archive move: %v", err)
	}
	arch, _ := md.ListFolder("x.test", "bob", "Archive")
	if len(arch) != 1 {
		t.Fatalf("want 1 in Archive, got %d", len(arch))
	}
}

func TestMaildirSearch(t *testing.T) {
	t.Parallel()
	md := NewMaildir(t.TempDir())
	mk := func(folder, subj, from, body string) {
		raw := "From: " + from + "\r\nSubject: " + subj + "\r\n\r\n" + body
		if _, err := md.DeliverTo("x.test", "bob", folder, []byte(raw)); err != nil {
			t.Fatalf("deliver: %v", err)
		}
	}
	mk("Inbox", "Invoice March", "billing@acme.test", "Please pay the attached invoice")
	mk("Inbox", "Lunch", "friend@x.test", "are you free for lunch")
	mk("Sent", "Re: Invoice", "bob@x.test", "thanks, paying now")
	mk("Archive", "Old note", "self@x.test", "keyword-zeta lives only in this body")

	// Header (subject) match across folders.
	res, err := md.Search("x.test", "bob", "invoice", 100)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(res) != 2 {
		t.Fatalf("want 2 'invoice' hits (Inbox+Sent), got %d: %+v", len(res), res)
	}
	// Body-only match.
	res, _ = md.Search("x.test", "bob", "keyword-zeta", 100)
	if len(res) != 1 || res[0].Folder != "Archive" {
		t.Fatalf("body search failed: %+v", res)
	}
	// From match.
	res, _ = md.Search("x.test", "bob", "billing@acme", 100)
	if len(res) != 1 {
		t.Fatalf("from search failed: %+v", res)
	}
	// Limit is honoured.
	res, _ = md.Search("x.test", "bob", "x.test", 1)
	if len(res) != 1 {
		t.Fatalf("limit not honoured, got %d", len(res))
	}
	// Empty query returns nothing.
	if res, _ := md.Search("x.test", "bob", "   ", 100); len(res) != 0 {
		t.Fatalf("empty query should return no results")
	}
	_ = strings.TrimSpace("")
}
