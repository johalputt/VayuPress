// SPDX-License-Identifier: Apache-2.0

package safefetch

import (
	"errors"
	"net"
	"testing"
)

// The failure this exists for, in the exact shape the install produced:
//
//	lookup ip-ranges.amazonaws.com on 127.0.0.53:53: read udp …: i/o timeout
//
// The system resolver could not be reached. That is the only case where asking
// somebody else is defensible.
func TestOnlyATransportFailureJustifiesAskingAnotherResolver(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"the observed failure: stub resolver timing out",
			&net.DNSError{Err: "read udp 127.0.0.1:53095->127.0.0.53:53: i/o timeout", Name: "x", IsTimeout: true}, true},
		{"resolver refusing connections",
			&net.DNSError{Err: "connection refused", Name: "x"}, true},
		{"temporary server failure",
			&net.DNSError{Err: "server misbehaving", Name: "x", IsTemporary: true}, true},

		// The one that must NOT fall through. NXDOMAIN is an ANSWER. Asking a
		// second resolver until a name resolves is how a mistyped hostname ends
		// up connecting to a stranger.
		{"the name genuinely does not exist",
			&net.DNSError{Err: "no such host", Name: "typo.invalid", IsNotFound: true}, false},

		// The case that makes the guard load-bearing: a resolver can report a
		// timeout AND not-found together. "Does not exist" is still an answer,
		// however slowly it arrived, and must win. Without this the first version
		// of these tests could not tell the guard from its absence.
		{"not found, reported alongside a timeout",
			&net.DNSError{Err: "i/o timeout", Name: "typo.invalid", IsNotFound: true, IsTimeout: true}, false},
		{"not found, reported alongside a temporary failure",
			&net.DNSError{Err: "server misbehaving", Name: "typo.invalid", IsNotFound: true, IsTemporary: true}, false},

		{"not a DNS error at all", errors.New("context canceled"), false},
		{"no error", nil, false},
	}
	for _, c := range cases {
		if got := transportFailure(c.err); got != c.want {
			t.Errorf("%s: transportFailure = %v, want %v", c.name, got, c.want)
		}
	}
}

// A wrapped error must still be recognised, or the fallback never engages for
// the errors real code paths actually return.
func TestAWrappedResolverFailureIsStillRecognised(t *testing.T) {
	wrapped := &wrapErr{inner: &net.DNSError{Err: "i/o timeout", IsTimeout: true}}
	if !transportFailure(wrapped) {
		t.Fatal("a wrapped DNS timeout was not recognised, so the fallback never runs for it")
	}
}

type wrapErr struct{ inner error }

func (w *wrapErr) Error() string { return "wrapped: " + w.inner.Error() }
func (w *wrapErr) Unwrap() error { return w.inner }

// Tor mode. A direct DNS query to a public resolver is a clearnet callback that
// announces the onion server exists. No stale feed is worth that, so the
// fallback must be refused outright — the lookup fails instead.
func TestTheFallbackIsRefusedWhenClearnetEgressIsBlocked(t *testing.T) {
	// The real stub-resolver failure, which WOULD otherwise engage the fallback.
	timedOut := &net.DNSError{Err: "read udp 127.0.0.1:53095->127.0.0.53:53: i/o timeout",
		Name: "ip-ranges.amazonaws.com", IsTimeout: true}

	if !fallbackPermitted(timedOut) {
		t.Fatal("fixture: this error should normally permit the fallback")
	}

	SetBlockClearnetEgress(true)
	defer SetBlockClearnetEgress(false)
	if fallbackPermitted(timedOut) {
		t.Fatal("a Tor-mode install would query a public resolver directly. That is a clearnet " +
			"callback announcing the onion server exists, and no feed refresh is worth it.")
	}
}

// The operator's switch has to be obeyed at the decision, not merely read.
func TestTheOffSwitchIsObeyedByTheDecisionItself(t *testing.T) {
	timedOut := &net.DNSError{Err: "i/o timeout", Name: "x", IsTimeout: true}
	if !fallbackPermitted(timedOut) {
		t.Fatal("fixture: this error should normally permit the fallback")
	}
	t.Setenv("VAYU_DNS_FALLBACK", "off")
	if fallbackPermitted(timedOut) {
		t.Fatal("an operator who refused the fallback still has queries going off-box")
	}
}

// An operator who would rather fail than send a query off-box must be able to
// say so, and be obeyed.
func TestTheOperatorCanRefuseTheFallbackEntirely(t *testing.T) {
	t.Setenv("VAYU_DNS_FALLBACK", "off")
	if dnsFallbackEnabled() {
		t.Fatal("VAYU_DNS_FALLBACK=off was ignored")
	}
	t.Setenv("VAYU_DNS_FALLBACK", "OFF")
	if dnsFallbackEnabled() {
		t.Fatal("the switch is case-sensitive, so an operator who typed OFF is silently overridden")
	}
	t.Setenv("VAYU_DNS_FALLBACK", "")
	if !dnsFallbackEnabled() {
		t.Fatal("the fallback is off by default, so the broken-resolver install it exists for stays broken")
	}
}

// The state has to be readable, or an install quietly resolving through a third
// party is something an operator can only discover by reading logs they have no
// reason to read.
func TestWhetherTheFallbackIsCarryingLookupsIsReadable(t *testing.T) {
	dnsFallbackUsed.Store(false)
	if DNSFallbackActive() {
		t.Fatal("reported active when it is not")
	}
	dnsFallbackUsed.Store(true)
	if !DNSFallbackActive() {
		t.Fatal("the fallback is carrying lookups and nothing can see that")
	}
	dnsFallbackUsed.Store(false)
}

// The fallback must not use the transport that broke.
//
// The observed failure was UDP to the stub resolver being dropped. A fallback
// that dials UDP would reproduce it against a different address and report the
// same timeout, so the transport is a decision and not an incidental string.
func TestTheFallbackDoesNotUseTheTransportThatFailed(t *testing.T) {
	if fallbackNetwork == "udp" {
		t.Fatal("the fallback dials UDP — the exact path whose failure it exists to work around")
	}
	if fallbackNetwork != "tcp" {
		t.Fatalf("fallbackNetwork = %q; TCP is what survives a dropped UDP path", fallbackNetwork)
	}
}
