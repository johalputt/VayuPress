// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/safefetch"
	vmail "github.com/johalputt/vayupress/internal/vayuos/mail"
)

var pngBytes = []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x06\x00\x00\x00\x1f\x15\xc4\x89")

// Every remote picture goes through the proxy, signed; a data: picture
// stays; anything else, and srcset, is dropped.
func TestPicturesAreRewrittenToTheProxy(t *testing.T) {
	out := proxyImageSources(`<img src="https://tracker.test/p.gif?id=7" srcset="https://cdn.test/a.png 2x" alt="a">` +
		`<img src="data:image/png;base64,AAAA" alt="b"><img src="cid:part1" alt="c"><img src="javascript:x" alt="d">`)
	want := "/os/vayumail/picture?u=" + url.QueryEscape("https://tracker.test/p.gif?id=7") + "&amp;s=" + mailPictureSig("https://tracker.test/p.gif?id=7")
	if !strings.Contains(out, want) {
		t.Fatalf("the remote picture is not proxied, signed:\n%s", out)
	}
	for _, gone := range []string{"https://cdn.test", "srcset", "cid:", "javascript:", `src="https://`} {
		if strings.Contains(out, gone) {
			t.Errorf("%q survived:\n%s", gone, out)
		}
	}
	if !strings.Contains(out, `src="data:image/png;base64,AAAA"`) {
		t.Errorf("the data: picture was dropped:\n%s", out)
	}
}

func picture(a *App, u, sig string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	a.handleVayuOSMailPicture(rec, httptest.NewRequest(http.MethodGet, "/os/vayumail/picture?u="+url.QueryEscape(u)+"&s="+sig, nil))
	return rec
}

