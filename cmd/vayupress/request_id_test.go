// SPDX-License-Identifier: Apache-2.0

package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/trace"
)

// X-Request-ID and X-Correlation-ID come from whoever sends the request, and
// they are echoed back, logged and stored on the write jobs the request causes
// — the Replay page printed one as markup. A caller's ID is kept only when it
// looks like an ID; anything else is replaced, never passed along. One seed
// per rule: a tracing ID that must survive, markup, whitespace, length.
func TestOnlyAnIDShapedHeaderIsCarriedThrough(t *testing.T) {
	run := func(reqID, corrID string) (gotReq, gotCorr string) {
		h := requestIDMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotReq, gotCorr = getRequestID(r), trace.CorrelationID(r.Context())
		}))
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		if reqID != "" {
			r.Header.Set("X-Request-ID", reqID)
		}
		if corrID != "" {
			r.Header.Set("X-Correlation-ID", corrID)
		}
		h.ServeHTTP(httptest.NewRecorder(), r)
		return
	}
	uuid := "3f2c9a1e-7b4d-4c1a-9e2f-0a1b2c3d4e5f"
	if req, corr := run(uuid, "trace:"+uuid); req != uuid || corr != "trace:"+uuid {
		t.Errorf("an ordinary tracing ID was not carried through: %q, %q", req, corr)
	}
	for name, hostile := range map[string]string{
		"markup":     "<b>x</b>",
		"whitespace": "a b",
		"too long":   strings.Repeat("a", 65),
	} {
		req, corr := run(hostile, hostile)
		if req == hostile || corr == hostile {
			t.Errorf("%s: a caller's %q was carried through as an ID", name, hostile)
		}
		if req == "" || corr == "" {
			t.Errorf("%s: the replacement ID is empty", name)
		}
	}
}
