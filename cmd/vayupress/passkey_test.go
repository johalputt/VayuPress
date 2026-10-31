// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/fxamacker/cbor/v2"

	"github.com/johalputt/vayupress/internal/auth"
	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/passkey"
	"github.com/johalputt/vayupress/internal/signin"
	"github.com/johalputt/vayupress/internal/users"
	vmail "github.com/johalputt/vayupress/internal/vayuos/mail"
)

// softKey is an authenticator in software: one ES256 key and its credential
// id, answering the ceremonies the way a browser's authenticator does, for
// the origin the test install runs at.
type softKey struct {
	key    *ecdsa.PrivateKey
	id     []byte
	count  uint32
	rpID   string
	origin string
}

func newSoftKey(t *testing.T) *softKey {
	t.Helper()
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	id := make([]byte, 16)
	_, _ = rand.Read(id)
	return &softKey{key: k, id: id, rpID: "example.com", origin: "https://example.com"}
}

var pkB64 = base64.RawURLEncoding

func (k *softKey) clientData(kind, challenge string) []byte {
	b, _ := json.Marshal(map[string]any{"type": kind, "challenge": challenge, "origin": k.origin, "crossOrigin": false})
	return b
}

// authData is the authenticator data: the relying party's hash, the flags
// (present and verified, and attested data when adding), the count, and when
// adding the credential and its public key.
func (k *softKey) authData(attested bool) []byte {
	rp := sha256.Sum256([]byte(k.rpID))
	var b bytes.Buffer
	b.Write(rp[:])
	flags := byte(0x01 | 0x04)
	if attested {
		flags |= 0x40
	}
	b.WriteByte(flags)
	k.count++
	_ = binary.Write(&b, binary.BigEndian, k.count)
	if attested {
		b.Write(make([]byte, 16)) // no AAGUID
		_ = binary.Write(&b, binary.BigEndian, uint16(len(k.id)))
		b.Write(k.id)
		point, _ := k.key.PublicKey.Bytes() // 0x04, then X and Y, 32 bytes each
		x, y := point[1:33], point[33:65]
		cose, _ := cbor.Marshal(map[int]any{1: 2, 3: -7, -1: 1, -2: x, -3: y})
		b.Write(cose)
	}
	return b.Bytes()
}

// made is the answer to an add ceremony's options.
func (k *softKey) made(t *testing.T, options map[string]any) []byte {
	t.Helper()
	pk := options["publicKey"].(map[string]any)
	att, _ := cbor.Marshal(map[string]any{"fmt": "none", "attStmt": map[string]any{}, "authData": k.authData(true)})
	body, _ := json.Marshal(map[string]any{
		"id": pkB64.EncodeToString(k.id), "rawId": pkB64.EncodeToString(k.id), "type": "public-key",
		"response": map[string]any{
			"clientDataJSON":    pkB64.EncodeToString(k.clientData("webauthn.create", pk["challenge"].(string))),
			"attestationObject": pkB64.EncodeToString(att),
		},
	})
	return body
}

// signed is the answer to a sign-in ceremony's options, as handle's passkey.
func (k *softKey) signed(t *testing.T, options map[string]any, handle []byte) []byte {
	t.Helper()
	pk := options["publicKey"].(map[string]any)
	cd := k.clientData("webauthn.get", pk["challenge"].(string))
	ad := k.authData(false)
	h := sha256.Sum256(cd)
	digest := sha256.Sum256(append(append([]byte{}, ad...), h[:]...))
	sig, err := ecdsa.SignASN1(rand.Reader, k.key, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]any{
		"id": pkB64.EncodeToString(k.id), "rawId": pkB64.EncodeToString(k.id), "type": "public-key",
		"response": map[string]any{
			"clientDataJSON": pkB64.EncodeToString(cd), "authenticatorData": pkB64.EncodeToString(ad),
			"signature": pkB64.EncodeToString(sig), "userHandle": pkB64.EncodeToString(handle),
		},
	})
	return body
}

func pkPost(h http.HandlerFunc, path string, u *users.User, from string, body []byte) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "https://example.com")
	req.RemoteAddr = from + ":5000"
	if u != nil {
		req = withUser(req, u)
	}
	rec := httptest.NewRecorder()
	h(rec, req)
	return rec
}

