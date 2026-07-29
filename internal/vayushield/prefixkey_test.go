// SPDX-License-Identifier: Apache-2.0

package vayushield

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/johalputt/vayupress/internal/vayushield/challenge"
)

// Every durable gate — the rate limiter, the violation meter, the blocklist and
// the reputation brain — used to key on the exact client address. For IPv4 that
// is a scarce resource an attacker pays for. For IPv6 it is not: a routed /64 is
// the standard allocation for a single network, and every address inside it is
// free.
//
// So an attacker on one /64 arrived as a brand-new visitor 2^64 times. No bucket
// was ever drained twice, no jail sentence was ever served, no reputation ever
// accumulated. That is not a weak defence, it is the complete absence of one, and
// it is invisible in testing because a test that reuses one address passes.

// rotatingManager builds a shield whose client IP is whatever the caller last
// set, with a rate limit tight enough to trip immediately.
type rotatingManager struct {
	*Manager
	ip string
}

func newRotatingManager(t *testing.T, groupIPv4 bool) *rotatingManager {
	t.Helper()
	rm := &rotatingManager{}
	rm.Manager = New(Config{
		Enabled:  true,
		Signer:   challenge.NewSigner([]byte("s")),
		ClientIP: func(*http.Request) string { return rm.ip },
	})
	rm.ApplySettings(Settings{
		Enabled: true, PoWThreshold: 0.4, JSThreshold: 0.6, BlockThreshold: 0.8,
		RateLimit: true, RatePerMinute: 1, Burst: 2,
		AutoBlock: true, AutoBlockJailMinutes: 10,
		GroupIPv4: groupIPv4,
	})
	return rm
}

// get sends one request from ip and reports whether it was throttled or jailed.
func (rm *rotatingManager) get(ip string) int {
	rm.ip = ip
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/article", nil)
	req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64) rotating-client")
	rm.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})).ServeHTTP(rec, req)
	return rec.Code
}

// TestIPv6RotationInsideOneSlash64IsCaught is the whole point of the change. The
// attacker changes address on every single request, staying inside one /64 —
// which costs nothing and requires no cooperation from anyone.
func TestIPv6RotationInsideOneSlash64IsCaught(t *testing.T) {
	rm := newRotatingManager(t, false)

	blocked := 0
	const n = 40
	for i := 0; i < n; i++ {
		// A fresh address every request, all inside 2001:db8:1:1::/64.
		if code := rm.get(fmt.Sprintf("[2001:db8:1:1::%x]:443", i+1)); code != http.StatusOK {
			blocked++
		}
	}
	if blocked == 0 {
		t.Errorf("all %d requests from one /64 were served — an attacker with a routed /64 "+
			"is exempt from every durable gate at zero cost", n)
	}
	// The limit is 1 rpm with a burst of 2, so nearly everything should be
	// refused. Pinning "most" rather than an exact number keeps the test about
	// the property, not the bucket arithmetic.
	if blocked < n/2 {
		t.Errorf("only %d/%d requests from one /64 were refused — the group key is not being enforced", blocked, n)
	}
}

// TestSeparateSlash64sAreSeparateIdentities — the fix must not collapse the whole
// of IPv6 into one bucket. Two different /64s are two different networks and one
// must not serve the other's sentence.
func TestSeparateSlash64sAreSeparateIdentities(t *testing.T) {
	rm := newRotatingManager(t, false)

	// Exhaust the first /64.
	for i := 0; i < 20; i++ {
		rm.get(fmt.Sprintf("[2001:db8:1:1::%x]:443", i+1))
	}
	// A visitor on an unrelated /64 must still be served.
	if code := rm.get("[2001:db8:9:9::1]:443"); code != http.StatusOK {
		t.Errorf("an unrelated /64 got %d — one network is serving another's sentence", code)
	}
}

