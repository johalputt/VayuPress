// SPDX-License-Identifier: Apache-2.0

package pgp

// verdict_test.go — the Mail reader's seal says "verified" only for a
// signature made with the claimed sender's key on file. One case per verdict,
// for each of the three ways mail is signed, so a mutation of any one rule
// fails the test named for it.

import (
	"bytes"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/ProtonMail/go-crypto/openpgp/armor"
	"github.com/ProtonMail/go-crypto/openpgp/clearsign"
	"github.com/ProtonMail/go-crypto/openpgp/packet"
)

// sealEngine has three mailboxes on file (rcv, snd, other); stranger's key
// lives in a second engine this one has never seen.
func sealEngine(t *testing.T) (e *Engine, snd, other, stranger *openpgp.Entity, rcvPub *openpgp.Entity) {
	t.Helper()
	e = newTestEngine(t)
	for _, u := range []string{"rcv", "snd", "other"} {
		if _, err := e.GenerateKeypair(&PGPUser{UserID: u, Name: u, Email: u + "@example.com"}); err != nil {
			t.Fatal(err)
		}
	}
	far := newTestEngine(t)
	if _, err := far.GenerateKeypair(&PGPUser{UserID: "x", Name: "Stranger", Email: "stranger@elsewhere.test"}); err != nil {
		t.Fatal(err)
	}
	var err error
	if snd, err = e.entity("snd"); err != nil {
		t.Fatal(err)
	}
	if other, err = e.entity("other"); err != nil {
		t.Fatal(err)
	}
	if stranger, err = far.entity("x"); err != nil {
		t.Fatal(err)
	}
	pk, err := e.GetPublicKey("rcv@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if rcvPub, err = entityFromArmor(pk.Armor); err != nil {
		t.Fatal(err)
	}
	return e, snd, other, stranger, rcvPub
}

func TestTheSealJudgesAnEncryptedMessage(t *testing.T) {
	e, snd, other, stranger, rcv := sealEngine(t)
	msg := []byte("Saturday works.")
	for _, c := range []struct {
		name   string
		signer *openpgp.Entity
		claims string
		want   SigVerdict
	}{
		{"signed by the sender", snd, "snd@example.com", SigVerified},
		{"not signed", nil, "snd@example.com", SigNone},
		{"signed, sender's key not on file", stranger, "stranger@elsewhere.test", SigUnknownKey},
		{"signed by another key than the sender's", other, "snd@example.com", SigBad},
		{"signed by a stranger claiming to be the sender", stranger, "snd@example.com", SigBad},
	} {
		ct, err := encryptTo(msg, []*openpgp.Entity{rcv}, c.signer)
		if err != nil {
			t.Fatal(err)
		}
		body, got, err := e.DecryptAndCheck(ct, "rcv@example.com", c.claims)
		if err != nil || !bytes.Equal(body, msg) {
			t.Fatalf("%s: body %q, err %v", c.name, body, err)
		}
		if got != c.want {
			t.Errorf("%s: verdict %d, want %d", c.name, got, c.want)
		}
	}
}

// The two ways a signature by a key the ring holds is still not the sender's:
// the recipient's own key (a message they once signed, replayed with someone
// else's From), and the sender's own key on a signature that has expired.
func TestTheSealIsNotFooledByAKeyItHolds(t *testing.T) {
	e, snd, _, _, rcv := sealEngine(t)
	own, err := e.entity("rcv")
	if err != nil {
		t.Fatal(err)
	}
	ct, err := encryptTo([]byte("replayed"), []*openpgp.Entity{rcv}, own)
	if err != nil {
		t.Fatal(err)
	}
	if _, got, _ := e.DecryptAndCheck(ct, "rcv@example.com", "snd@example.com"); got != SigBad {
		t.Errorf("signed with the recipient's own key, claiming the sender: %d", got)
	}

	var b bytes.Buffer
	aw, _ := armor.Encode(&b, "PGP MESSAGE", nil)
	// A signature that lives one second, read two seconds later.
	w, err := openpgp.Encrypt(aw, []*openpgp.Entity{rcv}, snd, nil, &packet.Config{SigLifetimeSecs: 1})
	if err != nil {
		t.Fatal(err)
	}
	_, _ = w.Write([]byte("stale"))
	_ = w.Close()
	_ = aw.Close()
	time.Sleep(2 * time.Second)
	if _, got, err := e.DecryptAndCheck(b.Bytes(), "rcv@example.com", "snd@example.com"); err != nil || got != SigBad {
		t.Errorf("the sender's signature, expired: %d (%v)", got, err)
	}
}

