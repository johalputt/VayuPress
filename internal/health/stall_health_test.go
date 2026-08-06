// SPDX-License-Identifier: Apache-2.0

package health

// stall_health_test.go — the health check must answer during the incident it
// exists to report.
//
// ATTACK: hold the single write connection, then ask the product whether it is
// healthy.
//
// Every one of these handlers ran an UNBOUNDED query on dbpkg.DB, which is
// capped at one connection. So during a write stall — the exact failure this
// install had — /health/db did not report "degraded". It hung, for as long as
// the stall lasted, and any monitor watching it recorded a timeout: the one
// response that carries no information at all.
//
// A health endpoint that fails the same way as the thing it monitors is not a
// health endpoint.

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	dbpkg "github.com/johalputt/vayupress/internal/db"
)

// holdTheWriteConnection occupies the single write connection for the duration
// of the test, exactly as a helper process or a long checkpoint would.
func holdTheWriteConnection(t *testing.T) {
	t.Helper()
	c, err := dbpkg.DB.Conn(context.Background())
	if err != nil {
		t.Fatalf("take the write connection: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
}

// answersWithin runs a handler and fails if it does not return in time.
func answersWithin(t *testing.T, name string, h http.HandlerFunc, budget time.Duration) map[string]interface{} {
	t.Helper()
	type res struct {
		code int
		body map[string]interface{}
	}
	ch := make(chan res, 1)
	go func() {
		rr := get(h)
		var m map[string]interface{}
		_ = json.Unmarshal(rr.Body.Bytes(), &m)
		ch <- res{rr.Code, m}
	}()
	select {
	case r := <-ch:
		return r.body
	case <-time.After(budget):
		t.Fatalf("%s did not answer within %v while the write connection was held. It hangs for "+
			"exactly as long as the stall it is supposed to report, so a monitor sees a timeout "+
			"instead of a diagnosis.", name, budget)
		return nil
	}
}

// THE test. /health/db must come back promptly and say the writer is contended.
func TestHealthDBReportsContentionInsteadOfHanging(t *testing.T) {
	holdTheWriteConnection(t)

	body := answersWithin(t, "/health/db", HandleHealthDB, healthDBProbe+5*time.Second)
	if body == nil {
		t.Fatal("no body")
	}
	if got := body["status"]; got != "contended" {
		t.Errorf("status is %q while the write connection is held; want \"contended\" — the "+
			"database is fine, the queue in front of it is not, and those are different faults "+
			"with different fixes", got)
	}
	// It must also carry the evidence, not just a verdict. "Degraded" with no
	// number sends somebody to a shell, which is the thing this replaces.
	w, ok := body["writer"].(map[string]interface{})
	if !ok {
		t.Fatal("the response carries no writer detail, so it says something is wrong without " +
			"saying what")
	}
	for _, k := range []string{"in_use", "max_open", "waits_total", "wait_seconds", "stalled_now"} {
		if _, present := w[k]; !present {
			t.Errorf("the writer report omits %q", k)
		}
	}
	if mx, _ := w["max_open"].(float64); mx != 1 {
		t.Errorf("max_open reads %v; this pool is meant to be the single SQLite writer", w["max_open"])
	}
}

// Readiness must go false — and say why — rather than hanging. A probe that
// times out looks identical to a dead process to every orchestrator there is.
func TestHealthReadyFailsFastAndNamesTheReason(t *testing.T) {
	holdTheWriteConnection(t)

	body := answersWithin(t, "/health/ready", HandleHealthReady, healthDBProbe+5*time.Second)
	if got := body["status"]; got != "not_ready" {
		t.Errorf("readiness is %q while the writer is jammed, want \"not_ready\"", got)
	}
	if reason, _ := body["reason"].(string); reason == "" {
		t.Error("not_ready with no reason; an operator gets a red light and no next step")
	}
}

// The worker and ethics checks touch the write pool too, and were the same
// hazard. They must answer, whatever they answer.
func TestTheOtherWritePoolHealthChecksStillAnswer(t *testing.T) {
	holdTheWriteConnection(t)

	for _, c := range []struct {
		name string
		h    http.HandlerFunc
	}{
		{"/health/workers", HandleHealthWorkers},
		{"/health/ethics", HandleHealthEthics},
	} {
		if body := answersWithin(t, c.name, c.h, healthDBProbe+5*time.Second); body["status"] == nil {
			t.Errorf("%s answered without a status", c.name)
		}
	}
}

// And when nothing is wrong, nothing is reported as wrong. A check that says
// "contended" on a healthy install is a check that gets ignored.
func TestHealthDBIsPlainlyOKWhenTheWriterIsFree(t *testing.T) {
	body := answersWithin(t, "/health/db", HandleHealthDB, 5*time.Second)
	if got := body["status"]; got != "ok" {
		t.Errorf("status is %q on an idle install, want \"ok\"", got)
	}
	if _, present := body["current_stall"]; present {
		t.Error("an idle install is reporting a stall in progress")
	}
}
