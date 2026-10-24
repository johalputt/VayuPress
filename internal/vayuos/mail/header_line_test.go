// SPDX-License-Identifier: Apache-2.0

package mail

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A Subject is written to the wire as a header may be: a line break in it is
// refused, since it would end the Subject and start a header the sender
// chose (a Bcc, a second From), and one that is not plain ASCII is
// RFC 2047-encoded, since raw UTF-8 is not allowed in a header. The raw
// message on disk is read, because a mail client decodes the Subject and
// would show the same text either way.
func TestASubjectIsWrittenAsAHeaderMayBe(t *testing.T) {
	t.Parallel()
	e := newLoopbackEngine(t, loopbackBridge{localSet: map[string]bool{"bob@example.com": true}})
	ctx := context.Background()

	if _, err := e.SendMail(ctx, "alice@example.com", []string{"bob@example.com"},
		"Hello\r\nBcc: eve@attacker.test", "", "body", ""); err == nil || !strings.Contains(err.Error(), "line break") {
		t.Fatalf("a Subject holding a line break was sent (err %v)", err)
	}

	if _, err := e.SendMail(ctx, "alice@example.com", []string{"bob@example.com"},
		"Grüße aus Köln", "", "body", ""); err != nil {
		t.Fatal(err)
	}
	var raw []string
	_ = filepath.WalkDir(e.cfg.StorageDir, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			if b, rerr := os.ReadFile(p); rerr == nil && strings.Contains(string(b), "\r\nSubject: ") {
				raw = append(raw, string(b))
			}
		}
		return nil
	})
	if len(raw) != 1 {
		t.Fatalf("found %d delivered messages, want the one that was sent", len(raw))
	}
	if !strings.Contains(raw[0], "\r\nSubject: =?utf-8?q?Gr=C3=BC=C3=9Fe_aus_K=C3=B6ln?=\r\n") {
		head, _, _ := strings.Cut(raw[0], "\r\n\r\n")
		t.Errorf("the Subject is not RFC 2047-encoded:\n%s", head)
	}
}
