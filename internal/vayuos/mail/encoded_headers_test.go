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
