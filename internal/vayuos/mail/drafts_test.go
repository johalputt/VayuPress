// SPDX-License-Identifier: Apache-2.0

package mail

// drafts_test.go — a draft must keep what the composer showed.
//
// The failure this guards is silent: the composer displayed attachments, the
// draft stored none, and pressing Send from the reopened draft delivered a message
// without them. Nobody notices until the recipient says the file never arrived.

import (
	"bytes"
	"strings"
	"testing"
)

func TestADraftKeepsItsAttachments(t *testing.T) {
	t.Parallel()
	e := newLoopbackEngine(t, nil)
	pdf := []byte("%PDF-1.4\n%\xe2\xe3\xcf\xd3\nbinary\x00tail")
	note := []byte("note body\r\nsecond line")
	id, err := e.SaveDraftWithAttachments(`"Alice" <alice@example.com>`,
		[]string{"bob@example.com"}, nil, nil, "With files", "body text",
		[]Attachment{
			{Filename: "report.pdf", ContentType: "application/pdf", Data: pdf},
			{Filename: "notes.txt", ContentType: "text/plain", Data: note},
		})
	if err != nil || id == "" {
		t.Fatalf("save draft: id=%q err=%v", id, err)
	}
	rd := ReadAsSystem("alice", "test")

	// The draft is still an ordinary message in Drafts.
	msgs, err := e.ListFolder(rd, "Drafts")
	if err != nil || len(msgs) != 1 {
		t.Fatalf("drafts listing: %d messages, err %v", len(msgs), err)
	}
	if msgs[0].Subject != "With files" {
		t.Errorf("subject = %q", msgs[0].Subject)
	}

	// …and its files come back byte-for-byte.
	got, err := e.DraftAttachments(rd, id)
	if err != nil {
		t.Fatalf("DraftAttachments: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 attachments, got %d: %+v", len(got), got)
	}
	if got[0].Filename != "report.pdf" || got[0].ContentType != "application/pdf" {
		t.Errorf("first part = %q (%s)", got[0].Filename, got[0].ContentType)
	}
	if !bytes.Equal(got[0].Data, pdf) {
		t.Errorf("binary attachment did not survive the round trip (%d bytes in, %d out)", len(pdf), len(got[0].Data))
	}
	if got[1].Filename != "notes.txt" || !bytes.Equal(got[1].Data, note) {
		t.Errorf("second attachment = %q (%d bytes)", got[1].Filename, len(got[1].Data))
	}
}

func TestADraftWithoutAttachmentsCarriesNone(t *testing.T) {
	t.Parallel()
	e := newLoopbackEngine(t, nil)
	id, err := e.SaveDraft(`"Alice" <alice@example.com>`, []string{"bob@example.com"},
		nil, nil, "Plain", "no files here")
	if err != nil {
		t.Fatal(err)
	}
	got, err := e.DraftAttachments(ReadAsSystem("alice", "test"), id)
	if err != nil {
		t.Fatalf("DraftAttachments: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("a plain draft must report no attachments, got %d", len(got))
	}
	// And it stays a plain (non-MIME) message, exactly as before.
	raw, _ := e.ReadFolderMessage(ReadAsSystem("alice", "test"), "Drafts", id)
	if strings.Contains(string(raw), "multipart/mixed") {
		t.Error("a draft with no files must not grow MIME structure")
	}
}

func TestAHostileAttachmentFilenameCannotInjectHeaders(t *testing.T) {
	t.Parallel()
	e := newLoopbackEngine(t, nil)
	evil := "ok\r\nX-Injected: yes\r\n\r\nand-a-quote\".txt"
	id, err := e.SaveDraftWithAttachments(`"Alice" <alice@example.com>`,
		[]string{"bob@example.com"}, nil, nil, "Hostile name", "body",
		[]Attachment{{Filename: evil, ContentType: "text/plain", Data: []byte("payload")}})
	if err != nil {
		t.Fatal(err)
	}
	rd := ReadAsSystem("alice", "test")
	raw, err := e.ReadFolderMessage(rd, "Drafts", id)
	if err != nil {
		t.Fatal(err)
	}
	// The danger is a REAL header line: the CRLF must be neutralised so the
	// injected text stays inside the quoted filename, where a parser reads it as
	// part of the name. (The name itself may still contain the words — that is
	// harmless — so this asserts the line structure, not a substring.)
	if strings.Contains(string(raw), "\r\nX-Injected:") || strings.Contains(string(raw), "\nX-Injected:") {
		t.Errorf("a filename injected a header line into the stored draft:\n%s", raw)
	}
	got, _ := e.DraftAttachments(rd, id)
	if len(got) != 1 {
		t.Fatalf("the hostile filename should still round-trip as one attachment, got %d", len(got))
	}
	if strings.ContainsAny(got[0].Filename, "\r\n\"\\") {
		t.Errorf("stored filename still carries characters that break a MIME parameter: %q", got[0].Filename)
	}
	if !strings.HasSuffix(got[0].Filename, ".txt") {
		t.Errorf("sanitising must not lose the whole name: %q", got[0].Filename)
	}
	if !bytes.Equal(got[0].Data, []byte("payload")) {
		t.Error("the attachment's bytes must survive regardless of its name")
	}
}

func TestAnEmptyAttachmentIsNotStoredAsAPart(t *testing.T) {
	t.Parallel()
	e := newLoopbackEngine(t, nil)
	id, err := e.SaveDraftWithAttachments(`"Alice" <alice@example.com>`,
		[]string{"bob@example.com"}, nil, nil, "Zero byte", "body",
		[]Attachment{{Filename: "empty.bin", ContentType: "application/octet-stream"}})
	if err != nil {
		t.Fatal(err)
	}
	got, _ := e.DraftAttachments(ReadAsSystem("alice", "test"), id)
	if len(got) != 0 {
		t.Errorf("a zero-byte attachment should not become a MIME part, got %d", len(got))
	}
}
