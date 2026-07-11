package main

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	vpgp "github.com/johalputt/vayupress/internal/vayuos/pgp"
	vtalk "github.com/johalputt/vayupress/internal/vayuos/vayutalk"
)

// appWithTalk builds an App whose VayuTalk engine uses a fake verifier and a
// fake pubkey provider, so the handler suite needs no real PGP/DB. The verifier
// accepts exactly the pairs in creds.
func appWithTalk(t *testing.T, creds map[string]string) *App {
	t.Helper()
	a := &App{}
	e := vtalk.NewEngine(vtalk.Config{
		Enabled: true,
		Verify: func(_ context.Context, email, password string) bool {
			want, ok := creds[email]
			return ok && want == password
		},
		PubKey: func(email string) (string, string, error) {
			if email == "dana@example.com" {
				return "-----BEGIN PGP PUBLIC KEY BLOCK-----\nX\n-----END PGP PUBLIC KEY BLOCK-----", "FPR123", nil
			}
			return "", "", vpgp.ErrNotFound
		},
	})
	ctx, cancel := context.WithCancel(context.Background())
	if err := e.Start(ctx); err != nil {
		t.Fatalf("start talk: %v", err)
	}
	t.Cleanup(func() {
		_ = e.Stop(context.Background())
		cancel()
	})
	a.vayuTalk = e
	return a
}

func talkConnect(t *testing.T, a *App, email, password string) *httptest.ResponseRecorder {
	t.Helper()
	payload, _ := json.Marshal(map[string]string{"email": email, "password": password})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/talk/connect", strings.NewReader(string(payload)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	a.handleTalkConnect(rec, req)
	return rec
}

func tokenFrom(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var out struct {
		Token     string `json:"token"`
		ExpiresIn int    `json:"expires_in"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode connect: %v (%s)", err, rec.Body.String())
	}
	if out.Token == "" {
		t.Fatalf("empty token: %s", rec.Body.String())
	}
	if out.ExpiresIn != 43200 {
		t.Fatalf("expires_in = %d, want 43200", out.ExpiresIn)
	}
	return out.Token
}

func talkSend(a *App, token, to, ciphertextB64, mode string, ttl int) *httptest.ResponseRecorder {
	payload, _ := json.Marshal(map[string]interface{}{"to": to, "ciphertext": ciphertextB64, "mode": mode, "ttl_seconds": ttl})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/talk/send", strings.NewReader(string(payload)))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	a.handleTalkSend(rec, req)
	return rec
}

func TestTalkConnectValidAndInvalid(t *testing.T) {
	a := appWithTalk(t, map[string]string{"dana@example.com": "pw"})
	if rec := talkConnect(t, a, "dana@example.com", "pw"); rec.Code != http.StatusOK {
		t.Fatalf("valid connect = %d (%s)", rec.Code, rec.Body.String())
	} else {
		tokenFrom(t, rec)
	}
	// Wrong password and unknown mailbox both yield a uniform 401.
	for _, c := range []struct{ email, pw string }{{"dana@example.com", "wrong"}, {"nobody@example.com", "pw"}} {
		rec := talkConnect(t, a, c.email, c.pw)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("bad connect (%s) = %d, want 401", c.email, rec.Code)
		}
		if !strings.Contains(rec.Body.String(), "invalid-credentials") {
			t.Fatalf("bad connect body = %s", rec.Body.String())
		}
	}
}

func TestTalkConnectDisabled(t *testing.T) {
	a := &App{vayuTalk: vtalk.NewEngine(vtalk.Config{Enabled: false})}
	rec := talkConnect(t, a, "dana@example.com", "pw")
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("disabled connect = %d, want 503", rec.Code)
	}
}

func TestTalkSendRequiresBearer(t *testing.T) {
	a := appWithTalk(t, map[string]string{"dana@example.com": "pw"})
	ct := base64.StdEncoding.EncodeToString([]byte("hi"))
	if rec := talkSend(a, "", "dana@example.com", ct, "store", 300); rec.Code != http.StatusUnauthorized {
		t.Fatalf("no-bearer send = %d, want 401", rec.Code)
	}
	if rec := talkSend(a, "garbage-token", "dana@example.com", ct, "store", 300); rec.Code != http.StatusUnauthorized {
		t.Fatalf("bad-bearer send = %d, want 401", rec.Code)
	}
}

func TestTalkSendLiveOfflineDropped(t *testing.T) {
	a := appWithTalk(t, map[string]string{"a@example.com": "pw"})
	token := tokenFrom(t, talkConnect(t, a, "a@example.com", "pw"))
	ct := base64.StdEncoding.EncodeToString([]byte("hi"))
	rec := talkSend(a, token, "offline@example.com", ct, "live", 300)
	if rec.Code != http.StatusOK {
		t.Fatalf("send = %d (%s)", rec.Code, rec.Body.String())
	}
	var out struct {
		Delivered bool `json:"delivered"`
		Queued    bool `json:"queued"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if out.Delivered || out.Queued {
		t.Fatalf("live offline: delivered=%v queued=%v, want both false", out.Delivered, out.Queued)
	}
}

func TestTalkSendOversize413(t *testing.T) {
	a := appWithTalk(t, map[string]string{"a@example.com": "pw"})
	token := tokenFrom(t, talkConnect(t, a, "a@example.com", "pw"))
	big := base64.StdEncoding.EncodeToString(make([]byte, vtalk.MaxCiphertextBytes+1))
	rec := talkSend(a, token, "b@example.com", big, "store", 300)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversize send = %d, want 413 (%s)", rec.Code, rec.Body.String())
	}
}