// The proxy fetches only what it signed, and serves only a picture, typed by
// its bytes; one seed per refusal.
func TestThePictureProxyServesOnlySignedPictures(t *testing.T) {
	a := &App{}
	fetched := 0
	body := pngBytes
	old := mailPictureFetch
	mailPictureFetch = func(_ context.Context, _ string) (*safefetch.Result, error) {
		fetched++
		return &safefetch.Result{Status: 200, Body: body, ContentType: "image/svg+xml"}, nil
	}
	t.Cleanup(func() { mailPictureFetch = old })
	const u = "https://img.test/a.png"

	if rec := picture(a, u, "bad"); rec.Code != http.StatusNotFound || fetched != 0 {
		t.Fatalf("an unsigned address: %d, fetched %d", rec.Code, fetched)
	}
	rec := picture(a, u, mailPictureSig(u))
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "image/png" || !strings.Contains(rec.Header().Get("Content-Security-Policy"), "sandbox") {
		t.Fatalf("a signed PNG: %d %q %q", rec.Code, rec.Header().Get("Content-Type"), rec.Header().Get("Content-Security-Policy"))
	}
	body = pngBytes
	status := 404
	mailPictureFetch = func(_ context.Context, _ string) (*safefetch.Result, error) {
		return &safefetch.Result{Status: status, Body: body}, nil
	}
	if rec := picture(a, u, mailPictureSig(u)); rec.Code != http.StatusNotFound {
		t.Fatalf("a picture from a 404 answer served: %d", rec.Code)
	}
	status = 200
	body = []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`)
	if rec := picture(a, u, mailPictureSig(u)); rec.Code != http.StatusNotFound {
		t.Fatalf("SVG served: %d", rec.Code)
	}
	body = []byte(`<html><script>alert(1)</script></html>`)
	if rec := picture(a, u, mailPictureSig(u)); rec.Code != http.StatusNotFound {
		t.Fatalf("HTML served: %d", rec.Code)
	}
	mailPictureFetch = func(context.Context, string) (*safefetch.Result, error) { return nil, errors.New("blocked") }
	if rec := picture(a, u, mailPictureSig(u)); rec.Code != http.StatusNotFound {
		t.Fatalf("a refused fetch: %d", rec.Code)
	}
}

// withAttachments puts a message in dana's inbox with a picture, a PDF and an
// HTML file that calls itself a picture, and returns its id.
func withAttachments(t *testing.T, a *App) string {
	t.Helper()
	part := func(name, ctype string, data []byte) string {
		return "--b\r\nContent-Type: " + ctype + "; name=\"" + name + "\"\r\nContent-Disposition: attachment; filename=\"" + name + "\"\r\n" +
			"Content-Transfer-Encoding: base64\r\n\r\n" + base64.StdEncoding.EncodeToString(data) + "\r\n"
	}
	raw := "From: x@example.net\r\nTo: dana@example.com\r\nSubject: Files\r\nMIME-Version: 1.0\r\nContent-Type: multipart/mixed; boundary=\"b\"\r\n\r\n" +
		"--b\r\nContent-Type: text/plain\r\n\r\nsee attached\r\n" +
		part("photo.png", "image/png", pngBytes) + part("doc.pdf", "application/pdf", []byte("%PDF-1.4\n%fake\n")) +
		part("trap.png", "image/png", []byte("<html><script>alert(1)</script></html>")) + "--b--\r\n"
	id, err := a.vayuMail.DeliverInbound("x@example.net", "dana@example.com", []byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func attachment(a *App, id string, idx int) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	a.handleVayuOSAttachment(rec, withUser(httptest.NewRequest(http.MethodGet, "/os/vayumail/attachment?folder=Inbox&id="+url.QueryEscape(id)+"&idx="+itoaSafe(idx)+"&inline=1", nil), danaHolder()))
	return rec
}

// An attachment opens in place only when its bytes are a picture or a PDF;
// one that only says it is a picture downloads.
func TestAnAttachmentOpensInPlaceByItsBytes(t *testing.T) {
	a := scheduledApp(t)
	id := withAttachments(t, a)
	rd := danaReader(a)
	card, _ := a.vayuReaderCard(rd, "Inbox", id, readerView{Pane: true})
	if !strings.Contains(card, `class="mx-previews"`) || strings.Count(card, ">Open<") != 3 {
		t.Fatalf("the reader offers no previews, or not Open on each previewable file:\n%s", card)
	}
	var idx []int
	for i := 0; i < 8 && len(idx) < 3; i++ {
		if rec := attachment(a, id, i); rec.Code == http.StatusOK {
			idx = append(idx, i)
		}
	}
	if len(idx) != 3 {
		t.Fatalf("found attachments %v", idx)
	}
	png, pdf, trap := attachment(a, id, idx[0]), attachment(a, id, idx[1]), attachment(a, id, idx[2])
	if !strings.HasPrefix(png.Header().Get("Content-Disposition"), "inline") || png.Header().Get("Content-Type") != "image/png" || !strings.Contains(png.Header().Get("Content-Security-Policy"), "sandbox") {
		t.Errorf("the picture: %q %q %q", png.Header().Get("Content-Disposition"), png.Header().Get("Content-Type"), png.Header().Get("Content-Security-Policy"))
	}
	// No sandbox for a PDF: the browser's own viewer will not run under one.
	if !strings.HasPrefix(pdf.Header().Get("Content-Disposition"), "inline") || pdf.Header().Get("Content-Type") != "application/pdf" || pdf.Header().Get("Content-Security-Policy") != "" {
		t.Errorf("the PDF: %q %q %q", pdf.Header().Get("Content-Disposition"), pdf.Header().Get("Content-Type"), pdf.Header().Get("Content-Security-Policy"))
	}
	if !strings.HasPrefix(trap.Header().Get("Content-Disposition"), "attachment") {
		t.Errorf("HTML calling itself a picture opened in place: %q", trap.Header().Get("Content-Disposition"))
	}
}

const listMail = "From: News <news@list.example>\r\nTo: dana@example.com\r\nSubject: Weekly\r\n" +
	"List-Unsubscribe: <https://list.example/u/1>\r\n%s\r\nhello\r\n"

// Mail from a list says so, with the way off it: from here when it offers
// one-click (its page beside, in case the request is refused as unsigned),
// its page when it offers only that, and nothing to a read-only mailbox.
func TestTheReaderOffersUnsubscribe(t *testing.T) {
	for _, c := range []struct {
		post, want string
	}{
		{"List-Unsubscribe-Post: List-Unsubscribe=One-Click\r\n", `hx-post="/os/vayumail/unsubscribe"`},
		{"List-Unsubscribe-Post: List-Unsubscribe=One-Click\r\n", `href="https://list.example/u/1" target="_blank" rel="noopener noreferrer">or on list.example<`},
		{"", `>Unsubscribe on list.example<`},
	} {
		a := scheduledApp(t)
		id, err := a.vayuMail.DeliverInbound("news@list.example", "dana@example.com", []byte(strings.Replace(listMail, "%s", c.post, 1)))
		if err != nil {
			t.Fatal(err)
		}
		card, _ := a.vayuReaderCard(danaReader(a), "Inbox", id, readerView{Pane: true})
		if !strings.Contains(card, c.want) {
			t.Errorf("post %q: the reader lacks %s", c.post, c.want)
		}
	}
	a, holder, _ := reviewerApp(t, vmail.RoleReviewer)
	id, _ := a.vayuMail.DeliverInbound("news@list.example", "dana@example.com", []byte(strings.Replace(listMail, "%s", "", 1)))
	rd := a.mailReader(withUser(httptest.NewRequest(http.MethodGet, "/", nil), holder), "")
	if card, _ := a.vayuReaderCard(rd, "Inbox", id, readerView{Pane: true}); strings.Contains(card, "mx-unsub") {
		t.Error("a read-only mailbox is offered Unsubscribe")
	}
}

func unsubscribe(a *App, vals url.Values) *httptest.ResponseRecorder {
	req := withUser(httptest.NewRequest(http.MethodPost, "/os/vayumail/unsubscribe", strings.NewReader(vals.Encode())), danaHolder())
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	a.handleVayuOSUnsubscribe(rec, req)
	return rec
}

// Unsubscribe says what it did in a toast, and reaches only the holder's own
// mail: naming another mailbox's message sends nothing.
func TestUnsubscribeAnswersForTheHoldersOwnMail(t *testing.T) {
	a := scheduledApp(t)
	viaMail := "From: News <news@list.example>\r\nSubject: Weekly\r\nList-Unsubscribe: <mailto:erin@example.com?subject=stop>\r\n\r\nhello\r\n"
	erins, err := a.vayuMail.DeliverInbound("news@list.example", "erin@example.com", []byte(viaMail))
	if err != nil {
		t.Fatal(err)
	}
	rec := unsubscribe(a, url.Values{"user": {"erin@example.com"}, "folder": {"Inbox"}, "id": {erins}})
	if !strings.Contains(rec.Header().Get("HX-Trigger"), "Not unsubscribed") || !strings.Contains(rec.Header().Get("HX-Trigger"), `"warn":true`) {
		t.Fatalf("dana naming erin's message: %q", rec.Header().Get("HX-Trigger"))
	}
	danas, err := a.vayuMail.DeliverInbound("news@list.example", "dana@example.com", []byte(viaMail))
	if err != nil {
		t.Fatal(err)
	}
	rec = unsubscribe(a, url.Values{"folder": {"Inbox"}, "id": {danas}})
	if rec.Code != http.StatusNoContent || !strings.Contains(rec.Header().Get("HX-Trigger"), "a request to leave was sent to erin@example.com") {
		t.Fatalf("dana's own: %d %q", rec.Code, rec.Header().Get("HX-Trigger"))
	}
}
