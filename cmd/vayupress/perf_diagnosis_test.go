// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/apikeys"
	"github.com/johalputt/vayupress/internal/pace"
)

// johal.in's topic pages read p95 1.5 s while renders read p99 55 ms, and the
// connector could not say whether the rest was a wait for a render slot or a
// card pass that never finished. A request shed after waiting its whole
// coldRenderWait must show in the performance tool as a wait of at least that
// long, and as one more shed.
func TestTheRenderSlotWaitIsReported(t *testing.T) {
	fillColdRenderSlots(t)
	before := coldRenderShed.Load()
	if _, ok := admitColdRender(httptest.NewRecorder(), httptest.NewRequest("GET", "/tags/go", nil)); ok {
		t.Fatal("a request was admitted with every slot taken")
	}
	st := coldRenderState()
	if st["shed_since_boot"].(int64) != before+1 {
		t.Errorf("shed_since_boot = %v, want %d", st["shed_since_boot"], before+1)
	}
	if w := st["wait_p99_ms"].(int64); w < 50 {
		t.Errorf("wait_p99_ms = %d after a 50 ms wait, want at least 50", w)
	}
	if st["in_use"].(int) != st["slots"].(int) {
		t.Errorf("in_use %v of %v slots, want all", st["in_use"], st["slots"])
	}
}

// A card pass says whether it finished: one that ran out of posts without a
// card is complete; one stopped before the end (here, cancelled) is not.
func TestACardPassSaysWhetherItFinished(t *testing.T) {
	openMigratedDB(t)
	prev := newCardJob
	newCardJob = func() *pace.Job {
		return pace.New(pace.Sources{P95: func() (int64, bool) { return 10, true }}).NewJob(pace.JobConfig{Min: 8, Max: 8, Step: 1})
	}
	t.Cleanup(func() { newCardJob = prev })
	for i := 0; i < 20; i++ {
		seedCardPost(t, fmt.Sprintf("c%02d", i), "Post", `<p>Body.</p>`, "", "", "2026-09-20 10:00:00")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, complete := fillMissingCards(ctx); complete {
		t.Error("a cancelled pass reported itself complete")
	}
	if n, complete := fillMissingCards(context.Background()); !complete || n != 20 {
		t.Errorf("a full pass built %d and complete=%t, want 20 and true", n, complete)
	}
}

// The performance tool carries both readings.
func TestThePerformanceToolReportsWaitsAndCards(t *testing.T) {
	rec := httptest.NewRecorder()
	(&App{}).buildMCPServer().ServeHTTP(rec, keyCtxRequest(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"get_performance","arguments":{}}}`,
		apikeys.SuperuserKeyInfo("s", "root", apikeys.ScopeExternal)))
	var out struct {
		Result struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || len(out.Result.Content) == 0 {
		t.Fatalf("get_performance: %v %s", err, rec.Body.String())
	}
	for _, k := range []string{`"cold_renders"`, `"wait_p95_ms"`, `"listing_cards"`} {
		if !strings.Contains(out.Result.Content[0].Text, k) {
			t.Errorf("get_performance does not report %s: %s", k, out.Result.Content[0].Text)
		}
	}
}
