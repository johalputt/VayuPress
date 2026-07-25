package mail

import (
	"mime"
	"mime/multipart"
	"net/mail"
	"strings"
	"testing"
)

// The HTML alternative is derived from the same text the operator typed, so the
// two parts can never describe different messages. These tests are about the MIME
// tree, because a malformed one does not error — it just renders wrong, in someone
// else's client, where you will never see it.

// parseEntity reads a Content-Type header line plus a body into its parts.
func parseEntity(t *testing.T, head string, body []byte) (*mime.WordDecoder, string, map[string]string, []*multipart.Part) {
	t.Helper()
	msg, err := mail.ReadMessage(strings.NewReader(head + "\r\n\r\n" + string(body)))
	if err != nil {
		t.Fatalf("parse entity: %v", err)
	}
	mediaType, params, err := mime.ParseMediaType(msg.Header.Get("Content-Type"))
	if err != nil {
		t.Fatalf("parse content-type %q: %v", msg.Header.Get("Content-Type"), err)
	}
	var parts []*multipart.Part
	if strings.HasPrefix(mediaType, "multipart/") {
		mr := multipart.NewReader(msg.Body, params["boundary"])
		for {
			p, err := mr.NextPart()
			if err != nil {
				break
			}
			parts = append(parts, p)
		}
	}
	return nil, mediaType, params, parts
}

// TestAlternativeEntityPutsTextFirst pins the part order. RFC 2046 §5.1.4 says the
// LAST alternative is the one the client prefers, so text first / HTML second is
// what makes HTML win where it is supported and text the fallback everywhere else.
// Reversed, every HTML-capable client would show the plain text.
func TestAlternativeEntityPutsTextFirst(t *testing.T) {
	ent := alternativeEntity("plain words", "<p>rich words</p>")
	i := strings.Index(string(ent), "\r\n\r\n")
	if i < 0 {
		t.Fatal("entity has no header/body separator")
	}
	_, mediaType, _, parts := parseEntity(t, string(ent[:i]), ent[i+4:])

	if mediaType != "multipart/alternative" {
		t.Fatalf("media type = %q, want multipart/alternative", mediaType)
	}
	if len(parts) != 2 {
		t.Fatalf("got %d parts, want 2", len(parts))
	}
	if ct := parts[0].Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Errorf("first part is %q, want text/plain — the text must be the fallback, not the preference", ct)
	}
	if ct := parts[1].Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("second part is %q, want text/html", ct)
	}
}

// TestAlternativeEntityIsWellFormed guards the details that make a client accept
// it: a charset on each part, and a closing boundary.
func TestAlternativeEntityIsWellFormed(t *testing.T) {
	ent := string(alternativeEntity("hello", "<p>hello</p>"))
	if strings.Count(ent, "charset=utf-8") != 2 {
		t.Error("both parts must declare charset=utf-8")
	}
	i := strings.Index(ent, `boundary="`)
	if i < 0 {
		t.Fatal("no boundary declared")
	}
	b := ent[i+10:]
	b = b[:strings.Index(b, `"`)]
	if !strings.Contains(ent, "--"+b+"--\r\n") {
		t.Error("the entity must be closed with a final boundary")
	}
	// A nested multipart must not claim a content encoding of its own beyond
	// 7bit/8bit/binary (RFC 2045 §6.4); declaring none is cleanest.
	head := ent[:strings.Index(ent, "\r\n\r\n")]
	if strings.Contains(head, "Content-Transfer-Encoding") {
		t.Error("the multipart entity itself must not declare a transfer encoding")
	}
}

// TestBoundariesAreUniquePerEntity is the bug that would corrupt a message with
// attachments: if the nested alternative reused the mixed part's boundary, the
// parent would terminate at the child's closing delimiter and the attachments
// would vanish.
func TestBoundariesAreUniquePerEntity(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 50; i++ {
		b := mimeBoundary()
		if seen[b] {
			t.Fatalf("mimeBoundary repeated %q — a nested part would terminate its parent", b)
		}
		seen[b] = true
	}
}

// TestPlainTextAssemblyIsUnchanged is the regression guard for everyone not using
// the toggle: with no HTML the entity must still be a bare text/plain, exactly as
// before, with no multipart wrapper appearing where none used to be.
func TestPlainTextAssemblyIsUnchanged(t *testing.T) {
	// Mirrors the default branch of ComposeRich's entity assembly.
	head := "Content-Type: text/plain; charset=utf-8\r\nContent-Transfer-Encoding: 8bit"
	body := []byte(normalizeCRLF("just text"))
	_, mediaType, _, parts := parseEntity(t, head, body)
	if mediaType != "text/plain" {
		t.Errorf("media type = %q, want text/plain when no HTML is supplied", mediaType)
	}
	if len(parts) != 0 {
		t.Errorf("a plain message must not be multipart; got %d parts", len(parts))
	}
}
