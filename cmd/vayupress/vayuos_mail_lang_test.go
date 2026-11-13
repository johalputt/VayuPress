// SPDX-License-Identifier: Apache-2.0

package main

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/johalputt/vayupress/internal/i18n"
	"github.com/johalputt/vayupress/internal/users"
	vmail "github.com/johalputt/vayupress/internal/vayuos/mail"
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

// TestMailsListIsInThePersonsLanguage — a folder drawn for a German request:
// its head, the selection, the date group and each row's words and actions,
// with none of the English they replaced; and an empty folder in Japanese.
func TestMailsListIsInThePersonsLanguage(t *testing.T) {
	a, holder, _ := reviewerApp(t, vmail.RoleMailbox)
	req := withUser(httptest.NewRequest(http.MethodGet, "/os/vayumail/inbox", nil), holder)
	ja := a.mailReader(req.WithContext(i18n.WithLang(req.Context(), "ja")), "")
	if empty, _ := a.vayuInboxBody(ja, "Archive", "", 0, ""); !strings.Contains(empty, "アーカイブには何もありません。") || strings.Contains(empty, "Nothing in") {
		t.Errorf("an empty folder is not in Japanese: %s", empty)
	}

	msg := "From: Eli <eli@example.org>\r\nTo: dana@example.com\r\nSubject: Hallo\r\nDate: " + time.Now().UTC().Format(time.RFC1123Z) + "\r\n\r\nHallo Dana\r\n"
	if _, err := a.vayuMail.DeliverInbound("eli@example.org", "dana@example.com", []byte(msg)); err != nil {
		t.Fatal(err)
	}
	de := a.mailReader(req.WithContext(i18n.WithLang(req.Context(), "de")), "")
	list, _ := a.vayuInboxBody(de, "Inbox", "", 0, "")
	for _, want := range []string{">Posteingang<", "Ordner</button>", ">Heute<", "Nachricht von Eli auswählen", "Als gelesen markieren", "Bis morgen zurückstellen", ">Alle auswählen<", "Verschieben nach…", "2 ungelesen", `aria-label="Nachrichten"`} {
		if !strings.Contains(list, want) {
			t.Errorf("the German list has no %q", want)
		}
	}
	for _, english := range []string{">Inbox<", "Folders</button>", ">Today<", "Select the message from", "Mark read", "Snooze until tomorrow", ">Select all<", "Move to…", " unread<", `aria-label="Messages"`} {
		if strings.Contains(list, english) {
			t.Errorf("the German list says %q", english)
		}
	}
}

// TestMailsDatesAreInThePersonsLanguage — a list's group heads and times, and
// the reader's time, in Japanese and Russian, and English as it was.
func TestMailsDatesAreInThePersonsLanguage(t *testing.T) {
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	thisWeek := now.AddDate(0, 0, -3) // Tuesday
	earlier := time.Date(2026, 3, 2, 9, 30, 0, 0, time.UTC)
	before := time.Date(2025, 3, 2, 9, 30, 0, 0, time.UTC)
	for _, c := range []struct{ got, want string }{
		{mailListGroup("en", now, now), "Today"},
		{mailListGroup("ja", now, now), "今日"},
		{mailListGroup("en", earlier, now), "March"},
		{mailListGroup("ru", earlier, now), "март"},
		{mailListGroup("en", before, now), "March 2025"},
		{mailListTime("en", thisWeek, now), "Tue"},
		{mailListTime("ja", thisWeek, now), "火曜日"},
		{mailListTime("en", earlier, now), "2 Mar"},
		{mailListTime("de", earlier, now), "2. März"},
		{mailListTime("en", before, now), "2 Mar 2025"},
		{mailReaderTime("en", now, now), "Today, 12:00"},
		{mailReaderTime("ko", now, now), "오늘 12:00"},
		{mailReaderTime("en", earlier, now), "Mon 2 Mar, 09:30"},
		{mailReaderTime("ru", before, now), "2 мар. 2025 г., 09:30"},
	} {
		if c.got != c.want {
			t.Errorf("got %q, want %q", c.got, c.want)
		}
	}
}

// TestScheduledAndUnsubscribeAreInThePersonsLanguage — a held message's row,
// failed, and the line under a list's mail, in Spanish.
func TestScheduledAndUnsubscribeAreInThePersonsLanguage(t *testing.T) {
	row := scheduledRow("es", "dana", vmail.ScheduledMessage{ID: 1, Due: time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC), State: vmail.ScheduledFailed}, nil)
	for _, want := range []string{"(sin asunto)", "No enviado", ">Enviar ahora<", ">Cancelar<"} {
		if !strings.Contains(row, want) {
			t.Errorf("the Spanish scheduled row has no %q", want)
		}
	}
	line := mailUnsubscribeLine("es", "dana", "Inbox", "1", vmail.UnsubscribeOffer{Link: "https://list.example/u"})
	if !strings.Contains(line, "Este es correo de una lista.") || !strings.Contains(line, "Cancelar la suscripción en list.example") || strings.Contains(line, "mail from a list") {
		t.Errorf("the Spanish unsubscribe line is not in Spanish: %s", line)
	}
}

