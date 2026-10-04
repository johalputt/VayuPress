// SPDX-License-Identifier: Apache-2.0

package mail

import (
	"archive/zip"
	"bufio"
	"bytes"
	"context"
	"io"
	"net/mail"
	"strings"
	"time"
)

// A mailbox's own download: every folder as an mbox file inside one zip, the
// format other mail apps import (Thunderbird, Apple Mail, mutt; Gmail through
// them). mboxrd is used, the variant whose quoting can be undone exactly: a
// body line starting with any number of '>' then "From " gains one more '>'.
// Messages are written as the reader shows them, decrypted where this
// install holds the key, since mail you cannot read is not mail you have
// taken with you.

// ArchiveMailbox writes rd's mailbox to w as a zip of mbox files, one per
// folder, and returns how many messages it wrote. It stops between messages
// when ctx ends.
func (e *Engine) ArchiveMailbox(ctx context.Context, rd Reader, w io.Writer) (int, error) {
	folders, err := e.FoldersFor(rd)
	if err != nil {
		return 0, err
	}
	z := zip.NewWriter(w)
	n := 0
	for _, f := range folders {
		msgs, err := e.ListFolder(rd, f)
		if err != nil {
			return n, err
		}
		if len(msgs) == 0 {
			continue
		}
		fw, err := z.CreateHeader(&zip.FileHeader{Name: f + ".mbox", Method: zip.Deflate, Modified: time.Now()})
		if err != nil {
			return n, err
		}
		bw := bufio.NewWriter(fw)
		// Oldest first, as an mbox reads.
		for i := len(msgs) - 1; i >= 0; i-- {
			if err := ctx.Err(); err != nil {
				return n, err
			}
			raw, err := e.ReadFolderMessage(rd, f, msgs[i].ID)
			if err != nil {
				continue // gone since it was listed
			}
			if err := writeMboxMessage(bw, raw, msgs[i].Date); err != nil {
				return n, err
			}
			n++
		}
		if err := bw.Flush(); err != nil {
			return n, err
		}
	}
	return n, z.Close()
}

// writeMboxMessage writes one message as an mboxrd entry: a From_ line naming
// the sender and date, the message with LF line ends and its From lines
// quoted, and the blank line that ends an entry.
func writeMboxMessage(w *bufio.Writer, raw []byte, date time.Time) error {
	sender := "MAILER-DAEMON"
	if m, err := mail.ReadMessage(bytes.NewReader(raw)); err == nil {
		if a, err := mail.ParseAddress(m.Header.Get("From")); err == nil && a.Address != "" && !strings.ContainsAny(a.Address, " \t") {
			sender = a.Address
		}
	}
	if date.IsZero() {
		date = time.Unix(0, 0)
	}
	if _, err := w.WriteString("From " + sender + " " + date.UTC().Format(time.ANSIC) + "\n"); err != nil {
		return err
	}
	text := strings.ReplaceAll(string(raw), "\r\n", "\n")
	text = strings.TrimSuffix(text, "\n")
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(strings.TrimLeft(line, ">"), "From ") {
			line = ">" + line
		}
		if _, err := w.WriteString(line + "\n"); err != nil {
			return err
		}
	}
	_, err := w.WriteString("\n")
	return err
}
