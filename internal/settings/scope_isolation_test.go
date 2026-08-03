// SPDX-License-Identifier: Apache-2.0

package settings

import (
	"context"
	"testing"
)

// ADR-0153 Phase 2 — the phase where a hosted domain's settings stop being the
// operator's.
//
// These are written from the position of the operator who reported it: they set
// up test.johal.in, opened Theme Studio, and were editing johal.in.

// The headline property. Two scopes, one key, two values — and neither can see
// the other's.
func TestTwoDomainsHoldDifferentValuesForTheSameKey(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	a, b := ForDomain("client-one.example"), ForDomain("client-two.example")

	for sc, name := range map[Scope]string{
		ForPrimary(): "The Studio",
		a:            "Client One Ltd",
		b:            "Client Two GmbH",
	} {
		if err := s.SetMany(ctx, sc, map[string]string{KeySiteName: name}); err != nil {
			t.Fatalf("SetMany(%s): %v", sc, err)
		}
	}

	for sc, want := range map[Scope]string{
		ForPrimary(): "The Studio",
		a:            "Client One Ltd",
		b:            "Client Two GmbH",
	} {
		if got := s.Get(ctx, sc, KeySiteName); got != want {
			t.Errorf("%s reads %q, want %q — the scopes are not isolated", sc, got, want)
		}
	}
}

// The decision the operator made (D2), tested rather than assumed: an unset key
// resolves to the PRODUCT DEFAULT, never to the primary's stored value.
//
// This is the difference between a hosted domain being a site and being a skin.
// If it inherited, a client's theme would silently track the operator's, which
// is the behaviour that produced the original complaint.
func TestAnUnsetKeyFallsBackToTheProductDefaultNotThePrimary(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	// The operator sets a distinctive theme on their own install.
	if err := s.SetMany(ctx, ForPrimary(), map[string]string{
		KeyThemePrimaryDark: "#ff0000",
		KeySiteName:         "The Studio",
	}); err != nil {
		t.Fatal(err)
	}

	client := ForDomain("client.example")
	if got := s.Get(ctx, client, KeyThemePrimaryDark); got == "#ff0000" {
		t.Error("a hosted domain with no theme of its own inherited the OPERATOR's colour. " +
			"That is the defect ADR-0153 exists to remove: the client's site keeps looking " +
			"like the studio's, and the operator cannot tell which values are really theirs")
	}
	if got, want := s.Get(ctx, client, KeyThemePrimaryDark), Defaults[KeyThemePrimaryDark]; got != want {
		t.Errorf("unset key = %q, want the compiled-in default %q", got, want)
	}
	if got := s.Get(ctx, client, KeySiteName); got != Defaults[KeySiteName] {
		t.Errorf("unset site name = %q, want the default — a new domain is a clean install", got)
	}
}

// Writing one scope must not touch another. The upsert keys on (scope,key); if
// it keyed on (key) alone, the last domain to save would overwrite every other
// domain's value for that setting and report success.
func TestWritingOneScopeLeavesTheOthersUntouched(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	a, b := ForDomain("a.example"), ForDomain("b.example")

	if err := s.SetMany(ctx, ForPrimary(), map[string]string{KeySiteTagline: "studio tagline"}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetMany(ctx, a, map[string]string{KeySiteTagline: "a tagline"}); err != nil {
		t.Fatal(err)
	}
	// b writes the SAME key. Under a (key)-only primary key this overwrites both.
	if err := s.SetMany(ctx, b, map[string]string{KeySiteTagline: "b tagline"}); err != nil {
		t.Fatal(err)
	}

	for sc, want := range map[Scope]string{
		ForPrimary(): "studio tagline",
		a:            "a tagline",
		b:            "b tagline",
	} {
		if got := s.Get(ctx, sc, KeySiteTagline); got != want {
			t.Errorf("after three scopes wrote the same key, %s reads %q, want %q", sc, got, want)
		}
	}
}

// An install that has never had a second domain must read exactly as before.
// Every row written before migration 082 is the operator's, and the migration
// backfills them to the primary — so a single-domain install sees no change at
// all, which is the promise made to every existing installation.
func TestASingleDomainInstallIsUnchanged(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	// Simulate rows that predate the scope column: migration 082 gives them
	// scope=''. Writing through the primary scope produces exactly that.
	want := map[string]string{
		KeySiteName:         "My Blog",
		KeySiteTagline:      "words about things",
		KeyThemePrimaryDark: "#123456",
	}
	if err := s.SetMany(ctx, ForPrimary(), want); err != nil {
		t.Fatal(err)
	}
	for k, v := range want {
		if got := s.Get(ctx, ForPrimary(), k); got != v {
			t.Errorf("primary %s = %q, want %q", k, got, v)
		}
	}
	// And they really are stored under the primary's sentinel, so the migration's
	// backfill target and the store's write target agree.
	var n int
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(1) FROM site_settings WHERE scope=''`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != len(want) {
		t.Errorf("%d row(s) under scope='', want %d — the backfill sentinel and the write "+
			"path disagree, so an upgraded install would not find its own settings", n, len(want))
	}
}

// A cached read must not survive a write to the same scope, and must survive a
// write to a different one.
func TestACachedScopeSeesItsOwnWritesAndOnlyItsOwn(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	client := ForDomain("client.example")

	_ = s.Get(ctx, ForPrimary(), KeySiteName) // warm
	_ = s.Get(ctx, client, KeySiteName)       // warm

	if err := s.SetMany(ctx, client, map[string]string{KeySiteName: "Client Ltd"}); err != nil {
		t.Fatal(err)
	}
	if got := s.Get(ctx, client, KeySiteName); got != "Client Ltd" {
		t.Errorf("the writing scope still reads %q from a stale cache", got)
	}
	if got := s.Get(ctx, ForPrimary(), KeySiteName); got == "Client Ltd" {
		t.Error("a hosted domain's save leaked into the primary's cached settings")
	}
}
