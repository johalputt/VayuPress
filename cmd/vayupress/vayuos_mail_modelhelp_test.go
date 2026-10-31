// SPDX-License-Identifier: Apache-2.0

package main

import (
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/johalputt/vayupress/internal/aiassist"
	"github.com/johalputt/vayupress/internal/config"
	"github.com/johalputt/vayupress/internal/users"
	vmail "github.com/johalputt/vayupress/internal/vayuos/mail"
)

// mailFakeModel is a model server that records each prompt and answers with
// answer.
func mailFakeModel(t *testing.T, answer string) (*httptest.Server, func() []string) {
	t.Helper()
	var mu sync.Mutex
	var prompts []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var in struct {
			Prompt string `json:"prompt"`
		}
		_ = json.Unmarshal(b, &in)
		mu.Lock()
		prompts = append(prompts, in.Prompt)
		mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{"response": answer, "done": true})
	}))
	t.Cleanup(srv.Close)
	return srv, func() []string { mu.Lock(); defer mu.Unlock(); return append([]string(nil), prompts...) }
}

// What is withheld from a model outside, and put back in its answer.
func TestPersonalDetailsAreWithheld(t *testing.T) {
	in := "Write to priya@halcyon.dev or call +91 98765 43210; the card is 4111 1111 1111 1111. Meet at 10."
	masked, back := maskPersonal(in)
	for _, leak := range []string{"priya@halcyon.dev", "98765", "4111"} {
		if strings.Contains(masked, leak) {
			t.Errorf("%q leaks into %q", leak, masked)
		}
	}
	if !strings.Contains(masked, "[ADDRESS 1]") || !strings.Contains(masked, "[NUMBER 1]") || !strings.Contains(masked, "[PHONE 1]") || !strings.Contains(masked, "Meet at 10.") {
		t.Errorf("masked as %q", masked)
	}
	if got := unmaskPersonal(masked, back); got != in {
		t.Errorf("put back as %q", got)
	}
	for ep, want := range map[string]bool{"http://127.0.0.1:11434": true, "http://localhost:11434": true, "http://[::1]:1": true, "http://10.0.0.5:11434": false, "https://openrouter.ai/api/v1": false} {
		if loopbackEndpoint(ep) != want {
			t.Errorf("%s on this server = %v", ep, !want)
		}
	}
}

// Model help runs only when the holder turned it on, only for their own
// mailbox, counts each request, and answers with a suggestion.
func TestModelHelpIsTheHoldersOwn(t *testing.T) {
	a, holder, id := reviewerApp(t, vmail.RoleMailbox)
	srv, prompts := mailFakeModel(t, "Rohan wants the keys by Friday.")
	prevURL := config.Cfg.AIURL
	config.Cfg.AIURL = srv.URL
	t.Cleanup(func() { config.Cfg.AIURL = prevURL })
	a.aiAssist = aiassist.New(aiassist.Config{URL: srv.URL, Model: "m"}, nil)

	post := func(u *users.User, vals url.Values) string {
		return postAppPwForm(a.handleVayuOSMailModel, "/os/vayumail/model", vals, u).Body.String()
	}
	ask := url.Values{"op": {"summary"}, "user": {"dana"}, "folder": {"Inbox"}, "id": {id}}
	if out := post(holder, ask); !strings.Contains(out, "Model help is off") || len(prompts()) != 0 {
		t.Fatalf("with model help off: %s (%d prompts)", out, len(prompts()))
	}
	if err := a.vayuMail.Accounts().SetModelHelp(t.Context(), "dana@example.com", true); err != nil {
		t.Fatal(err)
	}
	out := post(holder, ask)
	if !strings.Contains(out, "A suggestion, on this server. Nothing has been sent or changed.") || !strings.Contains(out, "Rohan wants the keys by Friday.") || len(prompts()) != 1 {
		t.Fatalf("with model help on: %s", out)
	}
	if mh := a.vayuMail.Accounts().ModelHelpFor(t.Context(), "dana@example.com"); mh.Sent != 1 || mh.SentTo != "on this server" {
		t.Errorf("counted as %+v", mh)
	}
	admin := &users.User{ID: "u-admin", Email: "root@example.com", Role: users.RoleAdmin}
	if out := post(admin, ask); !strings.Contains(out, "only for your own mailbox") || len(prompts()) != 1 {
		t.Errorf("an administrator reading the mailbox: %s", out)
	}
}

