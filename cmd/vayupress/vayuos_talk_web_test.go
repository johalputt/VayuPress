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
// wired to it, a user store, and keypairs for alice & bob. It returns the app
// plus the two session users the web handlers authenticate as. This is the
// shared fixture for the identity + web↔app interop tests below.
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

// TestTalkIdentityResolution pins the identity resolver that fixes the "no
// mailbox assigned" dead-end: a signed-in user gets a usable chat identity
// without an explicitly assigned mailbox row.
func TestTalkIdentityResolution(t *testing.T) {
	a, _, _ := appWithTalkWeb(t) // mail domain example.com; dana@example.com exists

	req := func(u *users.User) *http.Request {
		return withUser(httptest.NewRequest(http.MethodGet, "/os/talk", nil), u)
	}

	// 1. Assigned mailbox wins.
	if got := a.talkIdentity(req(&users.User{ID: "u1", Email: "x@example.com", Role: users.RoleAuthor, MailAddress: "assigned@example.com"})); got != "assigned@example.com" {
		t.Fatalf("assigned mailbox: got %q", got)
	}
	// 2. No assigned mailbox, but the login email is on the mail domain → use it.
	//    (This is exactly the reported case: logged in, nothing "assigned".)
	if got := a.talkIdentity(req(&users.User{ID: "u2", Email: "Boss@Example.com", Role: users.RoleAuthor})); got != "boss@example.com" {
		t.Fatalf("email-on-domain: got %q", got)
	}
	// 3. Admin with an off-domain email → falls back to a real mailbox.
	if got := a.talkIdentity(req(&users.User{ID: "u3", Email: "owner@gmail.com", Role: users.RoleAdmin})); got != "dana@example.com" {
		t.Fatalf("admin fallback: got %q", got)
	}
	// 4. Non-admin, off-domain email, no mailbox → no identity (clean empty).
	if got := a.talkIdentity(req(&users.User{ID: "u4", Email: "someone@gmail.com", Role: users.RoleAuthor})); got != "" {
		t.Fatalf("non-admin off-domain: got %q, want empty", got)
	}
}

// TestTalkChatAsBoundary pins the "chat as" switcher's authorization boundary:
// an admin may send as any active mailbox on the server, a non-admin is confined
// to their own, and an unentitled `as` value silently falls back to the default.
func TestTalkChatAsBoundary(t *testing.T) {
	a, _, _ := appWithTalkWeb(t) // dana@example.com is an active mailbox
	reqFor := func(u *users.User) *http.Request {
		return withUser(httptest.NewRequest(http.MethodGet, "/os/talk", nil), u)
	}

	admin := &users.User{ID: "ad", Email: "boss@example.com", Role: users.RoleAdmin, MailAddress: "boss@example.com"}
	// Admin lists more than one identity (their own + server mailboxes).
	if ids := a.talkIdentities(reqFor(admin)); len(ids) < 2 {
		t.Fatalf("admin should have multiple identities, got %v", ids)
	}
	// Admin may select an active server mailbox.
	if got := a.talkSelf(reqFor(admin), "dana@example.com"); got != "dana@example.com" {
		t.Fatalf("admin select dana: got %q", got)
	}
	// A non-admin is confined to their own mailbox — any other `as` falls back.
	holder := &users.User{ID: "h", Email: "holder@example.com", Role: users.RoleAuthor, MailAddress: "holder@example.com"}
	if ids := a.talkIdentities(reqFor(holder)); len(ids) != 1 || ids[0] != "holder@example.com" {
		t.Fatalf("holder identities = %v, want just their own", ids)
	}
	if got := a.talkSelf(reqFor(holder), "dana@example.com"); got != "holder@example.com" {
		t.Fatalf("holder must not chat as another mailbox: got %q", got)
	}
	// Empty selection → default.
	if got := a.talkSelf(reqFor(holder), ""); got != "holder@example.com" {
		t.Fatalf("holder default: got %q", got)
	}
}

