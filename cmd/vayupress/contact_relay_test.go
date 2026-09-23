// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bufio"
	"context"
	"encoding/base64"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/mail"
	"strings"
	"sync"
	"testing"

	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/email"
	"github.com/johalputt/vayupress/internal/settings"
)

// smtpSink is an SMTP server on loopback that accepts every message and keeps
// it, keyed by recipient: the relay the install's mailer talks to.
func smtpSink(t *testing.T) (port int, sent func() map[string]string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	var mu sync.Mutex
	got := map[string]string{}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				r, w := bufio.NewReader(c), bufio.NewWriter(c)
				say := func(s string) { _, _ = w.WriteString(s + "\r\n"); _ = w.Flush() }
				say("220 sink")
				var rcpt string
				for {
					line, err := r.ReadString('\n')
					if err != nil {
						return
					}
					switch cmd := strings.ToUpper(strings.TrimSpace(line)); {
					case strings.HasPrefix(cmd, "EHLO"), strings.HasPrefix(cmd, "HELO"):
						say("250 sink")
					case strings.HasPrefix(cmd, "RCPT TO:"):
						rcpt = strings.Trim(strings.TrimSpace(line)[8:], "<> ")
						say("250 ok")
					case cmd == "DATA":
						say("354 go")
						var body strings.Builder
						for {
							l, err := r.ReadString('\n')
							if err != nil || l == ".\r\n" {
								break
							}
							body.WriteString(l)
						}
						mu.Lock()
						got[rcpt] = mailText(body.String())
						mu.Unlock()
						say("250 kept")
					case cmd == "QUIT":
						say("221 bye")
						return
					default:
						say("250 ok")
					}
				}
			}(c)
		}
	}()
	return ln.Addr().(*net.TCPAddr).Port, func() map[string]string {
		mu.Lock()
		defer mu.Unlock()
		out := map[string]string{}
		for k, v := range got {
			out[k] = v
		}
		return out
	}
}

// mailText is the readable text of a single-part message: its body, decoded
// when the mailer sent it base64.
func mailText(raw string) string {
	m, err := mail.ReadMessage(strings.NewReader(raw))
	if err != nil {
		return raw
	}
	body, _ := io.ReadAll(m.Body)
	if strings.EqualFold(m.Header.Get("Content-Transfer-Encoding"), "base64") {
		if dec, err := base64.StdEncoding.DecodeString(strings.Join(strings.Fields(string(body)), "")); err == nil {
			body = dec
		}
	}
	return m.Header.Get("Subject") + "\n" + string(body)
}

// Anyone can type anyone's address into a contact form, and the auto-reply
// goes to it unconfirmed. If that reply carries what the visitor wrote, the
// form is an open relay: any text, to any address, sent under the install's
// own mail identity — five a minute from each address the sender controls, and
// the operator's sending reputation pays for it. The operator's copy carries
// the message; the reply to the typed address carries none of it.
func TestTheAutoReplyCarriesNothingTheVisitorWrote(t *testing.T) {
	openMigratedDB(t)
	port, sent := smtpSink(t)
	a := &App{siteSettings: settings.New(dbpkg.DB),
		mailer: email.New(email.Config{Host: "127.0.0.1", Port: port, TLS: email.TLSNone, From: "site@install.example"})}
	if err := a.siteSettings.SetMany(context.Background(), settings.ForPrimary(),
		map[string]string{settings.KeyContactEmail: "owner@install.example"}); err != nil {
		t.Fatal(err)
	}
	pitch := "Your account is suspended. Restore it at https://evil.example/login"
	req := httptest.NewRequest(http.MethodPost, "/api/v1/contact", strings.NewReader(
		`{"name":"Security Team https://evil.example","email":"victim@elsewhere.example","message":"`+pitch+`"}`))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "203.0.113.9:4000"
	rec := httptest.NewRecorder()
	a.handleContactSubmit(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("submit: %d %s", rec.Code, rec.Body)
	}
	mail := sent()
	if !strings.Contains(mail["owner@install.example"], pitch) {
		t.Fatalf("the operator's copy does not carry the message; this test is looking at the wrong mail: %v", mail)
	}
	reply, ok := mail["victim@elsewhere.example"]
	if !ok {
		t.Fatalf("no auto-reply was sent; this test proves nothing: %v", mail)
	}
	for _, words := range []string{"evil.example", "suspended", "Security Team"} {
		if strings.Contains(reply, words) {
			t.Errorf("the reply to an unconfirmed address carries the visitor's %q:\n%s", words, reply)
		}
	}

	// The Host header is the sender's too: an unknown host is answered as the
	// primary site, so naming "the site" from it puts their words back in.
	if err := a.siteSettings.SetMany(context.Background(), settings.ForPrimary(),
		map[string]string{settings.KeySiteName: " "}); err != nil {
		t.Fatal(err)
	}
	req = httptest.NewRequest(http.MethodPost, "/api/v1/contact", strings.NewReader(
		`{"name":"A","email":"second@elsewhere.example","message":"hello"}`))
	req.Host = "restore-your-account.example"
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "203.0.113.10:4000"
	a.handleContactSubmit(httptest.NewRecorder(), req)
	reply, ok = sent()["second@elsewhere.example"]
	if !ok || strings.Contains(reply, "restore-your-account") {
		t.Errorf("the reply names the site from the sender's Host header (sent=%v):\n%s", ok, reply)
	}
}
