// SPDX-License-Identifier: Apache-2.0

package gossip

import (
	"crypto/sha256"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func testKey(t *testing.T) [32]byte {
	t.Helper()
	k, err := DeriveKey("a-shared-install-secret")
	if err != nil {
		t.Fatalf("derive: %v", err)
	}
	return k
}

// TestTheGossipKeyIsNotTheSharedSecret is the whole reason this package derives
// anything. API_KEY also guards the MCP server and the REST API, so handing it
// raw to N edge nodes would mean one compromised edge compromises all three
// everywhere. A node must be able to authenticate verdicts and be unable to
// recover the secret behind them.
func TestTheGossipKeyIsNotTheSharedSecret(t *testing.T) {
	const secret = "a-shared-install-secret"
	k, err := DeriveKey(secret)
	if err != nil {
		t.Fatalf("derive: %v", err)
	}
	if string(k[:]) == secret || strings.Contains(string(k[:]), secret) {
		t.Fatal("the derived key contains the shared secret, so an edge node holding it holds " +
			"the credential that also guards the API and the MCP server")
	}
	// Deterministic across nodes: they must agree on a key without talking to
	// each other, which is the only reason this can work at all.
	k2, _ := DeriveKey(secret)
	if k != k2 {
		t.Fatal("derivation is not deterministic, so two nodes would never agree on a key")
	}
	// A different secret must give an unrelated key.
	k3, _ := DeriveKey(secret + "x")
	if k == k3 {
		t.Fatal("two different secrets derived the same key")
	}
	// And it is not a bare hash of the secret either — that would be trivially
	// reversible from a rainbow table for a weak operator secret.
	if h := sha256.Sum256([]byte(secret)); h == k {
		t.Error("the key is a plain SHA-256 of the secret rather than an HKDF derivation")
	}
}

// TestAnEmptySecretRefusesRatherThanDerivingFromNothing — an install with no
// shared secret must not quietly run gossip under a key every other install with
// no secret would also compute.
func TestAnEmptySecretRefusesRatherThanDerivingFromNothing(t *testing.T) {
	if _, err := DeriveKey(""); err == nil {
		t.Fatal("an empty secret produced a key; every install without a secret would then " +
			"share one well-known gossip key and accept each other's verdicts")
	}
}

// TestRoundTrip is the happy path.
func TestRoundTrip(t *testing.T) {
	k := testKey(t)
	now := time.Now()
	in := Message{Node: "edge-1", Issued: now.Unix(), Verdicts: []Verdict{
		{Kind: KindJail, Source: "198.51.100.9"},
		{Kind: KindSuspect, Source: "203.0.113.4", Weight: 0.3},
		{Kind: KindPardon, Source: "192.0.2.7"},
	}}
	b, err := Seal(k, in)
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	out, err := Open(k, b, in.Issued, now)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if out.Node != "edge-1" || len(out.Verdicts) != 3 {
		t.Fatalf("round trip lost data: %+v", out)
	}
	if out.Verdicts[1].Weight != 0.3 {
		t.Errorf("weight = %v, want 0.3", out.Verdicts[1].Weight)
	}
}

// TestVisitorAddressesAreNotOnTheWireInClear — messages carry the IP addresses
// of real visitors. Authenticating them and leaving them readable would put the
// audience of a product that ships a Tor Space in cleartext between every pair
// of nodes.
func TestVisitorAddressesAreNotOnTheWireInClear(t *testing.T) {
	k := testKey(t)
	now := time.Now()
	m := Message{Node: "edge-1", Issued: now.Unix(),
		Verdicts: []Verdict{{Kind: KindJail, Source: "198.51.100.9"}}}
	b, _ := Seal(k, m)
	if strings.Contains(string(b), "198.51.100.9") {
		t.Error("a visitor's address is readable in the sealed message — the payload is signed " +
			"rather than encrypted, and anyone on the path between two nodes learns who visits")
	}
	if strings.Contains(string(b), "edge-1") {
		t.Error("the node name is readable, so the topology of the fleet is on the wire")
	}
}

// TestAForgedMessageIsRefused — the only thing standing between a stranger and
// the fleet's jails is this tag.
func TestAForgedMessageIsRefused(t *testing.T) {
	k := testKey(t)
	other, _ := DeriveKey("someone-elses-secret")
	now := time.Now()
	m := Message{Node: "attacker", Issued: now.Unix(),
		Verdicts: []Verdict{{Kind: KindJail, Source: "198.51.100.9"}}}

	b, _ := Seal(other, m)
	if _, err := Open(k, b, m.Issued, now); err != ErrNotAuthentic {
		t.Errorf("a message sealed with a different key opened with %v, want ErrNotAuthentic", err)
	}

	// A single flipped bit anywhere must fail, including in the nonce prefix.
	good, _ := Seal(k, m)
	for _, pos := range []int{0, nonceLen, len(good) - 1} {
		tampered := append([]byte{}, good...)
		tampered[pos] ^= 0x01
		if _, err := Open(k, tampered, m.Issued, now); err == nil {
			t.Errorf("a message with byte %d flipped was accepted", pos)
		}
	}
}

// TestTheTimestampCannotBeEditedToExtendAMessage — the timestamp is sealed as
// additional data as well as carried in the payload. If it were only a header,
// a captured message could be re-dated and replayed indefinitely.
func TestTheTimestampCannotBeEditedToExtendAMessage(t *testing.T) {
	k := testKey(t)
	issued := time.Now().Add(-2 * MaxAge)
	m := Message{Node: "edge-1", Issued: issued.Unix(),
		Verdicts: []Verdict{{Kind: KindJail, Source: "198.51.100.9"}}}
	b, _ := Seal(k, m)

	// Honest delivery: correctly refused as stale.
	if _, err := Open(k, b, m.Issued, time.Now()); err != ErrStale {
		t.Errorf("a two-window-old message opened with %v, want ErrStale", err)
	}
	// An attacker presenting the same bytes with a fresh timestamp must fail
	// authentication, not merely freshness.
	if _, err := Open(k, b, time.Now().Unix(), time.Now()); err != ErrNotAuthentic {
		t.Errorf("re-dating a captured message gave %v — the timestamp is not bound into the "+
			"tag, so any captured message is replayable forever", err)
	}
}

// TestAFutureDatedMessageIsRefused — without a skew bound, one message dated
// years ahead is inside the freshness window forever, so an attacker who
// captures it can replay it for as long as they hold it.
func TestAFutureDatedMessageIsRefused(t *testing.T) {
	k := testKey(t)
	now := time.Now()
	future := now.Add(365 * 24 * time.Hour)
	m := Message{Node: "edge-1", Issued: future.Unix(),
		Verdicts: []Verdict{{Kind: KindJail, Source: "198.51.100.9"}}}
	b, _ := Seal(k, m)
	if _, err := Open(k, b, m.Issued, now); err != ErrStale {
		t.Errorf("a message dated a year ahead opened with %v — it would stay 'fresh' "+
			"indefinitely and be replayable for as long as anyone held it", err)
	}
	// A small skew must still be tolerated; node clocks are never exactly equal.
	near := Message{Node: "edge-1", Issued: now.Add(3 * time.Second).Unix(),
		Verdicts: []Verdict{{Kind: KindJail, Source: "198.51.100.9"}}}
	nb, _ := Seal(k, near)
	if _, err := Open(k, nb, near.Issued, now); err != nil {
		t.Errorf("a message three seconds ahead was refused (%v) — real node clocks differ and "+
			"this would drop legitimate traffic", err)
	}
}

// TestReplayIsRefusedOnce — a captured, still-fresh message replayed inside its
// window is the one attack the tag cannot stop by itself.
func TestReplayIsRefusedOnce(t *testing.T) {
	seen := NewSeen(1024)
	now := time.Now()
	if !seen.Fresh("nonce-a", now) {
		t.Fatal("the first delivery was rejected")
	}
	if seen.Fresh("nonce-a", now) {
		t.Error("the same nonce was accepted twice — a captured message can be replayed inside " +
			"its freshness window")
	}
	if !seen.Fresh("nonce-b", now) {
		t.Error("a different nonce was rejected")
	}
	if seen.Fresh("", now) {
		t.Error("an empty nonce was accepted, so a message with no nonce replays freely")
	}
}

// TestReplayCacheIsFixedMemory — the key is a value a PEER chooses, so a cache
// that grew with traffic is a cache an attacker sizes.
func TestReplayCacheIsFixedMemory(t *testing.T) {
	seen := NewSeen(256)
	now := time.Now()
	for i := 0; i < 100_000; i++ {
		seen.Fresh("n"+strconv.Itoa(i), now)
	}
	if n := seen.Len(); n > 256 {
		t.Errorf("the cache holds %d entries against a cap of 256 — a peer chooses the keys, so "+
			"this is memory an attacker sizes", n)
	}
}

// TestAFullCacheRefusesRatherThanForgetting — evicting a live nonce to make room
// re-opens the replay it exists to close. Refusing costs a peer one message it
// will send again; forgetting costs the guarantee.
func TestAFullCacheRefusesRatherThanForgetting(t *testing.T) {
	seen := NewSeen(4)
	now := time.Now()
	for i := 0; i < 4; i++ {
		if !seen.Fresh("live"+strconv.Itoa(i), now) {
			t.Fatalf("setup: entry %d rejected", i)
		}
	}
	if seen.Fresh("overflow", now) {
		t.Error("a full cache admitted a new nonce, which means it dropped a live one")
	}
	// Once the window has passed, the entries are no longer replayable and the
	// space is genuinely free.
	later := now.Add(MaxAge + MaxSkew + 2*time.Second)
	if !seen.Fresh("after-window", later) {
		t.Error("the cache never recovers space, so it wedges permanently after the first burst")
	}
}

// TestConcurrentReplayChecksAreAtomic — the same replayed message can arrive on
// two connections at once. A check-then-claim with a gap between them would
// admit both.
func TestConcurrentReplayChecksAreAtomic(t *testing.T) {
	for round := 0; round < 200; round++ {
		seen := NewSeen(1024)
		now := time.Now()
		var wg sync.WaitGroup
		var mu sync.Mutex
		accepted := 0
		for i := 0; i < 32; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if seen.Fresh("contended", now) {
					mu.Lock()
					accepted++
					mu.Unlock()
				}
			}()
		}
		wg.Wait()
		if accepted != 1 {
			t.Fatalf("round %d: %d of 32 concurrent deliveries of one nonce were accepted, "+
				"want exactly 1", round, accepted)
		}
	}
}

