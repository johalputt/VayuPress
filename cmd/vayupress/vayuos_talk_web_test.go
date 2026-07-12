package main

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/johalputt/vayupress/internal/users"
	vpgp "github.com/johalputt/vayupress/internal/vayuos/pgp"
	vtalk "github.com/johalputt/vayupress/internal/vayuos/vayutalk"
)

// appWithTalkWeb builds an App with a REAL VayuPGP engine, a REAL VayuTalk relay
// wired to it, a user store (so ownMailbox resolves the session mailbox), and
// keypairs for alice & bob. It returns the app plus the two session users the
// web handlers authenticate as. This is the shared fixture for the web↔app
// interop tests below.
func appWithTalkWeb(t *testing.T) (*App, *users.User, *users.User) {
	t.Helper()
	a := appWithMailAndPGP(t)
	for _, m := range []string{"alice@example.com", "bob@example.com"} {
		name := m[:strings.Index(m, "@")]
		if _, err := a.vayuPGP.EnsureKeypair(&vpgp.PGPUser{UserID: m, Name: name, Email: m}); err != nil {
			t.Fatalf("ensure keypair %s: %v", m, err)
		}
	}
	e := vtalk.NewEngine(vtalk.Config{
		Enabled: true,
		Verify: func(_ context.Context, email, pw string) bool {
			return (email == "alice@example.com" || email == "bob@example.com") && pw == "pw"
		},
		PubKey: func(email string) (string, string, error) {
			pk, err := a.vayuPGP.GetPublicKey(email)
			if err != nil {
				return "", "", err
			}
			return pk.Armor, pk.Fingerprint, nil
		},
	})
	if err := e.Start(context.Background()); err != nil {
		t.Fatalf("start talk: %v", err)
	}
	t.Cleanup(func() { _ = e.Stop(context.Background()) })
	a.vayuTalk = e

	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	a.userStore = users.New(db)

	alice := &users.User{ID: "a1", Email: "alice@example.com", Role: users.RoleAuthor, MailAddress: "alice@example.com"}
	bob := &users.User{ID: "b1", Email: "bob@example.com", Role: users.RoleAuthor, MailAddress: "bob@example.com"}
	return a, alice, bob
}

// openSSE opens a Server-Sent Events stream at fullURL. bearer is set when
// non-empty (the app API path); the web path authenticates by the injected
// session user instead, so it passes "". It reuses the sseStream reader from
// vayuos_talk_test.go.
func openSSE(t *testing.T, fullURL, bearer string) *sseStream {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, fullURL, nil)
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("open stream: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("stream status = %d", resp.StatusCode)
	}
	s := &sseStream{resp: resp, events: make(chan sseEvent, 16), stop: make(chan struct{})}
	go func() {
		sc := bufio.NewScanner(resp.Body)
		var cur sseEvent
		for sc.Scan() {
			line := sc.Text()
			switch {
			case strings.HasPrefix(line, "event: "):
				cur.event = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				cur.data = strings.TrimPrefix(line, "data: ")
			case line == "":
				if cur.event != "" {
					select {
					case s.events <- cur:
					case <-s.stop:
						return
					}
				}
				cur = sseEvent{}
			}
		}
	}()
	time.Sleep(50 * time.Millisecond)
	return s
}

// TestTalkWebToApp proves a message SENT from the VayuOS web console reaches the
// mobile app: Alice sends via the web handler (server signs+encrypts to Bob),
// and Bob, streaming over the app API, receives the ciphertext envelope and
// decrypts it with his own key — the same relay, byte-compatible on the wire.
func TestTalkWebToApp(t *testing.T) {
	a, alice, _ := appWithTalkWeb(t)
	bobTok := tokenFrom(t, talkConnect(t, a, "bob@example.com", "pw"))

	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/talk/stream", a.handleTalkStream)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	bob := openSSE(t, srv.URL+"/api/v1/talk/stream", bobTok)
	defer bob.close()

	// Alice sends from the web console (session-authenticated, no bearer).
	body, _ := json.Marshal(map[string]interface{}{"to": "bob@example.com", "text": "hi from the web", "ttl_seconds": 300, "mode": "store"})
	req := withUser(httptest.NewRequest(http.MethodPost, "/os/vayumail/talk/send", strings.NewReader(string(body))), alice)
	rec := httptest.NewRecorder()
	a.handleVayuOSTalkSend(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("web send = %d (%s)", rec.Code, rec.Body.String())
	}

	ev := bob.wait(t, "envelope")
	var p struct {
		From       string `json:"from"`
		Ciphertext string `json:"ciphertext"`
	}
	if err := json.Unmarshal([]byte(ev.data), &p); err != nil {
		t.Fatalf("bad envelope json: %v", err)
	}
	if p.From != "alice@example.com" {
		t.Fatalf("envelope from = %q", p.From)
	}
	raw, err := base64.StdEncoding.DecodeString(p.Ciphertext)
	if err != nil {
		t.Fatalf("ciphertext not base64: %v", err)
	}
	pt, err := a.vayuPGP.DecryptForEmail(raw, "bob@example.com")
	if err != nil {
		t.Fatalf("bob decrypt: %v", err)
	}
	if string(pt) != "hi from the web" {
		t.Fatalf("bob plaintext = %q", pt)
	}
}

// TestTalkAppToWeb proves the reverse: a message SENT from the mobile app is
// decrypted and delivered as plaintext to the web console. Bob sends app-format
// ciphertext over the API; Alice, streaming the web SSE bridge, receives the
// already-decrypted message.
func TestTalkAppToWeb(t *testing.T) {
	a, alice, _ := appWithTalkWeb(t)
	bobTok := tokenFrom(t, talkConnect(t, a, "bob@example.com", "pw"))

	mux := http.NewServeMux()
	mux.HandleFunc("/os/vayumail/talk/stream", func(w http.ResponseWriter, r *http.Request) {
		a.handleVayuOSTalkStream(w, withUser(r, alice))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	aliceWeb := openSSE(t, srv.URL+"/os/vayumail/talk/stream", "")
	defer aliceWeb.close()

	// Bob composes app-format ciphertext: armored PGP, encrypted to Alice and
	// signed by Bob — exactly what VayuMail Mobile's keyring.Encrypt produces.
	armored, err := a.vayuPGP.EncryptAndSign([]byte("hi from the app"), "alice@example.com", "bob@example.com")
	if err != nil {
		t.Fatalf("app-side encrypt: %v", err)
	}
	ct := base64.StdEncoding.EncodeToString(armored)
	sendRec := talkSend(a, bobTok, "alice@example.com", ct, "store", 300)
	if sendRec.Code != http.StatusOK {
		t.Fatalf("app send = %d (%s)", sendRec.Code, sendRec.Body.String())
	}

	ev := aliceWeb.wait(t, "message")
	var m struct {
		From string `json:"from"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal([]byte(ev.data), &m); err != nil {
		t.Fatalf("bad message json: %v", err)
	}
	if m.From != "bob@example.com" || m.Text != "hi from the app" {
		t.Fatalf("web received from=%q text=%q", m.From, m.Text)
	}
}
