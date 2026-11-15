// SPDX-License-Identifier: Apache-2.0

package mail

import (
	"net/url"
	"strings"
	"testing"
)

// The parsers here read what anyone on the internet can send: a message, its
// List-Unsubscribe header, an invitation it carries, a calendar feed at an
// address the holder subscribed to, a contacts file, an IMAP client's
// sequence set. Each target holds a parser to what its callers rely on, and
// its seeds run with every go test; `go test -fuzz` explores from them.

const fuzzMessage = "From: a@example.org\r\nTo: b@example.org\r\nSubject: =?UTF-8?B?aMOpbGxv?=\r\n" +
	"MIME-Version: 1.0\r\nContent-Type: multipart/mixed; boundary=X\r\n\r\n" +
	"--X\r\nContent-Type: text/plain; charset=utf-8\r\nContent-Transfer-Encoding: quoted-printable\r\n\r\nh=C3=A9llo\r\n" +
	"--X\r\nContent-Type: application/pdf\r\nContent-Disposition: attachment; filename=\"a.pdf\"\r\nContent-Transfer-Encoding: base64\r\n\r\nJVBERi0=\r\n" +
	"--X--\r\n"

// Every attachment the reader lists can be downloaded: the listing and the
// download walk a message separately, and a link to an attachment the
// download cannot find is a broken link in every copy of that message.
func FuzzParseMessage(f *testing.F) {
	f.Add([]byte(fuzzMessage))
	f.Add([]byte("Subject: plain\r\n\r\nbody"))
	f.Add([]byte("Content-Type: multipart/alternative; boundary=\"\"\r\n\r\n--\r\n"))
	f.Add([]byte("Content-Type: multipart/mixed; boundary=A\r\n\r\n--A\r\nContent-Type: multipart/mixed; boundary=A\r\n\r\n--A--\r\n"))
	f.Fuzz(func(t *testing.T, raw []byte) {
		pm := ParseMessage(raw)
		for i, a := range pm.Attachments {
			if a.Index != i {
				t.Fatalf("attachment %d is numbered %d", i, a.Index)
			}
			if _, _, _, ok := ExtractAttachment(raw, a.Index); !ok {
				t.Fatalf("attachment %d (%q) is listed but cannot be downloaded", i, a.Filename)
			}
		}
	})
}

// This server posts a one-click unsubscribe itself, so the address it
// posts to is only ever https with a host; a mailto is only ever mailto.
func FuzzParseListUnsubscribe(f *testing.F) {
	f.Add([]byte("List-Unsubscribe: <https://example.org/u?x=1>, <mailto:u@example.org>\r\nList-Unsubscribe-Post: List-Unsubscribe=One-Click\r\n\r\n"))
	f.Add([]byte("List-Unsubscribe: <http://example.org/u>\r\n\r\n"))
	f.Add([]byte("List-Unsubscribe: <javascript:alert(1)>, <HTTPS:/x>\r\n\r\n"))
	f.Fuzz(func(t *testing.T, raw []byte) {
		o := ParseListUnsubscribe(raw)
		for name, v := range map[string]string{"one-click": o.OneClick, "link": o.Link} {
			if v == "" {
				continue
			}
			u, err := url.Parse(v)
			if err != nil || !strings.EqualFold(u.Scheme, "https") || u.Host == "" {
				t.Fatalf("the %s address %q is not an https address with a host", name, v)
			}
		}
		if o.Mailto != "" && !strings.HasPrefix(strings.ToLower(o.Mailto), "mailto:") {
			t.Fatalf("the mailto address %q is not mailto:", o.Mailto)
		}
	})
}

