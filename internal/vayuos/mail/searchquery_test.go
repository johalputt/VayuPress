// SPDX-License-Identifier: Apache-2.0

package mail

import (
	"reflect"
	"sort"
	"testing"
	"time"
)

func day(s string) time.Time { t, _ := time.Parse("2006-01-02", s); return t }

// Each term is read into its own part, one seed apiece; what cannot be read
// stays a word, and a term not yet given a value asks for nothing.
func TestSearchTermsAreRead(t *testing.T) {
	for in, want := range map[string]SearchQuery{
		"from:priya":                 {From: "priya"},
		"TO:dana@example.com":        {To: "dana@example.com"},
		`subject:"migration window"`: {Subject: "migration window"},
		"in:Sent":                    {Folder: "Sent"},
		"after:2026-09-01":           {After: day("2026-09-01")},
		"before:2026/09/30":          {Before: day("2026-09-30")},
		"has:attachment":             {Attachment: true},
		"is:unread":                  {Unread: true},
		`invoice "next week"`:        {Words: []string{"invoice", "next week"}},
		"label:work":                 {Words: []string{"label:work"}},
		"after:soon":                 {Words: []string{"after:soon"}},
		"has:star":                   {Words: []string{"has:star"}},
		"is:pinned":                  {Words: []string{"is:pinned"}},
		"from: hello":                {Words: []string{"hello"}},
		"after: x":                   {Words: []string{"x"}},
		`"from:priya"`:               {Words: []string{"from:priya"}},
		`"" x`:                       {Words: []string{"x"}},
	} {
		if got := ParseSearchQuery(in); !reflect.DeepEqual(got, want) {
			t.Errorf("ParseSearchQuery(%q) = %+v, want %+v", in, got, want)
		}
	}
}

// searchBox holds one message per rule, each the only one a term picks out.
func searchBox(t *testing.T) *Maildir {
	t.Helper()
	md := NewMaildir(t.TempDir())
	put := func(folder, name, headers, body string) {
		raw := "Message-ID: <" + name + "@t>\r\n" + headers + "\r\n\r\n" + body
		id, err := md.DeliverTo("x.test", "bob", folder, []byte(raw))
		if err != nil {
			t.Fatal(err)
		}
		if name != "unread" {
			if _, err := md.markSeenFolder("x.test", "bob", folder, id); err != nil {
				t.Fatal(err)
			}
		}
	}
	put("Inbox", "priya", "From: Priya <priya@h.test>\r\nTo: bob@x.test\r\nSubject: hello\r\nDate: Mon, 14 Sep 2026 10:00:00 +0000", "plain")
	put("Inbox", "dana", "From: Ops <ops@h.test>\r\nTo: dana@x.test\r\nSubject: hello\r\nDate: Mon, 14 Sep 2026 10:00:00 +0000", "plain")
	put("Inbox", "subject", "From: Ops <ops@h.test>\r\nTo: bob@x.test\r\nSubject: the migration window\r\nDate: Mon, 14 Sep 2026 10:00:00 +0000", "plain")
	put("Inbox", "early", "From: Ops <ops@h.test>\r\nTo: bob@x.test\r\nSubject: hello\r\nDate: Mon, 31 Aug 2026 23:00:00 +0000", "plain")
	put("Inbox", "late", "From: Ops <ops@h.test>\r\nTo: bob@x.test\r\nSubject: hello\r\nDate: Wed, 30 Sep 2026 00:00:00 +0000", "plain")
	put("Inbox", "attach", "From: Ops <ops@h.test>\r\nTo: bob@x.test\r\nSubject: hello\r\nDate: Mon, 14 Sep 2026 10:00:00 +0000\r\nMIME-Version: 1.0\r\nContent-Type: multipart/mixed; boundary=b",
		"--b\r\nContent-Type: text/plain\r\n\r\nx\r\n--b\r\nContent-Type: application/pdf\r\nContent-Disposition: attachment; filename=a.pdf\r\n\r\n%PDF\r\n--b--\r\n")
	put("Inbox", "unread", "From: Ops <ops@h.test>\r\nTo: bob@x.test\r\nSubject: hello\r\nDate: Mon, 14 Sep 2026 10:00:00 +0000", "plain")
	put("Sent", "sent", "From: bob@x.test\r\nTo: ops@h.test\r\nSubject: hello\r\nDate: Mon, 14 Sep 2026 10:00:00 +0000", "plain")
	put("Inbox", "words", "From: Ops <ops@h.test>\r\nTo: bob@x.test\r\nSubject: rotation\r\nDate: Mon, 14 Sep 2026 10:00:00 +0000", "the backup keys are in the vault")
	put("Inbox", "oneword", "From: Ops <ops@h.test>\r\nTo: bob@x.test\r\nSubject: rotation\r\nDate: Mon, 14 Sep 2026 10:00:00 +0000", "the backup is done")
	return md
}

func found(t *testing.T, md *Maildir, q string) []string {
	t.Helper()
	res, err := md.Search("x.test", "bob", ParseSearchQuery(q), 100)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, r := range res {
		names = append(names, r.MessageID[:len(r.MessageID)-2])
	}
	sort.Strings(names)
	return names
}

// Each term narrows the search to the one message it describes.
func TestEachSearchTermNarrows(t *testing.T) {
	md := searchBox(t)
	for q, want := range map[string][]string{
		"from:priya":                               {"priya"},
		"to:dana":                                  {"dana"},
		`subject:"migration window"`:               {"subject"},
		"hello before:2026-09-01":                  {"early"},
		"hello after:2026-09-30":                   {"late"},
		"hello before:2026-09-30 after:2026-09-29": nil,
		"has:attachment":                           {"attach"},
		"is:unread":                                {"unread"},
		"in:sent":                                  {"sent"},
		"backup vault":                             {"words"},
		`"backup keys"`:                            {"words"},
		"from:ops hello in:inbox is:unread":        {"unread"},
	} {
		if got := found(t, md, q); !reflect.DeepEqual(got, want) {
			t.Errorf("%q found %v, want %v", q, got, want)
		}
	}
}

// The scan bound counts only what the other terms let through to the words,
// so a narrow query finds what lies beyond it.
func TestNarrowingTermsReachPastTheScanBound(t *testing.T) {
	md := searchBox(t)
	old := searchMaxScan
	searchMaxScan = 2
	t.Cleanup(func() { searchMaxScan = old })
	if got := found(t, md, "backup subject:rotation"); len(got) != 2 {
		t.Fatalf("subject:rotation and a word, under a bound of 2, found %v, want both rotation messages", got)
	}
	if got := found(t, md, "o"); len(got) != 2 {
		t.Fatalf("a bound of 2 let %d through, want 2", len(got))
	}
}