// TestAPeerCannotMakeTheReceiverExpensive — limits are enforced on the RECEIVING
// side. Trusting the sender to have checked would be trusting a party that may
// be the compromised one.
func TestAPeerCannotMakeTheReceiverExpensive(t *testing.T) {
	k := testKey(t)
	now := time.Now()

	// Refused at the sender...
	big := Message{Node: "edge-1", Issued: now.Unix()}
	for i := 0; i < MaxVerdicts+1; i++ {
		big.Verdicts = append(big.Verdicts, Verdict{Kind: KindJail, Source: "198.51.100.9"})
	}
	if _, err := Seal(k, big); err != ErrTooMany {
		t.Errorf("sealing an oversized batch gave %v, want ErrTooMany", err)
	}

	// ...and at the receiver, which is the side that matters.
	if _, err := Open(k, make([]byte, MaxMessageBytes+1), now.Unix(), now); err != ErrTooLarge {
		t.Errorf("an oversized message opened with %v, want ErrTooLarge", err)
	}
	// A runt cannot be allowed to index past the nonce prefix.
	for _, n := range []int{0, 1, nonceLen, nonceLen + 15} {
		if _, err := Open(k, make([]byte, n), now.Unix(), now); err == nil {
			t.Errorf("a %d-byte message was accepted", n)
		}
	}
}

