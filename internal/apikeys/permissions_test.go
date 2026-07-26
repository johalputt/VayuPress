// SPDX-License-Identifier: Apache-2.0

package apikeys

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

func TestPermissionsWildcardsAndDenyDefault(t *testing.T) {
	// Deny-all: an empty grant set permits nothing.
	empty := NewPermissions()
	if empty.Has(SectionPosts, ActionRead) {
		t.Error("empty permissions must deny everything")
	}
	if !empty.IsEmpty() {
		t.Error("NewPermissions must be empty")
	}

	// Exact grant.
	p := NewPermissions()
	p.Grant(SectionPosts, ActionWrite)
	if !p.Has(SectionPosts, ActionWrite) {
		t.Error("exact grant should be allowed")
	}
	if p.Has(SectionPosts, ActionDelete) {
		t.Error("ungranted action must be denied")
	}
	if p.Has(SectionMedia, ActionWrite) {
		t.Error("ungranted section must be denied")
	}

	// Action wildcard: all actions of one section, but not other sections.
	sec := NewPermissions()
	sec.Grant(SectionComments, ActionAll)
	for _, a := range AllActions {
		if !sec.Has(SectionComments, a) {
			t.Errorf("section-all should grant comments:%s", a)
		}
	}
	if sec.Has(SectionPosts, ActionRead) {
		t.Error("section-all on comments must not leak to posts")
	}

	// Superuser: every capability.
	su := Superuser()
	if !su.IsSuperuser() {
		t.Error("Superuser().IsSuperuser() must be true")
	}
	for _, s := range AllSections {
		for _, a := range AllActions {
			if !su.Has(s, a) {
				t.Errorf("superuser must grant %s:%s", s, a)
			}
		}
	}

	// Unknown tokens are ignored (cannot widen access).
	bad := NewPermissions()
	bad.Grant(Section("root"), Action("sudo"))
	if !bad.IsEmpty() {
		t.Error("granting unknown section/action must be a no-op")
	}
}

func TestPermissionsJSONRoundTripAndFailClosed(t *testing.T) {
	p := NewPermissions()
	p.Grant(SectionPosts, ActionRead)
	p.Grant(SectionPosts, ActionWrite)
	p.Grant(SectionMedia, ActionRead)

	blob := p.MarshalString()
	// Envelope must carry the schema version.
	var env map[string]json.RawMessage
	if err := json.Unmarshal([]byte(blob), &env); err != nil {
		t.Fatalf("marshal produced invalid json: %v", err)
	}
	if _, ok := env["v"]; !ok {
		t.Error("permissions blob must carry a version field")
	}

	back := ParsePermissions(blob)
	if !back.Has(SectionPosts, ActionRead) || !back.Has(SectionPosts, ActionWrite) || !back.Has(SectionMedia, ActionRead) {
		t.Error("round-trip lost a grant")
	}
	if back.Has(SectionMedia, ActionWrite) {
		t.Error("round-trip invented a grant")
	}

	// Deterministic ordering: equal grant sets serialise identically.
	q := NewPermissions()
	q.Grant(SectionMedia, ActionRead)
	q.Grant(SectionPosts, ActionWrite)
	q.Grant(SectionPosts, ActionRead)
	if q.MarshalString() != blob {
		t.Errorf("marshal not deterministic:\n a=%s\n b=%s", blob, q.MarshalString())
	}

	// Fail-closed: garbage and forward-version tokens never grant access.
	if !ParsePermissions("not json").IsEmpty() {
		t.Error("garbage blob must parse to deny-all")
	}
	if !ParsePermissions(`{"v":99,"grants":{"root":["sudo"],"posts":["telepathy"]}}`).IsEmpty() {
		t.Error("unknown tokens in a blob must be dropped (deny-all)")
	}
}

