// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/johalputt/vayupress/internal/users"
	vmail "github.com/johalputt/vayupress/internal/vayuos/mail"
)

// A holder seals a message from their page; the person outside opens it on
// the public page with the passphrase alone, sees it escaped, and answers
// into the holder's Inbox. A wrong passphrase says no more than "not here".
func TestASealedMessageRoundTrip(t *testing.T) {
	a := appWithMailAccounts(t)
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	a.userStore = users.New(db)
	dana := &users.User{ID: "vmail:dana@example.com", Email: "dana@example.com", Role: users.RoleAuthor, MailAddress: "dana@example.com"}
	const pass = "plain old passphrase"

	rec := postAppPwForm(a.handleVayuOSSealedAction, "/os/vayumail/sealed", url.Values{"op": {"new"}, "to": {"rohan@outside.example"},
		"subject": {"Keys"}, "body": {"Use <script>alert(1)</script> nowhere."}, "passphrase": {pass}, "days": {"1"}}, dana)
	if !strings.Contains(rec.Header().Get("HX-Trigger"), "Sealed. The link is on its way to rohan@outside.example") || !strings.Contains(rec.Body.String(), "rohan@outside.example") {
		t.Fatalf("sealing: %q\n%s", rec.Header().Get("HX-Trigger"), rec.Body.String())
	}
	list, _ := a.vayuMail.SealedSent(vmail.ReadAsOwner("dana"))
	if len(list) != 1 {
		t.Fatalf("the holder's list: %+v", list)
	}
	id := list[0].ID

	open := func(vals url.Values) string {
		method := http.MethodGet
		var body *strings.Reader
		if vals != nil {
			method, body = http.MethodPost, strings.NewReader(vals.Encode())
		} else {
			body = strings.NewReader("")
		}
		req := httptest.NewRequest(method, "/sealed/"+id, body)
		req.RemoteAddr = "198.51.100.200:4000"
		if vals != nil {
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
		rc := chi.NewRouteContext()
		rc.URLParams.Add("id", id)
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rc))
		w := httptest.NewRecorder()
		a.handleSealedPage(w, req)
		if w.Header().Get("Cache-Control") != "no-store" {
			t.Error("a sealed page may be cached")
		}
		return w.Body.String()
	}
	if p := open(nil); !strings.Contains(p, `name="passphrase"`) || strings.Contains(p, "Keys") || strings.Contains(p, "dana") {
		t.Errorf("the page before the passphrase says something:\n%s", p)
	}
	if p := open(url.Values{"op": {"open"}, "passphrase": {"not the passphrase"}}); !strings.Contains(p, "is not here, or the passphrase is not its own") || strings.Contains(p, "Keys") {
		t.Errorf("a wrong passphrase:\n%s", p)
	}
	p := open(url.Values{"op": {"open"}, "passphrase": {pass}})
	if !strings.Contains(p, ">Keys</h1>") || !strings.Contains(p, "Use &lt;script&gt;alert(1)&lt;/script&gt; nowhere.") || strings.Contains(p, "<script>alert") {
		t.Errorf("opened:\n%s", p)
	}
	if p := open(url.Values{"op": {"reply"}, "passphrase": {pass}, "text": {"Thanks, got it."}}); !strings.Contains(p, "Your answer is on its way") {
		t.Errorf("answered:\n%s", p)
	}
	inbox, _ := a.vayuMail.ListFolder(vmail.ReadAsOwner("dana"), "Inbox")
	found := false
	for _, m := range inbox {
		found = found || (m.Subject == "Re: Keys" && strings.Contains(m.From, "rohan@outside.example"))
	}
	if !found {
		t.Error("the answer is not in the holder's Inbox")
	}
}
