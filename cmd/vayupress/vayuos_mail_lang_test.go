// SPDX-License-Identifier: Apache-2.0

package main

import (
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/i18n"
	"github.com/johalputt/vayupress/internal/users"
)

// TestMailIsReadInThePersonsLanguage — the language the console runs in for
// this request reaches Mail through its Reader, and an English request leaves
// the Reader as every other place makes it.
func TestMailIsReadInThePersonsLanguage(t *testing.T) {
	a, _ := ownMailboxApp(t)
	req := asAdminMailUser(&users.User{ID: "u1", Email: "dana@example.com", Role: users.RoleAdmin})

	if lang := a.mailReader(req, "").Lang(); lang != "en" {
		t.Errorf("an English request reads Mail in %q", lang)
	}
	de := req.WithContext(i18n.WithLang(req.Context(), "de"))
	rd := a.mailReader(de, "")
	if rd.Lang() != "de" {
		t.Errorf("a German request reads Mail in %q", rd.Lang())
	}
	if !rd.IsOwner() || rd.Key() != "dana" {
		t.Errorf("the language changed whose mailbox is read: owner=%v of %q", rd.IsOwner(), rd.Key())
	}
}

// TestMailsSidebarIsInThePersonsLanguage — folders, views, saved searches,
// labels and the menus beside them, in German, Japanese and French, with none
// of the English they replaced.
func TestMailsSidebarIsInThePersonsLanguage(t *testing.T) {
	nav := mailFolderNav(mailNav{
		Lang: "de", User: "dana", Active: "Inbox", Own: []string{"Projekte"}, Writable: true,
		Searches: []string{"from:eli"}, Labels: []string{"Rechnung"},
		ViewCounts: map[string]int{"followup": 2},
	})
	for _, want := range []string{">Posteingang<", ">Papierkorb<", ">Ordner<", "Neuer Ordner", ">Ansichten<", ">Nachfassen<", ">Suchen<", "Projekte", "Rechnung"} {
		if !strings.Contains(nav, want) {
			t.Errorf("the German sidebar has no %q", want)
		}
	}
	for _, english := range []string{">Inbox<", ">Trash<", ">Folders<", "New folder", ">Views<", ">Follow-ups<", ">Searches<"} {
		if strings.Contains(nav, english) {
			t.Errorf("the German sidebar says %q", english)
		}
	}

	if m := labelMenu("ja", "dana", "Inbox", "1", []string{"仕事"}, nil); !strings.Contains(m, "新しいラベル") || strings.Contains(m, "New label") {
		t.Errorf("the Japanese Label menu is not in Japanese: %s", m)
	}
	if m := ownFolderMenu("fr", "dana", "Projets"); !strings.Contains(m, "Supprimer le dossier") || strings.Contains(m, "Delete folder") {
		t.Errorf("the French folder menu is not in French: %s", m)
	}
}
