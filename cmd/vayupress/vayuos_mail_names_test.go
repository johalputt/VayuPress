// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/base64"
	"net/http"
	"strings"
	"testing"
)

// TestAnAttachmentIsShownAndSavedUnderTheNameItHas — a file sent as
// "invoice<right-to-left override>fdp.exe", which is drawn as
// "invoiceexe.pdf", is shown in the reader and downloaded as
// "invoice_fdp.exe": the override reaches neither the page (the card, the
// picture's description, Open and Save to Drive) nor the computer.
func TestAnAttachmentIsShownAndSavedUnderTheNameItHas(t *testing.T) {
	a := scheduledApp(t)
	part := func(name, ctype string, data []byte) string {
		return "--b\r\nContent-Type: " + ctype + "\r\nContent-Disposition: attachment; filename*=UTF-8''" + name + "\r\n" +
			"Content-Transfer-Encoding: base64\r\n\r\n" + base64.StdEncoding.EncodeToString(data) + "\r\n"
	}
	raw := "From: x@example.net\r\nTo: dana@example.com\r\nSubject: Invoice\r\nMIME-Version: 1.0\r\nContent-Type: multipart/mixed; boundary=\"b\"\r\n\r\n" +
		"--b\r\nContent-Type: text/plain\r\n\r\nsee attached\r\n" +
		part("invoice%E2%80%AEfdp.exe", "application/pdf", []byte("%PDF-1.4\n%fake\n")) +
		part("photo%E2%80%AEgnp.exe", "image/png", pngBytes) + "--b--\r\n"
	id, err := a.vayuMail.DeliverInbound("x@example.net", "dana@example.com", []byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	card, _ := a.vayuReaderCard(danaReader(a), "Inbox", id, readerView{Pane: true, Places: holderPlaces{Drive: true}})
	if !strings.Contains(card, `<span class="mx-file__name">invoice_fdp.exe</span>`) || !strings.Contains(card, `alt="photo_gnp.exe"`) ||
		!strings.Contains(card, ">Open<") || !strings.Contains(card, ">Save to Drive<") {
		t.Fatalf("the reader does not show the files as expected:\n%s", card)
	}
	if strings.Contains(card, "\u202E") {
		t.Errorf("the reader shows a name as sent:\n%s", card)
	}
	for i := 0; i < 4; i++ {
		rec := attachment(a, id, i)
		if rec.Code != http.StatusOK {
			continue
		}
		if got := rec.Header().Get("Content-Disposition"); got != `inline; filename="invoice_fdp.exe"` {
			t.Errorf("the download is named %+q", got)
		}
		return
	}
	t.Fatal("the attachment could not be downloaded")
}