// An invitation is only ever a REQUEST or a CANCEL, about an event with a
// UID, from an organizer other than the mailbox it is read for.
func FuzzInvitationOf(f *testing.F) {
	ics := "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//x//x//EN\r\nMETHOD:REQUEST\r\nBEGIN:VEVENT\r\nUID:1@x\r\nDTSTAMP:20261115T090000Z\r\n" +
		"DTSTART:20261116T090000Z\r\nORGANIZER:mailto:o@example.org\r\nATTENDEE:mailto:me@example.org\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"
	f.Add([]byte("Content-Type: text/calendar; method=REQUEST\r\n\r\n" + ics))
	f.Add([]byte("Content-Type: multipart/mixed; boundary=B\r\n\r\n--B\r\nContent-Type: text/calendar\r\n\r\n" + ics + "--B--\r\n"))
	f.Add([]byte("Content-Type: text/calendar\r\n\r\nBEGIN:VCALENDAR\r\nMETHOD:PUBLISH\r\nEND:VCALENDAR\r\n"))
	f.Fuzz(func(t *testing.T, raw []byte) {
		method, cal, ev, ok := invitationOf(raw, "me@example.org")
		if !ok {
			return
		}
		if method != "REQUEST" && method != "CANCEL" {
			t.Fatalf("an invitation read as method %q", method)
		}
		if cal == nil || ev == nil || textOf(ev, "UID") == "" {
			t.Fatal("an invitation read with no event, or an event with no UID")
		}
		if org := ev.Props.Get("ORGANIZER"); org == nil || mailtoOf(org.Value) == "me@example.org" {
			t.Fatal("the mailbox's own event read as an invitation to it")
		}
	})
}

// A feed is one calendar or more, or an error; never nothing and no error,
// which the subscription would store as an empty calendar that "worked".
func FuzzParseFeed(f *testing.F) {
	f.Add([]byte("BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:x\r\nBEGIN:VEVENT\r\nUID:a\r\nDTSTAMP:20261115T090000Z\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"))
	f.Add([]byte("<html>not a calendar</html>"))
	f.Add([]byte("BEGIN:VCALENDAR\r\nEND:VCALENDAR\r\nBEGIN:VCALENDAR\r\n"))
	f.Fuzz(func(t *testing.T, data []byte) {
		cals, err := parseFeed(data)
		if err == nil && len(cals) == 0 {
			t.Fatal("no calendar and no error")
		}
	})
}

// An address read from a contacts file becomes a recipient: it never holds
// a line break, which would let a file write headers into a message.
func FuzzParseVCards(f *testing.F) {
	f.Add([]byte("BEGIN:VCARD\r\nVERSION:4.0\r\nFN:A Person\r\nEMAIL:a@example.org\r\nEND:VCARD\r\n"))
	f.Add([]byte("BEGIN:VCARD\r\nVERSION:2.1\r\nN:Family;Given\r\nEMAIL;ENCODING=QUOTED-PRINTABLE:a=0D=0Ab@x=\r\n.org\r\nEND:VCARD\r\n"))
	f.Add([]byte("BEGIN:VCARD\nEMAIL:mailto:x@y\n folded\nCATEGORIES:a,b\nEND:VCARD\n"))
	f.Fuzz(func(t *testing.T, data []byte) {
		cards, err := ParseVCards(data)
		if err == nil && len(cards) == 0 {
			t.Fatal("no card and no error")
		}
		for _, c := range cards {
			for _, e := range c.Emails {
				if strings.ContainsAny(e, "\r\n") {
					t.Fatalf("an address read with a line break: %q", e)
				}
			}
		}
	})
}

// A sequence set from an IMAP client is a range, low to high, of numbers no
// client can push below zero: a number too long for an int once wrapped
// negative and turned "1:<huge>" into the first message alone.
func FuzzParseSeqSet(f *testing.F) {
	for _, s := range []string{"", "*", "1", "1:*", "3:1", "2:5", "1:99999999999999999999", "1:9223372036854775808", "x:y"} {
		f.Add(s, 10)
	}
	f.Fuzz(func(t *testing.T, s string, count int) {
		if count < 0 || count > 1<<20 {
			return
		}
		lo, hi := parseSeqSet(s, count)
		// An empty mailbox has no range to give: "1:0" there selects nothing,
		// which is the answer.
		if lo > hi && count > 0 {
			t.Fatalf("%q read as %d:%d, low above high", s, lo, hi)
		}
		if lo < 0 {
			t.Fatalf("%q read as %d:%d, below zero", s, lo, hi)
		}
	})
}