func TestTalkSendQueueFull429(t *testing.T) {
	a := appWithTalk(t, map[string]string{"a@example.com": "pw"})
	token := tokenFrom(t, talkConnect(t, a, "a@example.com", "pw"))
	ct := base64.StdEncoding.EncodeToString([]byte("hi"))
	// Fill the per-recipient queue for an offline recipient.
	for i := 0; i < vtalk.MaxPerRecipientQueue; i++ {
		if rec := talkSend(a, token, "victim@example.com", ct, "store", 300); rec.Code != http.StatusOK {
			t.Fatalf("fill %d = %d", i, rec.Code)
		}
	}
	rec := talkSend(a, token, "victim@example.com", ct, "store", 300)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("over-cap send = %d, want 429 (%s)", rec.Code, rec.Body.String())
	}
}

func TestTalkExpiredTokenRejected(t *testing.T) {
	a := appWithTalk(t, map[string]string{"a@example.com": "pw"})
	// A well-formed but never-issued token must be rejected.
	ct := base64.StdEncoding.EncodeToString([]byte("hi"))
	rec := talkSend(a, "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", "b@example.com", ct, "store", 300)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unknown token send = %d, want 401", rec.Code)
	}
}

func TestTalkPubkey(t *testing.T) {
	a := appWithTalk(t, map[string]string{"a@example.com": "pw"})
	token := tokenFrom(t, talkConnect(t, a, "a@example.com", "pw"))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/talk/pubkey?email=dana@example.com", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	a.handleTalkPubkey(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("pubkey = %d (%s)", rec.Code, rec.Body.String())
	}
	var out struct {
		Email       string `json:"email"`
		Armored     string `json:"armored_public_key"`
		Fingerprint string `json:"fingerprint"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if out.Fingerprint != "FPR123" || !strings.Contains(out.Armored, "PGP PUBLIC KEY") {
		t.Fatalf("pubkey body = %s", rec.Body.String())
	}

	// Unknown recipient -> 404.
	req2 := httptest.NewRequest(http.MethodGet, "/api/v1/talk/pubkey?email=ghost@example.com", nil)
	req2.Header.Set("Authorization", "Bearer "+token)
	rec2 := httptest.NewRecorder()
	a.handleTalkPubkey(rec2, req2)
	if rec2.Code != http.StatusNotFound {
		t.Fatalf("unknown pubkey = %d, want 404", rec2.Code)
	}
}

// TestTalkPubkeyRealPGP wires the VayuTalk pubkey provider to the REAL VayuPGP
// engine (temp keystore) to confirm the injection resolves a live key and mints
// one on demand for a mailbox that has none yet.
func TestTalkPubkeyRealPGP(t *testing.T) {
	a := appWithMailAndPGP(t)
	if _, err := a.vayuPGP.EnsureKeypair(&vpgp.PGPUser{UserID: "dana@example.com", Name: "Dana", Email: "dana@example.com"}); err != nil {
		t.Fatalf("ensure keypair: %v", err)
	}
	e := vtalk.NewEngine(vtalk.Config{
		Enabled: true,
		Verify: func(_ context.Context, email, password string) bool {
			return email == "dana@example.com" && password == "pw"
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

	token := tokenFrom(t, talkConnect(t, a, "dana@example.com", "pw"))
	req := httptest.NewRequest(http.MethodGet, "/api/v1/talk/pubkey?email=dana@example.com", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	a.handleTalkPubkey(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("real-pgp pubkey = %d (%s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "PGP PUBLIC KEY") {
		t.Fatalf("real-pgp pubkey body = %s", rec.Body.String())
	}
}

// TestTalkRoundTrip drives the full protocol: A and B connect, B opens a stream,
// A sends a store-mode envelope, B receives it over SSE, B acks, and A (also
// streaming) receives the read receipt.
func TestTalkRoundTrip(t *testing.T) {
	a := appWithTalk(t, map[string]string{"alice@example.com": "pw", "bob@example.com": "pw"})
	aliceTok := tokenFrom(t, talkConnect(t, a, "alice@example.com", "pw"))
	bobTok := tokenFrom(t, talkConnect(t, a, "bob@example.com", "pw"))

	// Alice opens a stream (to receive the read receipt) via a real test server
	// so SSE streaming (flusher + context cancel) is exercised end-to-end.
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/talk/stream", a.handleTalkStream)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	aliceEvents := openStream(t, srv.URL, aliceTok)
	defer aliceEvents.close()
	bobEvents := openStream(t, srv.URL, bobTok)
	defer bobEvents.close()

	// Alice sends a store-mode message to Bob (who is online).
	ct := base64.StdEncoding.EncodeToString([]byte("cipher"))
	sendRec := talkSend(a, aliceTok, "bob@example.com", ct, "store", 300)
	if sendRec.Code != http.StatusOK {
		t.Fatalf("send = %d (%s)", sendRec.Code, sendRec.Body.String())
	}
	var sent struct {
		ID        string `json:"id"`
		Delivered bool   `json:"delivered"`
	}
	_ = json.Unmarshal(sendRec.Body.Bytes(), &sent)
	if !sent.Delivered || sent.ID == "" {
		t.Fatalf("send result: delivered=%v id=%q", sent.Delivered, sent.ID)
	}

	// Bob receives the envelope over SSE.
	ev := bobEvents.wait(t, "envelope")
	var envPayload struct {
		ID   string `json:"id"`
		From string `json:"from"`
	}
	_ = json.Unmarshal([]byte(ev.data), &envPayload)
	if envPayload.ID != sent.ID || envPayload.From != "alice@example.com" {
		t.Fatalf("bob envelope = %+v", envPayload)
	}

	// Bob acks; Alice receives the read receipt.
	ackBody, _ := json.Marshal(map[string]string{"id": sent.ID})
	ackReq := httptest.NewRequest(http.MethodPost, "/api/v1/talk/ack", strings.NewReader(string(ackBody)))
	ackReq.Header.Set("Authorization", "Bearer "+bobTok)
	ackRec := httptest.NewRecorder()
	a.handleTalkAck(ackRec, ackReq)
	if ackRec.Code != http.StatusOK {
		t.Fatalf("ack = %d (%s)", ackRec.Code, ackRec.Body.String())
	}

	receipt := aliceEvents.wait(t, "receipt")
	var rp struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}
	_ = json.Unmarshal([]byte(receipt.data), &rp)
	if rp.ID != sent.ID || rp.Status != "read" {
		t.Fatalf("alice receipt = %+v", rp)
	}
}

// --- minimal SSE client for the round-trip test ---

type sseEvent struct{ event, data string }

type sseStream struct {
	resp   *http.Response
	events chan sseEvent
	stop   chan struct{}
}

func (s *sseStream) close() {
	select {
	case <-s.stop:
	default:
		close(s.stop)
	}
	_ = s.resp.Body.Close()
}

func (s *sseStream) wait(t *testing.T, event string) sseEvent {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case e := <-s.events:
			if e.event == event {
				return e
			}
		case <-deadline:
			t.Fatalf("timed out waiting for %q event", event)
			return sseEvent{}
		}
	}
}

func openStream(t *testing.T, base, token string) *sseStream {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, base+"/api/v1/talk/stream", nil)
	req.Header.Set("Authorization", "Bearer "+token)
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
	// Give the server a moment to register the subscription before the caller
	// sends, so a live message is not missed.
	time.Sleep(50 * time.Millisecond)
	return s
}