// TestIPv4IsNotGroupedByDefault — behind carrier-grade NAT a /24 can be thousands
// of unrelated subscribers, so grouping it by default would trade a real
// availability guarantee for a marginal gain against an attacker who, unlike the
// IPv6 case, has to pay for every address.
func TestIPv4IsNotGroupedByDefault(t *testing.T) {
	rm := newRotatingManager(t, false)

	for i := 1; i <= 20; i++ {
		rm.get(fmt.Sprintf("198.51.100.%d:443", i))
	}
	// A different address in the same /24 is a different subscriber until the
	// operator says otherwise.
	if code := rm.get("198.51.100.200:443"); code != http.StatusOK {
		t.Errorf("an unrelated IPv4 address in the same /24 got %d with grouping OFF — "+
			"a shared CGNAT range would lock out everyone behind it", code)
	}
}

// TestIPv4GroupingIsHonouredWhenOptedIn — and when the operator has accepted the
// collateral, the /24 must actually be the unit of enforcement.
func TestIPv4GroupingIsHonouredWhenOptedIn(t *testing.T) {
	rm := newRotatingManager(t, true)

	blocked := 0
	for i := 1; i <= 30; i++ {
		if code := rm.get(fmt.Sprintf("198.51.100.%d:443", i)); code != http.StatusOK {
			blocked++
		}
	}
	if blocked == 0 {
		t.Error("with IPv4 grouping ON, rotating through a /24 was still free")
	}
}

// TestEnforcementKeyShape pins the derivation itself, including the cases that
// would silently produce a useless key.
func TestEnforcementKeyShape(t *testing.T) {
	m := New(Config{Enabled: true})
	m.ApplySettings(Settings{Enabled: true})

	cases := []struct{ in, want string }{
		{"2001:db8:1:1::5", "2001:db8:1:1::/64"},
		{"2001:db8:1:1:ffff:ffff:ffff:ffff", "2001:db8:1:1::/64"},
		{"::1", "::/64"},
		// IPv4 stays exact while grouping is off.
		{"198.51.100.7", "198.51.100.7"},
		// An IPv4-mapped IPv6 address is an IPv4 client. Treating it as v6 would
		// group 4-in-6 traffic into a /64 that spans the entire IPv4 space.
		{"::ffff:198.51.100.7", "198.51.100.7"},
		// Anything unparseable is passed through rather than collapsing every
		// unknown source onto one shared key.
		{"not-an-address", "not-an-address"},
		{"", ""},
	}
	for _, c := range cases {
		if got := m.enforcementKey(c.in); got != c.want {
			t.Errorf("enforcementKey(%q) = %q, want %q", c.in, got, c.want)
		}
	}

	m.ApplySettings(Settings{Enabled: true, GroupIPv4: true})
	if got := m.enforcementKey("198.51.100.7"); got != "198.51.100.0/24" {
		t.Errorf("with grouping on, enforcementKey(198.51.100.7) = %q, want 198.51.100.0/24", got)
	}
}

// TestKernelOffloadStillReceivesAnExactAddress — the nft banlist sets are declared
// without `flags interval`, so no CIDR element can be added to them at all.
// Passing a prefix there would not widen the ban, it would be rejected by the
// agent's parser and the offload would silently stop working for IPv6.
func TestKernelOffloadStillReceivesAnExactAddress(t *testing.T) {
	var offloaded []string
	rm := &rotatingManager{}
	rm.Manager = New(Config{
		Enabled:   true,
		Signer:    challenge.NewSigner([]byte("s")),
		ClientIP:  func(*http.Request) string { return rm.ip },
		OffloadFn: func(ip string, _ time.Duration) { offloaded = append(offloaded, ip) },
	})
	rm.ApplySettings(Settings{
		Enabled: true, PoWThreshold: 0.4, JSThreshold: 0.6, BlockThreshold: 0.8,
		RateLimit: true, RatePerMinute: 1, Burst: 2,
		AutoBlock: true, AutoBlockJailMinutes: 10,
	})

	for i := 0; i < 200; i++ {
		rm.get(fmt.Sprintf("[2001:db8:1:1::%x]:443", i+1))
	}
	if len(offloaded) == 0 {
		t.Skip("no offload was triggered in this run — the reputation path did not escalate")
	}
	for _, ip := range offloaded {
		if strings.Contains(ip, "/") {
			t.Errorf("the kernel offload was handed %q — the nft banlist sets have no "+
				"`flags interval` and reject every CIDR element", ip)
		}
	}
}