// A model outside the server gets no address or number, nothing encrypted
// or confidential, and its answer has the withheld details put back.
func TestAModelOutsideGetsLess(t *testing.T) {
	a, holder, _ := reviewerApp(t, vmail.RoleMailbox)
	srv, prompts := mailFakeModel(t, "Write to [ADDRESS 1] today.")
	_ = holder
	rd := vmail.ReadAsOwner("dana")
	for _, raw := range []string{
		"From: Priya <priya@halcyon.dev>\r\nTo: dana@example.com\r\nSubject: Plain\r\n\r\nMy other address is priya.home@halcyon.dev.\r\n",
		"From: Priya <priya@halcyon.dev>\r\nTo: dana@example.com\r\nSubject: Secret\r\nContent-Type: multipart/encrypted; protocol=\"application/pgp-encrypted\"; boundary=\"e\"\r\n\r\n--e\r\nContent-Type: application/pgp-encrypted\r\n\r\nVersion: 1\r\n--e\r\nContent-Type: application/octet-stream\r\n\r\n-----BEGIN PGP MESSAGE-----\r\nx\r\n-----END PGP MESSAGE-----\r\n--e--\r\n",
		"From: Priya <priya@halcyon.dev>\r\nTo: dana@example.com\r\nSubject: Confidential: the numbers\r\n\r\nx\r\n",
	} {
		if _, err := a.vayuMail.DeliverInbound("priya@halcyon.dev", "dana@example.com", []byte(raw)); err != nil {
			t.Fatal(err)
		}
	}
	idOf := map[string]string{}
	msgs, _ := a.vayuMail.ListFolder(rd, "Inbox")
	for _, m := range msgs {
		idOf[m.Subject] = m.ID
	}
	outside := mailModel{b: aiassist.Backend{Kind: aiassist.KindOllama, Endpoint: srv.URL, Model: "m"}, where: "sent to Example"}
	out, err := a.runModelHelp(t.Context(), rd, outside, "reply", "Inbox", idOf["Plain"], "say yes")
	if err != nil {
		t.Fatal(err)
	}
	if p := prompts(); len(p) != 1 || strings.Contains(p[0], "@halcyon.dev") || !strings.Contains(p[0], "What to say: say yes") {
		t.Errorf("the prompt sent outside: %q", p)
	}
	if !strings.Contains(out, "Write to priya@halcyon.dev today.") || !strings.Contains(out, "Reply with this") {
		t.Errorf("the answer is not put back: %s", out)
	}
	for _, subject := range []string{"Secret", "Confidential: the numbers"} {
		if _, err := a.runModelHelp(t.Context(), rd, outside, "summary", "Inbox", idOf[subject], ""); err == nil || len(prompts()) != 1 {
			t.Errorf("%s was sent outside (%v)", subject, err)
		}
	}
}

// A model on another machine is outside the server, even one the install
// calls local: it is used only with the operator's leave, and named by its
// host on every button.
func TestAModelElsewhereNeedsTheOperatorsLeave(t *testing.T) {
	a, _, _ := reviewerApp(t, vmail.RoleMailbox)
	prevURL := config.Cfg.AIURL
	config.Cfg.AIURL = "http://10.255.255.1:11434"
	t.Cleanup(func() { config.Cfg.AIURL = prevURL })
	a.aiAssist = aiassist.New(aiassist.Config{URL: config.Cfg.AIURL, Model: "m"}, nil)
	accts := a.vayuMail.Accounts()
	if err := accts.SetModelHelp(t.Context(), "dana@example.com", true); err != nil {
		t.Fatal(err)
	}
	if _, ok := a.mailModelFor(t.Context(), "dana@example.com"); ok {
		t.Fatal("a model on another machine was used without the operator's leave")
	}
	if err := accts.SetModelOutside(t.Context(), "dana@example.com", true); err != nil {
		t.Fatal(err)
	}
	mm, ok := a.mailModelFor(t.Context(), "dana@example.com")
	if !ok || mm.local || mm.where != "sent to 10.255.255.1" {
		t.Errorf("with leave: %+v %v", mm, ok)
	}
}

// Each switch is only its owner's: the holder turns model help on, the
// administrator allows a model outside; neither sets the other's.
func TestModelHelpSwitchesAreEachTheirOwn(t *testing.T) {
	a := appWithMailAccounts(t)
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	a.userStore = users.New(db)
	dana := &users.User{ID: "vmail:dana@example.com", Email: "dana@example.com", Role: users.RoleAuthor, MailAddress: "dana@example.com"}
	admin := &users.User{ID: "u-admin", Email: "root@example.com", Role: users.RoleAdmin}
	set := func(u *users.User, op string) int {
		return postAppPwForm(a.handleVayuOSMailModelSettings, "/os/vayumail/model/settings", url.Values{"email": {"dana@example.com"}, "op": {op}, "on": {"1"}}, u).Code
	}
	if code := set(dana, "outside"); code != http.StatusForbidden {
		t.Errorf("the holder allowing a model outside: %d", code)
	}
	if code := set(admin, "help"); code != http.StatusForbidden {
		t.Errorf("an administrator turning the holder's model help on: %d", code)
	}
	if set(dana, "help") != http.StatusOK || set(admin, "outside") != http.StatusOK {
		t.Fatal("each could not set their own")
	}
	if mh := a.vayuMail.Accounts().ModelHelpFor(t.Context(), "dana@example.com"); !mh.On || !mh.Outside {
		t.Errorf("after both: %+v", mh)
	}
}
