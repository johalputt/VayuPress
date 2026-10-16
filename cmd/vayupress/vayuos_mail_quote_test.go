// SPDX-License-Identifier: Apache-2.0

package main

import (
	"strings"
	"testing"
)

// A reply's subject and quote are the message as the reader shows it: an
// encoded, folded subject decoded (it was put into the reply as
// =?UTF-8?Q?…?=), and a quoted-printable body decoded.
func TestAReplyTakesTheDecodedSubjectAndText(t *testing.T) {
	raw := strings.ReplaceAll("From: k@bairesdev.example\nSubject: =?UTF-8?Q?Re=3A_Monolith_=E2=80=94_added?=\n =?UTF-8?Q?_to_our_article?=\nDate: Fri, 16 Oct 2026 15:39:00 +0530\nContent-Type: text/plain; charset=utf-8\nContent-Transfer-Encoding: quoted-printable\n\nCould it be =E2=80=94 followed?\n", "\n", "\r\n")
	_, subject, text, date := parseForQuote([]byte(raw))
	if want := "Re: Monolith — added to our article"; subject != want {
		t.Errorf("subject %q, want %q", subject, want)
	}
	if !strings.Contains(text, "Could it be — followed?") {
		t.Errorf("quoted %q", text)
	}
	if date != "16 Oct 2026" {
		t.Errorf("date %q", date)
	}
}