// TestAnUnsetKindIsNotAVerdict — the zero value of Kind is deliberately not
// valid, so a message that omits the field cannot mean whatever the first
// constant happens to be. Same discipline as the enforcement-rule registry.
func TestAnUnsetKindIsNotAVerdict(t *testing.T) {
	k := testKey(t)
	now := time.Now()
	m := Message{Node: "edge-1", Issued: now.Unix(), Verdicts: []Verdict{
		{Source: "198.51.100.1"},                  // no kind
		{Kind: KindJail},                          // no source
		{Kind: Kind(200), Source: "198.51.100.2"}, // unknown kind
		{Kind: KindJail, Source: "198.51.100.3"},  // the only good one
	}}
	b, _ := Seal(k, m)
	out, err := Open(k, b, m.Issued, now)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if len(out.Verdicts) != 1 {
		t.Fatalf("kept %d verdicts, want 1: %+v", len(out.Verdicts), out.Verdicts)
	}
	if out.Verdicts[0].Source != "198.51.100.3" {
		t.Errorf("kept the wrong verdict: %+v", out.Verdicts[0])
	}
	if kindUnset.Valid() {
		t.Error("the zero Kind reports itself valid, so an omitted field is a silent verdict")
	}
}

// TestAMessageWithNoOriginIsRefused — Node is what per-peer rate limiting is
// keyed on. An unnamed message cannot be accounted to anyone, so a compromised
// node could evade its own budget by omitting it.
func TestAMessageWithNoOriginIsRefused(t *testing.T) {
	k := testKey(t)
	if _, err := Seal(k, Message{Issued: time.Now().Unix()}); err != ErrNoNode {
		t.Errorf("sealing an unnamed message gave %v, want ErrNoNode", err)
	}
}

// TestThereIsNoForwardingField pins an absence. A hop count or TTL is the seed
// of a relaying mesh, and a relaying mesh is an amplifier: one message in, N
// out, and a loop in the peer graph is a self-inflicted flood on the machines
// already under attack. If someone adds the field, they have to delete this
// test and explain why.
func TestThereIsNoForwardingField(t *testing.T) {
	k := testKey(t)
	now := time.Now()
	m := Message{Node: "edge-1", Issued: now.Unix(),
		Verdicts: []Verdict{{Kind: KindJail, Source: "198.51.100.9"}}}
	b, _ := Seal(k, m)
	out, err := Open(k, b, m.Issued, now)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	// Reflection: the Message type must expose nothing that could carry a hop
	// budget. Checked structurally so adding one is a failing test.
	for _, f := range []string{"TTL", "Hops", "Hop", "Forward", "Relay", "MaxHops"} {
		if hasField(out, f) {
			t.Errorf("Message has a %q field. Verdicts are pushed directly to configured peers "+
				"and never relayed, because a relaying mesh amplifies one message into N and a "+
				"loop in the peer graph floods the machines already under attack", f)
		}
	}
}

// hasField reports whether the Message struct declares a field by that name.
// Reflection rather than a grep so the check is against the type, not the file.
func hasField(_ Message, name string) bool {
	t := reflect.TypeOf(Message{})
	for i := 0; i < t.NumField(); i++ {
		if strings.EqualFold(t.Field(i).Name, name) {
			return true
		}
	}
	return false
}