// TestFormatSafetyMatchesApp checks the safety-number normalisation the web
// shares with VayuMail Mobile: colons/spaces stripped, uppercased, groups of
// four separated by a single space — so the two screens are comparable.
func TestFormatSafetyMatchesApp(t *testing.T) {
	cases := map[string]string{
		"aabbccddeeff00112233": "AABB CCDD EEFF 0011 2233",
		"AABB:CCDD:EEFF":       "AABB CCDD EEFF",
		"a1b2c3":               "A1B2 C3",
	}
	for in, want := range cases {
		if got := formatSafety(in); got != want {
			t.Fatalf("formatSafety(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestTalkChatAsAdminSendsAsSelected proves the switcher end to end: an admin
// who picks a different mailbox has the recipient see THAT address as the
// sender, not the admin's default identity.
func TestTalkChatAsAdminSendsAsSelected(t *testing.T) {
	a, _, _ := appWithTalkWeb(t)
	bobTok := tokenFrom(t, talkConnect(t, a, "bob@example.com", "pw"))

	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/talk/stream", a.handleTalkStream)
	srv := httptest.NewServer(mux)
	defer srv.Close()
	bob := openSSE(t, srv.URL+"/api/v1/talk/stream", bobTok)
	defer bob.close()

	admin := &users.User{ID: "ad", Email: "boss@example.com", Role: users.RoleAdmin, MailAddress: "boss@example.com"}
	body, _ := json.Marshal(map[string]interface{}{
		"to": "bob@example.com", "text": "as dana", "mode": "store", "as": "dana@example.com",
	})
	req := withUser(httptest.NewRequest(http.MethodPost, "/os/talk/send", strings.NewReader(string(body))), admin)
	rec := httptest.NewRecorder()
	a.handleVayuOSTalkSend(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("send = %d (%s)", rec.Code, rec.Body.String())
	}

	ev := bob.wait(t, "envelope")
	var p struct {
		From string `json:"from"`
	}
	_ = json.Unmarshal([]byte(ev.data), &p)
	if p.From != "dana@example.com" {
		t.Fatalf("recipient saw sender %q, want dana@example.com (the selected identity)", p.From)
	}
}

// TestTalkPeerEndpoint pins the peer-key preflight the web uses to (a) warn
// before a doomed send and (b) show a safety number for verification.
func TestTalkPeerEndpoint(t *testing.T) {
	a, alice, _ := appWithTalkWeb(t)

	// Known local mailbox → found, with a grouped safety number.
	req := withUser(httptest.NewRequest(http.MethodGet, "/os/talk/peer?email=bob@example.com", nil), alice)
	rec := httptest.NewRecorder()
	a.handleVayuOSTalkPeer(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("peer = %d (%s)", rec.Code, rec.Body.String())
	}
	var out struct {
		Found  bool   `json:"found"`
		Safety string `json:"safety"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if !out.Found || strings.TrimSpace(out.Safety) == "" {
		t.Fatalf("expected found bob with a safety number, got %s", rec.Body.String())
	}

	// Off-domain address with no key and no WKD → found:false (clean, not error).
	req2 := withUser(httptest.NewRequest(http.MethodGet, "/os/talk/peer?email=nobody@elsewhere.invalid", nil), alice)
	rec2 := httptest.NewRecorder()
	a.handleVayuOSTalkPeer(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("peer(unknown) = %d", rec2.Code)
	}
	if strings.Contains(rec2.Body.String(), `"found":true`) {
		t.Fatalf("unreachable address must report found:false, got %s", rec2.Body.String())
	}
}

// TestTalkSendUnknownRecipient verifies the send handler returns a distinct,
// actionable error (not a generic failure) when the recipient has no key.
func TestTalkSendUnknownRecipient(t *testing.T) {
	a, alice, _ := appWithTalkWeb(t)
	body, _ := json.Marshal(map[string]interface{}{"to": "ghost@elsewhere.invalid", "text": "hi", "mode": "store"})
	req := withUser(httptest.NewRequest(http.MethodPost, "/os/talk/send", strings.NewReader(string(body))), alice)
	rec := httptest.NewRecorder()
	a.handleVayuOSTalkSend(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("send to unknown = %d, want 404 (%s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "no-recipient-key") {
		t.Fatalf("want no-recipient-key code, got %s", rec.Body.String())
	}
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
	req := withUser(httptest.NewRequest(http.MethodPost, "/os/talk/send", strings.NewReader(string(body))), alice)
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
	mux.HandleFunc("/os/talk/stream", func(w http.ResponseWriter, r *http.Request) {
		a.handleVayuOSTalkStream(w, withUser(r, alice))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	aliceWeb := openSSE(t, srv.URL+"/os/talk/stream", "")
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
