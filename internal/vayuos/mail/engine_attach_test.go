package mail

import (
	"bytes"
	"encoding/base64"
	"strings"
	"testing"
)

func TestWriteAttachmentPart(t *testing.T) {
	var buf bytes.Buffer
	data := []byte("hello attachment contents — with unicode ✓ and length > a few bytes")
	writeAttachmentPart(&buf, "BOUND", Attachment{Filename: "note.txt", ContentType: "text/plain", Data: data})
	s := buf.String()
	if !strings.HasPrefix(s, "--BOUND\r\n") {
		t.Error("part must start with the boundary")
	}
	if !strings.Contains(s, `Content-Disposition: attachment; filename="note.txt"`) {
		t.Error("missing attachment disposition")
	}
	if !strings.Contains(s, "Content-Transfer-Encoding: base64") {
		t.Error("missing base64 transfer encoding")
	}
	// The base64 payload (after the blank line) decodes back to the original.
	idx := strings.Index(s, "\r\n\r\n")
	if idx < 0 {
		t.Fatal("no header/body separator")
	}
	b64 := strings.ReplaceAll(s[idx+4:], "\r\n", "")
	dec, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		t.Fatalf("payload is not valid base64: %v", err)
	}
	if string(dec) != string(data) {
		t.Errorf("round-trip mismatch: got %q", dec)
	}
}

func TestMimeSanitizeFilename(t *testing.T) {
	cases := map[string]string{
		`a"b`:     "ab",
		"a\r\nb":  "ab",
		"   ":     "attachment",
		"ok.pdf":  "ok.pdf",
		`x\y.zip`: "xy.zip",
	}
	for in, want := range cases {
		if got := mimeSanitizeFilename(in); got != want {
			t.Errorf("mimeSanitizeFilename(%q) = %q, want %q", in, got, want)
		}
	}
}

// A default (empty) content type must fall back to application/octet-stream.
func TestWriteAttachmentPartDefaultCT(t *testing.T) {
	var buf bytes.Buffer
	writeAttachmentPart(&buf, "B", Attachment{Filename: "x.bin", Data: []byte{1, 2, 3}})
	if !strings.Contains(buf.String(), "Content-Type: application/octet-stream") {
		t.Error("empty content type should default to application/octet-stream")
	}
}
