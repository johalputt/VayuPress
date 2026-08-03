// SPDX-License-Identifier: Apache-2.0

package settings

import (
	"context"
	"os"
	"strings"
	"testing"
)

// ADR-0153 Phase 1 guards.
//
// Phase 1 plumbs the scope through the API and the cache and deliberately
// changes no behaviour: site_settings has no scope column yet, so every scope
// still resolves to the same rows. These tests pin the properties that must
// hold BEFORE Phase 2 adds the column, because each of them is the difference
// between per-domain settings and a cross-tenant leak.

// An unscoped read must return the product default and never reach the
// database. This is the direction of the fallback, decided in ADR-0153 D2, and
// it is the whole reason a hosted domain stops inheriting the operator's site.
func TestAnUnscopedReadGetsTheProductDefaultNotTheStoredValue(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	// Store something under the primary that differs from the compiled default.
	if err := s.SetMany(ctx, ForPrimary(), map[string]string{KeySiteName: "The Operator's Own Blog"}); err != nil {
		t.Fatalf("SetMany: %v", err)
	}
	if got := s.Get(ctx, ForPrimary(), KeySiteName); got != "The Operator's Own Blog" {
		t.Fatalf("primary read = %q, want the stored value", got)
	}

	var unset Scope
	if got := s.Get(ctx, unset, KeySiteName); got != Defaults[KeySiteName] {
		t.Errorf("an unscoped read returned %q — a caller that named no scope was served the "+
			"PRIMARY's stored value. That silent inheritance is the defect this design removes: "+
			"it is how a hosted domain ended up being the operator's own site wearing a "+
			"different name", got)
	}
}

// A write with no scope must be refused outright.
//
// The asymmetry with reads is deliberate. An unscoped read serves one wrong
// page and is recoverable. An unscoped write silently edits the operator's own
// install on behalf of a caller who never named it, and afterwards nothing can
// distinguish it from a change somebody meant to make.
func TestAnUnscopedWriteIsRefused(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	var unset Scope
	err := s.SetMany(ctx, unset, map[string]string{KeySiteName: "written by nobody"})
	if err == nil {
		t.Fatal("an unscoped write succeeded. Whichever settings it landed on, no record " +
			"exists of who it was for")
	}
	// And it must not have written anything on the way to failing.
	if got := s.Get(ctx, ForPrimary(), KeySiteName); got == "written by nobody" {
		t.Error("the unscoped write was reported as an error and still took effect on the primary")
	}
}

// The cache must be keyed by scope.
//
// This is the failure that would never appear in single-domain testing and only
// under concurrency in production: with one shared map, the first domain to warm
// the cache serves its theme, its SEO and its newsletter settings to every other
// domain on the install — a cross-tenant leak with no schema change behind it.
func TestTheCacheIsKeyedByScopeSoOneDomainCannotServeAnother(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	// Warm the primary, then a domain.
	_ = s.Get(ctx, ForPrimary(), KeySiteName)
	_ = s.Get(ctx, ForDomain("client.example"), KeySiteName)

	s.mu.RLock()
	defer s.mu.RUnlock()
	if len(s.cache) < 2 {
		t.Fatalf("the cache holds %d scope(s) after reading two — it is not keyed by scope, so "+
			"whichever scope warmed it first answers for all of them", len(s.cache))
	}
	if _, ok := s.cache[ForPrimary().key()]; !ok {
		t.Error("no cache entry for the primary")
	}
	if _, ok := s.cache[ForDomain("client.example").key()]; !ok {
		t.Error("no cache entry for the hosted domain")
	}
}

// Invalidating one scope must not cold-start every other scope on the install.
// On a thirty-client install, expiring the whole map on any single save is
// thirty re-queries for one edit.
func TestInvalidatingOneScopeLeavesTheOthersWarm(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	other := ForDomain("other.example")

	_ = s.Get(ctx, ForPrimary(), KeySiteName)
	_ = s.Get(ctx, other, KeySiteName)

	if err := s.SetMany(ctx, ForPrimary(), map[string]string{KeySiteName: "changed"}); err != nil {
		t.Fatal(err)
	}

	s.mu.RLock()
	defer s.mu.RUnlock()
	if _, ok := s.cache[ForPrimary().key()]; ok {
		t.Error("the written scope's cache survived its own write, so the change is invisible " +
			"until the TTL expires")
	}
	if _, ok := s.cache[other.key()]; !ok {
		t.Error("writing to the primary evicted an unrelated domain's cache")
	}
}

// The store must expose no way to read or write without a scope. A convenience
// wrapper added later "just for the primary" is how the ambient global read got
// there the first time.
func TestNoUnscopedAccessorExists(t *testing.T) {
	src := readOwnSource(t, "settings.go")
	for _, banned := range []string{
		"func (s *Store) Get(ctx context.Context, key string)",
		"func (s *Store) GetAll(ctx context.Context)",
		"func (s *Store) SetMany(ctx context.Context, kv map[string]string)",
		"func GetActiveSettings(",
	} {
		if strings.Contains(src, banned) {
			t.Errorf("an unscoped accessor is back: %s\nA settings read that cannot be wrong at "+
				"the call site is one no new feature will ever think about, which is exactly how "+
				"~60 call sites came to inherit the primary silently", banned)
		}
	}
}

// readOwnSource loads a file from this package, so these assertions read the
// shipped artefact rather than a restatement of it.
func readOwnSource(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(name)
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return string(b)
}
