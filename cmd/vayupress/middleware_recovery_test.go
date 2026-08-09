// SPDX-License-Identifier: Apache-2.0

package main

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

// A PANIC BEFORE RECOVERER IS A 502, NOT A 500.
//
// Reported from a live install: "after updating VayuOS went 502 gateway, now
// back again." That one was the restart window. But it named a failure mode the
// stack was genuinely exposed to: Recoverer sat FOURTH, so a panic in
// realIPMiddleware or structuredLoggerMiddleware — both of which run on every
// request — was never recovered. The panic reaches net/http, which closes the
// connection with no response, and the reverse proxy in front turns that into a
// 502. The operator sees a gateway error and the app log shows no status at all.
//
// realIPMiddleware is the worst place to leave unprotected: it resolves the
// client address, records proxy sightings, and samples resolution outcomes — the
// code most likely to meet a request shape nobody anticipated.

func middlewareIndex(t *testing.T, stack []func(http.Handler) http.Handler, want func(http.Handler) http.Handler) int {
	t.Helper()
	target := reflect.ValueOf(want).Pointer()
	for i, m := range stack {
		if reflect.ValueOf(m).Pointer() == target {
			return i
		}
	}
	t.Fatal("middleware not found in the core stack")
	return -1
}

func TestRecovererWrapsEveryMiddlewareThatRunsOnARequest(t *testing.T) {
	stack := coreMiddleware()

	// Positions come from the real slice by function pointer, not from reading
	// the source: the order IS data, so read the data.
	reqID := middlewareIndex(t, stack, requestIDMiddleware)
	realIP := middlewareIndex(t, stack, realIPMiddleware)
	logger := middlewareIndex(t, stack, structuredLoggerMiddleware)

	if realIP <= reqID+1 || logger <= reqID+1 {
		t.Fatalf("requestID is at %d and realIP/logger at %d/%d, leaving no recovery between "+
			"them. Both run on every request, so a panic in either is a dropped connection — a "+
			"502 at the proxy with no status in the app log — instead of a 500", reqID, realIP, logger)
	}

	// And it must actually recover: compose the real stack around a panicking
	// handler and require a 500 with a response, not a dropped connection.
	var h http.Handler = http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("boom")
	})
	for i := len(stack) - 1; i >= 0; i-- {
		h = stack[i](h)
	}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "127.0.0.1:5000"
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Errorf("a panicking handler produced %d, want 500 — the stack is not recovering at all", rr.Code)
	}
}