// TestMigrationBackfillLiteralIsSuperuser couples migration 062's hardcoded
// backfill JSON to the parser: the literal it stamps onto pre-existing keys MUST
// parse to a full superuser grant, or the upgrade would silently strip access
// from live automation. If the wire shape ever changes, this fails loudly.
func TestMigrationBackfillLiteralIsSuperuser(t *testing.T) {
	const migrationLiteral = `{"v":1,"grants":{"*":["*"]}}`
	if migrationLiteral != Superuser().MarshalString() {
		t.Fatalf("migration 062 backfill literal %q no longer matches Superuser().MarshalString() %q — update the migration",
			migrationLiteral, Superuser().MarshalString())
	}
	if !ParsePermissions(migrationLiteral).IsSuperuser() {
		t.Error("migration backfill literal must parse to a superuser grant")
	}
}

func TestParseCapability(t *testing.T) {
	if s, a, ok := ParseCapability("posts:write"); !ok || s != SectionPosts || a != ActionWrite {
		t.Errorf("ParseCapability(posts:write) = %v,%v,%v", s, a, ok)
	}
	if _, _, ok := ParseCapability("posts:telepathy"); ok {
		t.Error("unknown action must not parse")
	}
	if _, _, ok := ParseCapability("noseparator"); ok {
		t.Error("missing colon must not parse")
	}
}

// TestResolveRespectsLifecycle proves the enforcement-time resolver honours the
// active flag, revocation, and hard expiry — the security-critical filters.
func TestResolveRespectsLifecycle(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	perms := NewPermissions()
	perms.Grant(SectionPosts, ActionRead)
	key, raw, err := s.CreateWithPermissions(ctx, "user-1", "scoped", perms, nil, 120)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// Resolves with the exact grant set + owner + rate.
	ki, ok := s.Resolve(raw)
	if !ok {
		t.Fatal("freshly created key must resolve")
	}
	if ki.Owner != "user-1" || ki.RatePerMin != 120 {
		t.Errorf("resolved owner/rate = %q/%d, want user-1/120", ki.Owner, ki.RatePerMin)
	}
	if !ki.Can(SectionPosts, ActionRead) {
		t.Error("resolved key should be able to read posts")
	}
	if ki.Can(SectionPosts, ActionWrite) {
		t.Error("scoped key must not have posts:write")
	}
	if ki.IsSuperuser() {
		t.Error("a scoped external key is not a superuser")
	}

	// Deactivate → stops resolving.
	if err := s.SetActive(ctx, key.ID, false); err != nil {
		t.Fatalf("deactivate: %v", err)
	}
	if _, ok := s.Resolve(raw); ok {
		t.Error("a deactivated key must not resolve")
	}
	// Reactivate → resolves again.
	if err := s.SetActive(ctx, key.ID, true); err != nil {
		t.Fatalf("reactivate: %v", err)
	}
	if _, ok := s.Resolve(raw); !ok {
		t.Error("a reactivated key must resolve")
	}

	// UpdatePermissions → new grant set takes effect.
	wider := NewPermissions()
	wider.Grant(SectionPosts, ActionAll)
	if err := s.UpdatePermissions(ctx, key.ID, wider); err != nil {
		t.Fatalf("update perms: %v", err)
	}
	ki2, _ := s.Resolve(raw)
	if !ki2.Can(SectionPosts, ActionDelete) {
		t.Error("widened grant should now allow posts:delete")
	}

	// Legacy Create → superuser (backward-compatible full access).
	_, legacyRaw, err := s.Create(ctx, "legacy")
	if err != nil {
		t.Fatalf("legacy create: %v", err)
	}
	if lk, ok := s.Resolve(legacyRaw); !ok || !lk.IsSuperuser() {
		t.Error("legacy Create must yield a superuser key for backward compatibility")
	}
}

// TestResolveExpiry proves an expired key stops resolving after the cache TTL is
// invalidated (mutations invalidate immediately; here we force a refresh).
func TestResolveExpiry(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	past := time.Now().Add(-time.Hour)
	_, raw, err := s.CreateWithPermissions(ctx, "u", "expired", Superuser(), &past, 0)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// The insert invalidated the cache; the next Resolve refreshes and must
	// exclude the already-expired key.
	if _, ok := s.Resolve(raw); ok {
		t.Error("an already-expired key must not resolve")
	}
}