// TestTheReaderIsInThePersonsLanguage — an open message in German: its tools,
// who it went to, the More menu and the reply field; and the trust line's
// warning in French.
func TestTheReaderIsInThePersonsLanguage(t *testing.T) {
	a, holder, _ := reviewerApp(t, vmail.RoleMailbox)
	req := withUser(httptest.NewRequest(http.MethodGet, "/os/vayumail/inbox", nil), holder)
	de := a.mailReader(req.WithContext(i18n.WithLang(req.Context(), "de")), "")
	msg := "From: Eli Rossi <eli@example.org>\r\nTo: dana@example.com\r\nMessage-ID: <de-1@example.org>\r\nSubject: Hallo\r\nDate: " + time.Now().UTC().Format(time.RFC1123Z) + "\r\n\r\nHallo Dana\r\n"
	id, err := a.vayuMail.DeliverInbound("eli@example.org", "dana@example.com", []byte(msg))
	if err != nil {
		t.Fatal(err)
	}
	card, ok := a.vayuReaderCard(de, "Inbox", id, readerView{Pane: true, Peek: true})
	if !ok {
		t.Fatal("the message could not be read")
	}
	for _, want := range []string{`aria-label="Antworten"`, `aria-label="Allen antworten"`, `aria-label="In den Papierkorb"`, "an mich", "Eli antworten…", ">Drucken<", "Eli Rossi"} {
		if !strings.Contains(card, want) {
			t.Errorf("the German reader has no %q", want)
		}
	}
	for _, english := range []string{`aria-label="Reply"`, "Reply all", "Move to Trash", "to me", "Reply to Eli", ">Print<", "Show the original"} {
		if strings.Contains(card, english) {
			t.Errorf("the German reader says %q", english)
		}
	}
	got := mailTrustFor("fr", "", "", `"support@bank.example" <x@evil.example>`, true).Warning
	if want := "Cet expéditeur ne fait pas partie de vos contacts. Le nom indique support@bank.example, mais le message vient de x@evil.example."; got != want {
		t.Errorf("the French warning is %q, want %q", got, want)
	}
}

// TestTheReadersKeysAreFoundInAnyLanguage — admin-os-mail.js finds what each
// key does by its aria-keyshortcuts, so a reader in Japanese offers every key
// the script uses, on the control it means.
func TestTheReadersKeysAreFoundInAnyLanguage(t *testing.T) {
	a, holder, _ := reviewerApp(t, vmail.RoleMailbox)
	req := withUser(httptest.NewRequest(http.MethodGet, "/os/vayumail/inbox", nil), holder)
	ja := a.mailReader(req.WithContext(i18n.WithLang(req.Context(), "ja")), "")
	for i, subj := range []string{"one", "two", "three"} {
		msg := "From: Eli <eli@example.org>\r\nTo: dana@example.com\r\nMessage-ID: <k-" + subj + "@example.org>\r\nSubject: " + subj + "\r\nDate: " + time.Now().Add(time.Duration(i)*time.Minute).UTC().Format(time.RFC1123Z) + "\r\n\r\nbody\r\n"
		if _, err := a.vayuMail.DeliverInbound("eli@example.org", "dana@example.com", []byte(msg)); err != nil {
			t.Fatal(err)
		}
	}
	// A message with one newer and one older, by the id the list gives it.
	list, err := a.vayuMail.ListFolder(ja, "Inbox")
	if err != nil || len(list) < 3 {
		t.Fatalf("the Inbox holds %d messages (%v)", len(list), err)
	}
	card, _ := a.vayuReaderCard(ja, "Inbox", list[1].ID, readerView{Pane: true, Peek: true})
	for key, what := range map[string]string{"r": "返信", "a": "全員に返信", "f": "転送", "#": "ゴミ箱に移動", "e": "アーカイブ", "!": "迷惑メール", "s": "ピン留め", "j": "古いメッセージ", "k": "新しいメッセージ"} {
		if !strings.Contains(card, `aria-label="`+what+`"`) || !regexp.MustCompile(`aria-label="`+regexp.QuoteMeta(what)+`"[^>]*aria-keyshortcuts="`+regexp.QuoteMeta(key)+`"`).MatchString(card) {
			t.Errorf("the Japanese reader's %q is not on the key %q", what, key)
		}
	}
	for key, what := range map[string]string{"v": "移動先", "b": "スヌーズ", "l": "ラベル"} {
		if !regexp.MustCompile(`<summary[^>]*aria-label="` + what + `" aria-keyshortcuts="` + key + `"`).MatchString(card) {
			t.Errorf("the Japanese reader's %s menu is not on the key %q", what, key)
		}
	}
}
