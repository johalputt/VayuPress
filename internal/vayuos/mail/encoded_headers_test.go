// SPDX-License-Identifier: Apache-2.0

package mail

// encoded_headers_test.go — a subject or name beyond ASCII arrives as RFC 2047
// encoded-words, often folded over several lines, and is read as the reader
// sees it everywhere: the listing, a filter, a reply. The fixture is the
// shape a real reply arrived in (operator's screenshot, 2026-10-16): the list
// showed "=?UTF-8?Q?Re=3A_Monolith_to_m…".

import (
	"strings"
	"testing"
)

const foldedSubject = "Subject: =?UTF-8?Q?Re=3A_Monolith_to_microservices_resource_=E2=80=94_added_to_our_?=\n =?UTF-8?Q?article?=\n"
const decodedSubject = "Re: Monolith to microservices resource — added to our article"

func TestTheListingReadsAnEncodedSubjectAndName(t *testing.T) {
	h := summary(t, "From: =?UTF-8?B?S2F0aGx5biBUYW50dcOhbg==?= <k@bairesdev.example>\n"+foldedSubject+"\nBody.\n")
	if h.subject != decodedSubject {
		t.Errorf("subject listed as %q, want %q", h.subject, decodedSubject)
	}
	if h.from != "Kathlyn Tantuán <k@bairesdev.example>" {
		t.Errorf("sender listed as %q", h.from)
	}
}

func TestAFilterMatchesWordsInAFoldedEncodedSubject(t *testing.T) {
	raw := []byte(strings.ReplaceAll("From: k@bairesdev.example\n"+foldedSubject+"\nBody.\n", "\n", "\r\n"))
	for _, words := range []string{"resource — added", "our article"} {
		if !matchFilter(FilterRule{Field: "subject", Contains: words}, raw) {
			t.Errorf("a filter on %q did not match the subject", words)
		}
	}
}

// A reply quotes what the reader shows: the subject decoded, and a
// quoted-printable body decoded rather than quoted as =E2=80=94; an HTML-only
// message is quoted as its text.
func TestAReplyQuotesTheMessageAsTheReaderShowsIt(t *testing.T) {
	qp := "From: k@bairesdev.example\n" + foldedSubject +
		"Date: Fri, 16 Oct 2026 15:39:00 +0530\nContent-Type: text/plain; charset=utf-8\nContent-Transfer-Encoding: quoted-printable\n\nThe link =E2=80=94 could it be followed?\n"
	_, subject, text, _ := ReplySource([]byte(strings.ReplaceAll(qp, "\n", "\r\n")))
	if subject != decodedSubject {
		t.Errorf("reply subject %q", subject)
	}
	if !strings.Contains(text, "The link — could it be followed?") {
		t.Errorf("quoted text %q", text)
	}
	html := "From: k@x.example\nSubject: s\nContent-Type: text/html; charset=utf-8\n\n<p>Only <b>HTML</b> here.</p>\n"
	if _, _, text, _ := ReplySource([]byte(strings.ReplaceAll(html, "\n", "\r\n"))); !strings.Contains(text, "Only HTML here.") || strings.Contains(text, "<p>") {
		t.Errorf("an HTML-only message was quoted as %q", text)
	}
}

// Release audit (3.18.6): a malformed line in the header block hides nothing
// after it. net/textproto stopped there, and the autoresponder's loop guard
// then missed Auto-Submitted and answered a machine.
func TestAMalformedHeaderLineHidesNothingAfterIt(t *testing.T) {
	raw := []byte("From: bot@x.example\r\nnot a header line\r\nAuto-Submitted: auto-replied\r\nSubject: a\r\n b\r\n\r\nBody.\r\n")
	if got := headerValue(raw, "Auto-Submitted"); got != "auto-replied" {
		t.Errorf("Auto-Submitted read as %q after a malformed line", got)
	}
	if got := headerValue(raw, "Subject"); got != "a b" {
		t.Errorf("a folded subject read as %q", got)
	}
}

// Release audit (3.18.6): an encoded-word can carry a line break, which a
// reply would put back into its Subject header as a header of the sender's
// choosing. Decoded, it is a space, in the listing and in the reply alike.
func TestAnEncodedLineBreakStartsNoHeader(t *testing.T) {
	raw := "From: x@evil.example\r\nSubject: =?UTF-8?Q?hi=0D=0ABcc=3A_victim=40x=2Eexample?=\r\n\r\nBody.\r\n"
	if _, subject, _, _ := ReplySource([]byte(raw)); strings.ContainsAny(subject, "\r\n") {
		t.Errorf("a reply's subject carries a line break: %q", subject)
	}
	if h := summary(t, raw); strings.ContainsAny(h.subject, "\r\n") {
		t.Errorf("the listing's subject carries a line break: %q", h.subject)
	}
}