// TestCleanupPurgesOnlyLongExpired verifies the sweeper deletes only external
// keys whose expiry passed more than the grace window ago — recently-expired
// keys stay visible (badged) and the internal key is never touched.
func TestCleanupPurgesOnlyLongExpired(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	longGone := time.Now().Add(-time.Duration(cleanupGraceDays+5) * 24 * time.Hour)
	recent := time.Now().Add(-time.Hour)
	_, _, err := s.CreateWithPermissions(ctx, "u", "long-expired", Superuser(), &longGone, 0)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	_, _, err = s.CreateWithPermissions(ctx, "u", "recently-expired", Superuser(), &recent, 0)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := s.EnsureInternal(ctx); err != nil {
		t.Fatalf("ensure internal: %v", err)
	}

	n, err := s.Cleanup(ctx)
	if err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	if n != 1 {
		t.Fatalf("cleanup purged %d keys, want exactly the long-expired one", n)
	}
	keys, err := s.List(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	var labels []string
	for _, k := range keys {
		labels = append(labels, k.Label)
	}
	for _, want := range []string{"recently-expired", "System (internal)"} {
		found := false
		for _, l := range labels {
			if l == want {
				found = true
			}
		}
		if !found {
			t.Errorf("cleanup must keep %q; remaining: %v", want, labels)
		}
	}
	for _, l := range labels {
		if l == "long-expired" {
			t.Error("cleanup must delete the long-expired key")
		}
	}
}

// TestResolveExpiryExactWhileCached is the regression guard for the security-
// review finding: a key whose expires_at passes WHILE it sits in the fresh cache
// must be refused on the very next request — hard expiry is exact, never lagged
// by the 30s cache TTL (revoke/deactivate invalidate the cache; the clock
// cannot, so Resolve re-checks expiry on every hit).
func TestResolveExpiryExactWhileCached(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	soon := time.Now().Add(150 * time.Millisecond)
	_, raw, err := s.CreateWithPermissions(ctx, "u", "short-lived", Superuser(), &soon, 0)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// Prime the cache while the key is still valid (TTL is 30s — far beyond the
	// key's lifetime, so without the exact check the stale entry would win).
	if _, ok := s.Resolve(raw); !ok {
		t.Fatal("key should resolve before its expiry")
	}
	time.Sleep(200 * time.Millisecond) // cross expires_at; cache still fresh
	if _, ok := s.Resolve(raw); ok {
		t.Error("a key that expired while cached must be refused immediately, not after the cache TTL")
	}
}

// TestCovers proves the subset predicate used to stop a key minting a key more
// powerful than itself: p.Covers(other) is true iff every capability in other is
// granted by p, with wildcards honoured on the p side.
func TestCovers(t *testing.T) {
	super := Superuser()
	posts := NewPermissions()
	posts.Grant(SectionPosts, ActionRead)
	posts.Grant(SectionPosts, ActionWrite)
	postsAll := NewPermissions()
	postsAll.Grant(SectionPosts, ActionAll)

	reqSuper := Superuser()
	reqPostWrite := NewPermissions()
	reqPostWrite.Grant(SectionPosts, ActionWrite)
	reqPostsStar := NewPermissions()
	reqPostsStar.Grant(SectionPosts, ActionAll)
	reqSettings := NewPermissions()
	reqSettings.Grant(SectionSettings, ActionWrite)

	cases := []struct {
		name string
		p    Permissions
		req  Permissions
		want bool
	}{
		{"superuser covers superuser request", super, reqSuper, true},
		{"superuser covers anything", super, reqSettings, true},
		{"posts:rw covers posts:write", posts, reqPostWrite, true},
		{"posts:rw does NOT cover posts:* (missing delete/etc)", posts, reqPostsStar, false},
		{"posts:* covers posts:write", postsAll, reqPostWrite, true},
		{"posts key does NOT cover superuser request", posts, reqSuper, false},
		{"posts key does NOT cover a settings request", posts, reqSettings, false},
		{"any key covers an empty (deny-all) request", posts, NewPermissions(), true},
	}
	for _, c := range cases {
		if got := c.p.Covers(c.req); got != c.want {
			t.Errorf("%s: Covers = %v, want %v", c.name, got, c.want)
		}
	}
}