func TestTheSealJudgesADetachedSignature(t *testing.T) {
	e, snd, other, stranger, _ := sealEngine(t)
	data := []byte("Content-Type: text/plain\r\n\r\nSigned words.\r\n")
	sign := func(by *openpgp.Entity) []byte {
		var b bytes.Buffer
		if err := openpgp.ArmoredDetachSign(&b, by, bytes.NewReader(data), nil); err != nil {
			t.Fatal(err)
		}
		return b.Bytes()
	}
	if got := e.CheckDetached(data, sign(snd), "snd@example.com"); got != SigVerified {
		t.Errorf("the sender's own signature: %d", got)
	}
	if got := e.CheckDetached(append([]byte("X"), data...), sign(snd), "snd@example.com"); got != SigBad {
		t.Errorf("a signature over altered content: %d", got)
	}
	if got := e.CheckDetached(data, sign(stranger), "stranger@elsewhere.test"); got != SigUnknownKey {
		t.Errorf("a sender with no key on file: %d", got)
	}
	if got := e.CheckDetached(data, sign(other), "snd@example.com"); got != SigBad {
		t.Errorf("another mailbox's signature claiming the sender: %d", got)
	}
}

func TestTheSealJudgesAClearSignedMessage(t *testing.T) {
	e, snd, _, stranger, _ := sealEngine(t)
	clear := func(by *openpgp.Entity, text string) []byte {
		var b bytes.Buffer
		w, err := clearsign.Encode(&b, by.PrivateKey, nil)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte(text))
		_ = w.Close()
		return b.Bytes()
	}
	signed := clear(snd, "Clear-signed words.\n")
	if got := e.CheckClearSigned(signed, "snd@example.com"); got != SigVerified {
		t.Errorf("the sender's clear signature: %d", got)
	}
	if got := e.CheckClearSigned(bytes.Replace(signed, []byte("Clear-signed words."), []byte("Altered words here."), 1), "snd@example.com"); got != SigBad {
		t.Errorf("altered clear-signed text: %d", got)
	}
	if got := e.CheckClearSigned(clear(stranger, "Hi\n"), "stranger@elsewhere.test"); got != SigUnknownKey {
		t.Errorf("a sender with no key on file: %d", got)
	}
	if got := e.CheckClearSigned([]byte("Just words.\n"), "snd@example.com"); got != SigNone {
		t.Errorf("unsigned text: %d", got)
	}
}

// wkdStub answers every WKD request with one binary key.
type wkdStub struct {
	key  []byte
	hits *int
}

func (s wkdStub) RoundTrip(*http.Request) (*http.Response, error) {
	*s.hits++
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(bytes.NewReader(s.key))}, nil
}

// The compose sheet's lock and line say what the send will do, so
// CanEncryptTo must agree with Encrypt for every kind of recipient: a key on
// file, a key only the recipient's own WKD holds, and no key anywhere.
func TestCanEncryptToResolvesAsTheSendDoes(t *testing.T) {
	e, _, _, stranger, _ := sealEngine(t)
	var key bytes.Buffer
	if err := stranger.Serialize(&key); err != nil {
		t.Fatal(err)
	}
	hits := 0
	e.wkdClient = &http.Client{Transport: wkdStub{key.Bytes(), &hits}}
	for _, c := range []struct {
		addr string
		want bool
	}{
		{"snd@example.com", true},         // on file
		{"stranger@elsewhere.test", true}, // its own WKD has it
		{"other@elsewhere.test", false},   // WKD answers with a key that is not theirs
		{"nobody@localhost", false},       // nothing on file, and no WKD to ask
	} {
		got := e.CanEncryptTo(c.addr)
		_, err := e.Encrypt([]byte("x"), c.addr)
		if got != c.want || got != (err == nil) {
			t.Errorf("%s: CanEncryptTo %v, Encrypt err %v; want %v from both", c.addr, got, err, c.want)
		}
	}
	if hits == 0 {
		t.Error("no WKD request was made: the WKD case was not exercised")
	}
}
