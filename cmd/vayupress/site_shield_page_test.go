// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"strings"
	"testing"

	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/vayushield"
	"github.com/johalputt/vayupress/internal/vayushield/botdb"
)

// A site's Security shows that site's refusals alone, with the request id of
// each; events recorded before events named their site appear only in the
// install's trail.
func TestASitesSecurityIsThatSitesAlone(t *testing.T) {
	openMigratedDB(t)
	ctx := context.Background()
	for _, q := range []string{
		`INSERT INTO vayushield_blocked(request_path,block_reason,domain_id,request_id) VALUES('/s1-probe','scanner path','s1','req-s1')`,
		`INSERT INTO vayushield_blocked(request_path,block_reason,domain_id,request_id) VALUES('/s2-probe','scanner path','s2','req-s2')`,
		`INSERT INTO vayushield_blocked(request_path,block_reason) VALUES('/before-sites','old rule')`,
		`INSERT INTO vayushield_challenges(challenge_type,outcome,domain_id) VALUES('pow','issued','s1')`,
	} {
		if _, err := dbpkg.DB.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	store := botdb.New(dbpkg.DB)
	a := &App{vayuShield: vayushield.New(vayushield.Config{Enabled: true, Bots: store})}
	page := a.renderSiteShieldBody(ctx, "s1", "s1", "one.test", 24)
	for _, want := range []string{"/s1-probe", "req-s1", "1 request refused on one.test", ">Challenged<"} {
		if !strings.Contains(page, want) {
			t.Errorf("site one's Security has no %q", want)
		}
	}
	for _, leak := range []string{"/s2-probe", "req-s2", "/before-sites"} {
		if strings.Contains(page, leak) {
			t.Errorf("site one's Security shows %q", leak)
		}
	}
	all, err := store.ReadTrailScoped(ctx, botdb.AllSites, 24, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if all.TotalBlocks != 3 {
		t.Errorf("the install's trail counts %d refusals, want all 3, the one with no site among them", all.TotalBlocks)
	}
	for _, not := range []string{`class="card`, "style=", "hx-"} {
		if strings.Contains(page, not) {
			t.Errorf("the page carries %q", not)
		}
	}
}
