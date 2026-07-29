// SPDX-License-Identifier: Apache-2.0

package settings

import "testing"

// A default is a decision made on behalf of every operator who never opens the
// panel — including, on upgrade, every existing install that never saved these
// keys. These tests pin which of the three availability gates ships on and, more
// importantly, why the other two do not.

// TestLoadShedDefaultsOn — the one availability gate that is safe to default on.
// It caps concurrent in-flight requests and answers a cheap 503 with Retry-After
// when the process is genuinely saturated. It has NO per-visitor keying, so it
// cannot single anyone out, cannot be wrong about who a visitor is, and cannot
// lock a reader out. Without it, a default install has nothing at all standing
// between a flood and process collapse.
func TestLoadShedDefaultsOn(t *testing.T) {
	if got := Defaults[KeyShieldLoadShed]; got != "on" {
		t.Errorf("%s defaults to %q, want \"on\" — a default install has nothing stopping "+
			"the process being driven into collapse", KeyShieldLoadShed, got)
	}
}

// TestRateLimitDoesNotDefaultOn is the one that needs the reasoning attached,
// because rate limiting looks just as harmless as load shedding and is not.
//
// It keys on the client address. On a proxied origin that has not set "Behind a
// CDN", every visitor resolves to a handful of edge addresses, so the whole
// audience shares one bucket and the default 120 requests a minute is nothing.
// Defaulting it on would take exactly the installs that have never opened this
// panel — the ones least likely to have set that switch — and show all of their
// readers a 429.
func TestRateLimitDoesNotDefaultOn(t *testing.T) {
	if got := Defaults[KeyShieldRateLimit]; got != "off" {
		t.Errorf("%s defaults to %q. On a proxied origin without shield.behind_cdn set, "+
			"per-IP limits measure the CDN edge and not the reader, so this would show a "+
			"429 to every visitor of every install that has never opened the panel.", KeyShieldRateLimit, got)
	}
}

// TestAutoBlockDoesNotDefaultOn — auto-block is the punitive gate: it jails a
// source and feeds the kernel offload, which drops packets outside this process.
// Observe-only mode now exists precisely so an operator can watch what it would
// have done to their own traffic before it does it to anyone.
func TestAutoBlockDoesNotDefaultOn(t *testing.T) {
	if got := Defaults[KeyShieldAutoBlock]; got != "off" {
		t.Errorf("%s defaults to %q — the punitive gate must be an operator's decision, "+
			"taken after an observe-only trial, not a silent upgrade", KeyShieldAutoBlock, got)
	}
}

// TestObserveModeDoesNotDefaultOn — a shield that ships observing is a shield
// that ships off, with a reassuring label on it.
func TestObserveModeDoesNotDefaultOn(t *testing.T) {
	if got := Defaults[KeyShieldObserve]; got != "off" {
		t.Errorf("%s defaults to %q — the site would ship undefended", KeyShieldObserve, got)
	}
}

// TestEveryShieldDefaultIsAllowlisted — a default for a key the writer rejects
// would be applied on read and refused on save, which is the kind of split an
// operator experiences as "the panel does not remember my setting".
func TestEveryShieldDefaultIsAllowlisted(t *testing.T) {
	for k := range Defaults {
		if !AllKeys[k] {
			t.Errorf("key %q has a default but is not in AllKeys — it can be read and never written", k)
		}
	}
}
