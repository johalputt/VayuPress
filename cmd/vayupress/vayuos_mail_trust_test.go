// SPDX-License-Identifier: Apache-2.0

package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	vmail "github.com/johalputt/vayupress/internal/vayuos/mail"
)

// What the trust line reads from a message: one seed per rule, and a near
// miss beside each that must stay quiet.
func TestWhatTheTrustLineReads(t *testing.T) {
	for _, c := range []struct {
		name, html, from string
		stranger         bool
		trackers         int
		warning          string
	}{
		{"a 1-pixel remote picture", `<p>Hi</p><img src="https://t.example/o.gif" width="1" height="1"><img src="https://cdn.example/logo.png" width="120">`, "a@shop.example", false, 1, ""},
		{"a tiny picture carried in the message", `<img src="data:image/gif;base64,R0lGOD" width="1" height="1">`, "a@shop.example", false, 0, ""},
		{"a link whose words name another site", `<a href="https://paypa1.com/login">paypal.com</a>`, "a@shop.example", false, 0, "A link says paypal.com but goes to paypa1.com."},
		{"a link to another page of the same site", `<a href="https://www.paypal.com/x?y=1">paypal.com</a>`, "a@shop.example", false, 0, ""},
		{"a link whose words are not an address", `<a href="https://elsewhere.example/">Login</a>`, "a@shop.example", false, 0, ""},
		{"a name that is another site's address", "", `"support@bank.example" <x@evil.example>`, false, 0, "The name says support@bank.example but the mail comes from x@evil.example."},
		{"a name that is the sender's own site", "", `"help@bank.example" <noreply@mail.bank.example>`, false, 0, ""},
		{"a stranger's pretending link", `<a href="https://paypa1.com/">https://paypal.com/</a>`, "a@shop.example", true, 0, "This sender is not in your contacts. A link says paypal.com but goes to paypa1.com."},
	} {
		got := mailTrustFor("", mailHTMLPolicyImages.Sanitize(c.html), c.from, c.stranger)
		if got.Trackers != c.trackers || got.Warning != c.warning {
			t.Errorf("%s: %d trackers, warning %q; want %d, %q", c.name, got.Trackers, got.Warning, c.trackers, c.warning)
		}
	}
	// A tracker is never fetched, even with pictures on; the picture beside it is.
	out := proxyImageSources(mailHTMLPolicyImages.Sanitize(`<img src="https://t.example/o.gif" width="1" height="1"><img src="https://cdn.example/logo.png">`))
	if strings.Contains(out, "t.example") || !strings.Contains(out, "cdn.example") {
		t.Errorf("with pictures on, the tracker is fetched or the picture is not:\n%s", out)
	}
}

// The reader says a domain confirmed a message only from this install's
// seal, warns on a pretending link from a stranger, and warns no one about
// mail the mailbox itself sent.
func TestTheReadersTrustLine(t *testing.T) {
	a, holder, _ := reviewerApp(t, vmail.RoleMailbox)
	key := []byte("test verdict key")
	a.vayuMail.UseVerdictKey(key)
	rd := a.mailReader(withUser(httptest.NewRequest(http.MethodGet, "/", nil), holder), "")
	body := "From: Shop <news@shop.example>\r\nTo: dana@example.com\r\nSubject: Sealed\r\nContent-Type: text/html\r\n\r\n" +
		`<p>Your order</p><a href="https://paypa1.com/">paypal.com</a><img src="https://t.example/o.gif" width="1" height="1">` + "\r\n"
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte("shop.example\x00" + body))
	sealed := "X-VayuMail-Checked: shop.example; mac=" + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)) + "\r\n" + body
	for _, raw := range []string{sealed, strings.Replace(sealed, "Subject: Sealed", "Subject: Forged", 1)} {
		if _, err := a.vayuMail.DeliverInbound("news@shop.example", "dana@example.com", []byte(raw)); err != nil {
			t.Fatal(err)
		}
	}
	msgs, _ := a.vayuMail.ListFolder(rd, "Inbox")
	card := func(subject string) string {
		for _, m := range msgs {
			if m.Subject == subject {
				c, _ := a.vayuReaderCard(rd, "Inbox", m.ID, readerView{Pane: true, HTML: true})
				return c
			}
		}
		t.Fatalf("%s is not listed", subject)
		return ""
	}
	sealedCard := card("Sealed")
	for _, want := range []string{"shop.example confirmed it sent this", "1 tracker blocked",
		`<p class="mx-trust__warn" role="alert">`, "This sender is not in your contacts. A link says paypal.com but goes to paypa1.com."} {
		if !strings.Contains(sealedCard, want) {
			t.Errorf("the sealed message's reader lacks %q", want)
		}
	}
	if strings.Contains(card("Forged"), "confirmed it sent this") {
		t.Error("a message altered after its seal reads as confirmed")
	}
	// The same message in Sent is the mailbox's own: no warning.
	for _, m := range msgs {
		if m.Subject == "Forged" {
			if err := a.vayuMail.MoveMessage(rd, m.ID, "Inbox", "Sent"); err != nil {
				t.Fatal(err)
			}
		}
	}
	sent, _ := a.vayuMail.ListFolder(rd, "Sent")
	if len(sent) != 1 {
		t.Fatalf("Sent holds %d messages, want the one moved there", len(sent))
	}
	if c, _ := a.vayuReaderCard(rd, "Sent", sent[0].ID, readerView{Pane: true, HTML: true}); strings.Contains(c, "mx-trust__warn") || !strings.Contains(c, "1 tracker blocked") {
		t.Error("a message in Sent warns, or its trackers are not counted")
	}
}

// A link loses its tracking parameters whichever way the message is shown,
// keeps every other parameter, and a link with none is left exactly as it
// was written.
func TestLinksLoseTheirTrackingParameters(t *testing.T) {
	in := `<a href="https://shop.example/p?id=7&amp;utm_source=news&amp;fbclid=x">Shop</a> <a href="https://a.example/?b=1&amp;a=2">A</a> <a href="mailto:x@y.example?utm_source=a&amp;subject=Hi">M</a>`
	for name, out := range map[string]string{
		"pictures off": mailHTMLNoImages(in),
		"pictures on":  proxyImageSources(mailHTMLPolicyImages.Sanitize(in)),
	} {
		if !strings.Contains(out, `href="https://shop.example/p?id=7"`) || strings.Contains(out, "utm_source=news") || strings.Contains(out, "fbclid") {
			t.Errorf("%s: the tracking parameters stay:\n%s", name, out)
		}
		if !strings.Contains(out, `href="https://a.example/?b=1&amp;a=2"`) {
			t.Errorf("%s: a link with no tracking was rewritten:\n%s", name, out)
		}
		if !strings.Contains(out, `href="mailto:x@y.example?utm_source=a&amp;subject=Hi"`) {
			t.Errorf("%s: a mail link was rewritten:\n%s", name, out)
		}
	}
}