type started struct {
	Ceremony string         `json:"ceremony"`
	Options  map[string]any `json:"options"`
}

func startOf(t *testing.T, rec *httptest.ResponseRecorder) started {
	t.Helper()
	var s started
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &s) != nil || s.Ceremony == "" {
		t.Fatalf("a ceremony did not start: %d %s", rec.Code, rec.Body.String())
	}
	return s
}

// addPasskey runs the add ceremony for u with key k and gives back u's handle.
func addPasskey(t *testing.T, a *App, u *users.User, k *softKey) []byte {
	t.Helper()
	s := startOf(t, pkPost(a.handlePasskeyAddBegin, "/os/api/passkeys/begin", u, "192.0.2.50", nil))
	handle, err := pkB64.DecodeString(s.Options["publicKey"].(map[string]any)["user"].(map[string]any)["id"].(string))
	if err != nil {
		t.Fatal(err)
	}
	if rec := pkPost(a.handlePasskeyAddFinish, "/os/api/passkeys/finish?ceremony="+url.QueryEscape(s.Ceremony)+"&name=Laptop", u, "192.0.2.50", k.made(t, s.Options)); rec.Code != http.StatusOK {
		t.Fatalf("adding the passkey = %d %s", rec.Code, rec.Body.String())
	}
	return handle
}

// signInWith runs a sign-in from from with k, answering as handle, and gives
// back the response.
func signInWith(t *testing.T, a *App, k *softKey, handle []byte, from string) *httptest.ResponseRecorder {
	t.Helper()
	s := startOf(t, pkPost(a.handlePasskeySignInBegin, "/os/passkey/begin", nil, from, nil))
	return pkPost(a.handlePasskeySignInFinish, "/os/passkey/finish?ceremony="+url.QueryEscape(s.Ceremony), nil, from, k.signed(t, s.Options, handle))
}

func sessionOf(t *testing.T, a *App, rec *httptest.ResponseRecorder) string {
	t.Helper()
	for _, c := range rec.Result().Cookies() {
		if c.Value != "" {
			if id, err := a.sessions.Validate(context.Background(), c.Value); err == nil {
				return id
			}
		}
	}
	return ""
}

func passkeyApp(t *testing.T) *App {
	t.Helper()
	a := credentialFloodApp(t)
	withSignins(t, a)
	a.passkeys = passkeyStoreForTest()
	return a
}

