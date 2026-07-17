package vayutor

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"strings"
	"testing"
	"time"

	"filippo.io/edwards25519"
	"golang.org/x/crypto/sha3"
)

// decodeAndVerifyOnion decodes a v3 .onion address and re-verifies its checksum
// and version exactly as a Tor client does. It returns the 32-byte public key.
// A derivation bug (wrong checksum, wrong layout) fails here — which is precisely
// what would make Tor reject the address.
func decodeAndVerifyOnion(t *testing.T, addr string) []byte {
	t.Helper()
	if !strings.HasSuffix(addr, ".onion") {
		t.Fatalf("address %q missing .onion suffix", addr)
	}
	core := strings.TrimSuffix(addr, ".onion")
	if len(core) != 56 {
		t.Fatalf("v3 onion core must be 56 chars, got %d (%q)", len(core), core)
	}
	raw, err := onionB32.DecodeString(strings.ToUpper(core))
	if err != nil {
		t.Fatalf("base32 decode %q: %v", core, err)
	}
	if len(raw) != 35 {
		t.Fatalf("decoded onion must be 35 bytes, got %d", len(raw))
	}
	pub, cs, ver := raw[:32], raw[32:34], raw[34]
	if ver != onionVersion {
		t.Fatalf("version byte = %d, want %d", ver, onionVersion)
	}
	h := sha3.New256()
	_, _ = h.Write([]byte(".onion checksum"))
	_, _ = h.Write(pub)
	_, _ = h.Write([]byte{onionVersion})
	want := h.Sum(nil)[:2]
	if cs[0] != want[0] || cs[1] != want[1] {
		t.Fatalf("checksum mismatch: addr has %x, recomputed %x", cs, want)
	}
	return pub
}

// assertBlobMatchesPub decodes the "ED25519-V3:<base64>" blob, takes its 32-byte
// scalar half, and checks scalar·B equals the public key from the address — the
// invariant tor relies on to bring the same .onion back from a stored key.
func assertBlobMatchesPub(t *testing.T, blob string, wantPub []byte) {
	t.Helper()
	b64 := strings.TrimPrefix(blob, "ED25519-V3:")
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil || len(raw) != 64 {
		t.Fatalf("bad key blob (len %d, err %v)", len(raw), err)
	}
	s, err := edwards25519.NewScalar().SetBytesWithClamping(raw[:32])
	if err != nil {
		t.Fatalf("scalar from blob: %v", err)
	}
	got := edwards25519.NewIdentityPoint().ScalarBaseMult(s).Bytes()
	if string(got) != string(wantPub) {
		t.Fatalf("blob-derived pubkey != address pubkey\n got %x\nwant %x", got, wantPub)
	}
}

func TestDeriveOnionValidAndDeterministic(t *testing.T) {
	for i := 0; i < 20; i++ {
		seed := make([]byte, 32)
		if _, err := rand.Read(seed); err != nil {
			t.Fatal(err)
		}
		addr, blob := deriveOnion(seed)
		if addr == "" || blob == "" {
			t.Fatal("deriveOnion returned empty result")
		}
		if !strings.HasPrefix(blob, "ED25519-V3:") {
			t.Errorf("key blob missing ED25519-V3 prefix: %q", blob)
		}
		pub := decodeAndVerifyOnion(t, addr) // checksum/version/layout must be valid
		// The stored key blob must re-derive the SAME public key as the address —
		// i.e. tor, given this blob, reproduces exactly this .onion.
		assertBlobMatchesPub(t, blob, pub)
		// Deterministic: same seed → same address + key.
		addr2, blob2 := deriveOnion(seed)
		if addr2 != addr || blob2 != blob {
			t.Errorf("deriveOnion not deterministic for a fixed seed")
		}
	}
}

func TestValidVanityPrefix(t *testing.T) {
	good := map[string]string{"a": "a", "ABC": "abc", "vayu": "vayu", "2345": "2345", "  Vay  ": "vay"}
	for in, want := range good {
		if got, ok := validVanityPrefix(in); !ok || got != want {
			t.Errorf("validVanityPrefix(%q) = %q,%v want %q,true", in, got, ok, want)
		}
	}
	bad := []string{"", "0abc", "1abc", "abc8", "hello9", "up-per", "toolongprefix", "aaaaaaaa"} // 0,1,8,9 not base32; '-' invalid; >7 too long
	for _, in := range bad {
		if _, ok := validVanityPrefix(in); ok {
			t.Errorf("validVanityPrefix(%q) accepted, want rejected", in)
		}
	}
}

func TestStartVanityValidation(t *testing.T) {
	e := NewEngine(Config{Enabled: true})
	// Unknown host → rejected.
	if err := e.StartVanity("nope.in", "ab"); err != errVanityHost {
		t.Errorf("unknown host err = %v, want errVanityHost", err)
	}
	// Bad prefix → rejected before host check.
	if err := e.StartVanity("nope.in", "!!"); err != errVanityPrefix {
		t.Errorf("bad prefix err = %v, want errVanityPrefix", err)
	}
}

func TestVanitySearchFindsAndApplies(t *testing.T) {
	store := newMemStore()
	e := NewEngine(Config{Enabled: true, Store: store})
	// Pretend the domain already has a (random) live onion.
	e.mu.Lock()
	e.onionByHost["blog.in"] = "oldrandomaddressxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx.onion"
	e.hostByOnion["oldrandomaddressxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx.onion"] = "blog.in"
	e.mu.Unlock()

	// A 1-char prefix averages ~32 tries — effectively instant.
	if err := e.StartVanity("blog.in", "a"); err != nil {
		t.Fatalf("StartVanity: %v", err)
	}
	deadline := time.Now().Add(15 * time.Second)
	for {
		st := e.VanityStatus()
		if st.Found {
			if !strings.HasPrefix(st.Address, "a") {
				t.Fatalf("found address %q does not start with prefix", st.Address)
			}
			decodeAndVerifyOnion(t, st.Address)
			// The winning identity must be persisted for republication.
			recs, _ := store.LoadOnions(context.Background())
			var got string
			for _, r := range recs {
				if r.Host == "blog.in" {
					got = r.Address
				}
			}
			if got != st.Address {
				t.Fatalf("persisted address %q != found %q", got, st.Address)
			}
			// The live registry entry was torn down so reconcile republishes it.
			e.mu.RLock()
			_, stillLive := e.onionByHost["blog.in"]
			e.mu.RUnlock()
			if stillLive {
				t.Error("old onion should have been cleared from the live registry")
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("vanity search did not finish; tries=%d", st.Tries)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