// A mailbox's holder adds a passkey, and it signs in as that mailbox, with no
// name typed: the session is the mailbox's own.
func TestAPasskeySignsInAsTheMailboxThatAddedIt(t *testing.T) {
	a := passkeyApp(t)
	boss := holderOf(lockedMailbox)
	k := newSoftKey(t)
	handle := addPasskey(t, a, boss, k)
	if list, _ := a.passkeys.List(context.Background(), "vmail:"+lockedMailbox); len(list) != 1 || list[0].Name != "Laptop" {
		t.Fatalf("passkeys = %+v", list)
	}
	rec := signInWith(t, a, k, handle, "198.51.100.140")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"next": "/os/"`) {
		t.Fatalf("signing in = %d %s", rec.Code, rec.Body.String())
	}
	if got := sessionOf(t, a, rec); got != "vmail:"+lockedMailbox {
		t.Errorf("the session is %q, want the mailbox's", got)
	}
}

// An install account's passkey signs in as that account, not as a mailbox at
// the same address.
func TestAPasskeySignsInAsTheAccountThatAddedIt(t *testing.T) {
	a := passkeyApp(t)
	u, err := a.userStore.Create(context.Background(), lockedMailbox, "Boss", "a-console-password-1", "admin")
	if err != nil {
		t.Fatal(err)
	}
	k := newSoftKey(t)
	handle := addPasskey(t, a, u, k)
	if got := sessionOf(t, a, signInWith(t, a, k, handle, "198.51.100.141")); got != u.ID {
		t.Errorf("the session is %q, want the account %q", got, u.ID)
	}
	if err := a.userStore.Delete(context.Background(), u.Email); err != nil {
		t.Fatal(err)
	}
	if rec := signInWith(t, a, k, handle, "198.51.100.147"); rec.Code != http.StatusUnauthorized {
		t.Errorf("a removed account's passkey = %d, want 401", rec.Code)
	}
}

// A sign-in is refused: answered by a key that is not the passkey's; answered
// twice; from a blocked source; for a mailbox that is closed. A paused
// account is not refused: a passkey cannot be guessed.
func TestAPasskeySignInIsRefusedWhenItShouldBe(t *testing.T) {
	a := passkeyApp(t)
	ctx := context.Background()
	k := newSoftKey(t)
	handle := addPasskey(t, a, holderOf(lockedMailbox), k)

	impostor := newSoftKey(t)
	impostor.id = k.id
	if rec := signInWith(t, a, impostor, handle, "198.51.100.142"); rec.Code != http.StatusUnauthorized {
		t.Errorf("another key answering for the passkey = %d, want 401", rec.Code)
	}

	s := startOf(t, pkPost(a.handlePasskeySignInBegin, "/os/passkey/begin", nil, "198.51.100.143", nil))
	answer := k.signed(t, s.Options, handle)
	if rec := pkPost(a.handlePasskeySignInFinish, "/os/passkey/finish?ceremony="+url.QueryEscape(s.Ceremony), nil, "198.51.100.143", answer); rec.Code != http.StatusOK {
		t.Fatalf("the first answer = %d %s", rec.Code, rec.Body.String())
	}
	// A fresh answer, signed and counted on, to the ceremony already used: only
	// the ceremony being single-use refuses it.
	if rec := pkPost(a.handlePasskeySignInFinish, "/os/passkey/finish?ceremony="+url.QueryEscape(s.Ceremony), nil, "198.51.100.143", k.signed(t, s.Options, handle)); rec.Code != http.StatusUnauthorized {
		t.Errorf("a second answer to one ceremony = %d, want 401", rec.Code)
	}

	for i := 0; i < 5; i++ {
		auth.RecordAccountFailure(lockedMailbox, "203.0.113.1")
	}
	if locked, _ := auth.CheckAccountLockout(lockedMailbox); !locked {
		t.Fatal("the account is not paused")
	}
	if rec := signInWith(t, a, k, handle, "198.51.100.144"); rec.Code != http.StatusOK {
		t.Errorf("a paused account's passkey = %d %s, want a sign-in", rec.Code, rec.Body.String())
	}

	a.signin.Record(signinAttemptFrom("198.51.100.145"))
	_ = a.signin.Flush(ctx)
	rows, _ := a.signin.Attempts(ctx, "", "x@example.com", 1)
	if _, err := a.signin.BlockSource(ctx, rows[0].FP, false, false, "", time.Hour, "admin", "192.0.2.200"); err != nil {
		t.Fatal(err)
	}
	if rec := signInWith(t, a, k, handle, "198.51.100.145"); rec.Code != http.StatusForbidden {
		t.Errorf("a blocked source's passkey = %d, want 403", rec.Code)
	}

	if err := a.vayuMail.Accounts().SetActive(ctx, lockedMailbox, false); err != nil {
		t.Fatal(err)
	}
	if rec := signInWith(t, a, k, handle, "198.51.100.146"); rec.Code != http.StatusUnauthorized {
		t.Errorf("a closed mailbox's passkey = %d, want 401", rec.Code)
	}
}

// A passkey is added only by the session that started adding it, and removed
// only by its own principal.
func TestAPasskeyIsOnlyItsPrincipals(t *testing.T) {
	a := passkeyApp(t)
	boss, erin := holderOf(lockedMailbox), holderOf("erin@example.com")
	k := newSoftKey(t)
	s := startOf(t, pkPost(a.handlePasskeyAddBegin, "/os/api/passkeys/begin", boss, "192.0.2.50", nil))
	if rec := pkPost(a.handlePasskeyAddFinish, "/os/api/passkeys/finish?ceremony="+url.QueryEscape(s.Ceremony), erin, "192.0.2.50", k.made(t, s.Options)); rec.Code != http.StatusBadRequest {
		t.Errorf("another session finishing the ceremony = %d, want 400", rec.Code)
	}
	if list, _ := a.passkeys.List(context.Background(), "vmail:erin@example.com"); len(list) != 0 {
		t.Error("a passkey was added to the session that did not start the ceremony")
	}
	addPasskey(t, a, boss, newSoftKey(t))
	list, _ := a.passkeys.List(context.Background(), "vmail:"+lockedMailbox)
	del := func(u *users.User) int {
		req := httptest.NewRequest(http.MethodPost, "/os/api/passkeys/remove?id="+itoaSafe(int(list[0].ID)), nil)
		req = withUser(req, u)
		rec := httptest.NewRecorder()
		a.handlePasskeyDelete(rec, req)
		return rec.Code
	}
	if code := del(erin); code != http.StatusNotFound {
		t.Errorf("another holder removing the passkey = %d, want 404", code)
	}
	if code := del(boss); code != http.StatusOK {
		t.Errorf("the holder removing their passkey = %d, want 200", code)
	}
}

func passkeyStoreForTest() *passkey.Store { return passkey.New(dbpkg.DB) }

// signinAttemptFrom is a wrong password from ip, so the source can be blocked.
func signinAttemptFrom(ip string) signin.Attempt {
	return signin.Attempt{Account: "x@example.com", Door: signin.DoorWeb, Stage: signin.StagePassword, IP: ip}
}

// A passkey whose count went back is a copy of the authenticator, and is
// refused, though its signature is good.
func TestACopiedPasskeyIsRefused(t *testing.T) {
	a := passkeyApp(t)
	k := newSoftKey(t)
	handle := addPasskey(t, a, holderOf(lockedMailbox), k)
	for i := 0; i < 3; i++ {
		if rec := signInWith(t, a, k, handle, "198.51.100.150"); rec.Code != http.StatusOK {
			t.Fatalf("sign-in %d = %d", i, rec.Code)
		}
	}
	copied := *k
	copied.count = 0
	if rec := signInWith(t, a, &copied, handle, "198.51.100.151"); rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), "looks copied") {
		t.Errorf("a copied passkey = %d %s, want refused as copied", rec.Code, rec.Body.String())
	}
}

// postOnly turns u's passkey only on or off and gives back the answer.
func postOnly(a *App, u *users.User, on string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/os/api/passkeys/only?on="+on, nil)
	req = withUser(req, u)
	rec := httptest.NewRecorder()
	a.handlePasskeyOnly(rec, req)
	return rec
}

// Passkey only needs a spare passkey; then the mailbox's own password is
// refused at every door while an app password and a passkey still sign in;
// removing passkeys below two, or the administrator, turns the password back
// on.
func TestPasskeyOnlyRefusesTheMailboxsOwnPassword(t *testing.T) {
	a := passkeyApp(t)
	ctx := context.Background()
	if err := a.vayuMail.Accounts().SetRequireDeviceApproval(ctx, lockedMailbox, false); err != nil {
		t.Fatal(err)
	}
	boss := holderOf(lockedMailbox)
	k := newSoftKey(t)
	handle := addPasskey(t, a, boss, k)
	if rec := postOnly(a, boss, "1"); rec.Code != http.StatusConflict {
		t.Fatalf("passkey only with one passkey = %d, want refused for want of a spare", rec.Code)
	}
	addPasskey(t, a, boss, newSoftKey(t))
	if rec := postOnly(a, boss, "1"); rec.Code != http.StatusOK {
		t.Fatalf("passkey only with a spare = %d", rec.Code)
	}
	const secret = "abcdabcdabcdabcdabcd"
	hash, _ := auth.HashSecretArgon2id(secret)
	_, _ = a.vayuMail.Accounts().CreateAppPassword(ctx, lockedMailbox, "phone", hash)

	if _, got := postVayuMailLoginCode(t, a, "198.51.100.160", lockedMailbox, lockedRight, ""); got == "authenticated" {
		t.Error("the site's sign-in took the password of a mailbox that signs in by passkey only")
	}
	if ok, _ := (&vayuMailBridge{app: a}).AuthUser(lockedMailbox, lockedRight, vmail.Origin{Addr: "198.51.100.161:1", Door: "imap"}); ok {
		t.Error("IMAP took the password of a mailbox that signs in by passkey only")
	}
	form := url.Values{"email": {lockedMailbox}, "password": {lockedRight}}
	req := httptest.NewRequest(http.MethodPost, "/os/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.RemoteAddr = "198.51.100.162:5000"
	rec := httptest.NewRecorder()
	a.handleOSLoginSubmit(rec, req)
	if rec.Code == http.StatusSeeOther {
		t.Error("the console took the password of a mailbox that signs in by passkey only")
	}
	if ok, _ := (&vayuMailBridge{app: a}).AuthUser(lockedMailbox, secret, vmail.Origin{Addr: "198.51.100.163:1", Door: "imap"}); !ok {
		t.Error("an app password stopped working under passkey only")
	}
	if rec := signInWith(t, a, k, handle, "198.51.100.164"); rec.Code != http.StatusOK {
		t.Errorf("the passkey under passkey only = %d", rec.Code)
	}

	list, _ := a.passkeys.List(ctx, "vmail:"+lockedMailbox)
	del := httptest.NewRequest(http.MethodPost, "/os/api/passkeys/remove?id="+itoaSafe(int(list[1].ID)), nil)
	a.handlePasskeyDelete(httptest.NewRecorder(), withUser(del, boss))
	if _, got := postVayuMailLoginCode(t, a, "198.51.100.165", lockedMailbox, lockedRight, ""); got != "authenticated" {
		t.Errorf("with one passkey left the password stayed off: %s", got)
	}

	addPasskey(t, a, boss, newSoftKey(t))
	_ = postOnly(a, boss, "1")
	adm := httptest.NewRequest(http.MethodPost, "/os/api/shield/sign-ins/password-on?principal="+url.QueryEscape("vmail:"+lockedMailbox), nil)
	a.handleOSSigninPasswordOn(httptest.NewRecorder(), adm)
	if _, got := postVayuMailLoginCode(t, a, "198.51.100.166", lockedMailbox, lockedRight, ""); got != "authenticated" {
		t.Errorf("after the administrator turned it back on the password stayed off: %s", got)
	}
}

// An install account's passkey only refuses that account's password, not
// the password of the mailbox at the same address, which is another's.
func TestPasskeyOnlyIsTheAccountsNotTheAddresss(t *testing.T) {
	a := passkeyApp(t)
	ctx := context.Background()
	const consolePass = "the-console-password-1"
	u, err := a.userStore.Create(ctx, lockedMailbox, "Boss", consolePass, "admin")
	if err != nil {
		t.Fatal(err)
	}
	addPasskey(t, a, u, newSoftKey(t))
	addPasskey(t, a, u, newSoftKey(t))
	if rec := postOnly(a, u, "1"); rec.Code != http.StatusOK {
		t.Fatalf("passkey only = %d", rec.Code)
	}
	try := func(pass string) int {
		form := url.Values{"email": {lockedMailbox}, "password": {pass}}
		req := httptest.NewRequest(http.MethodPost, "/os/login", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.RemoteAddr = "198.51.100.170:5000"
		rec := httptest.NewRecorder()
		a.handleOSLoginSubmit(rec, req)
		return rec.Code
	}
	if code := try(consolePass); code == http.StatusSeeOther {
		t.Error("the account's own password signed in under passkey only")
	}
	auth.LiftAllAccountLockouts()
	if code := try(lockedRight); code != http.StatusSeeOther {
		t.Errorf("the mailbox's password at the same address = %d, want a sign-in: passkey only is the account's", code)
	}
}

// The middleware refuses a request from a source blocked from the whole site
// and passes any other.
func TestTheSiteBlockMiddlewareRefusesABlockedSource(t *testing.T) {
	a := passkeyApp(t)
	ctx := context.Background()
	a.signin.Record(signinAttemptFrom("198.51.100.180"))
	_ = a.signin.Flush(ctx)
	rows, _ := a.signin.Attempts(ctx, "", "x@example.com", 1)
	if _, err := a.signin.BlockSource(ctx, rows[0].FP, false, true, "", time.Hour, "admin", "192.0.2.200"); err != nil {
		t.Fatal(err)
	}
	h := a.siteBlockMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) }))
	for ip, want := range map[string]int{"198.51.100.180": http.StatusForbidden, "198.51.100.181": http.StatusTeapot} {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = ip + ":5000"
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != want {
			t.Errorf("a request from %s = %d, want %d", ip, rec.Code, want)
		}
	}
}
