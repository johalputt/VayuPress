// SPDX-License-Identifier: Apache-2.0

package main

// The preview answered 405 with 0 bytes on every domain, from the connector and
// from the console, while browsers got 200. It ran its synthetic visit with the
// CALLER's context, and chi routes by the route state it finds in a context: the
// inner GET / was routed as the outer POST /mcp. These tests hand the preview a
// context shaped like each real caller's and assert on what the visit saw.

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/johalputt/vayupress/internal/users"
)

func TestThePreviewVisitsAsAStrangerNotAsItsCaller(t *testing.T) {
	var sawUser *users.User
	site := chi.NewRouter()
	site.Get("/", func(w http.ResponseWriter, r *http.Request) {
		sawUser = currentUser(r)
		_, _ = w.Write([]byte("<!doctype html><title>home</title>"))
	})

	// The caller: an operator's POST /mcp, mid-routing.
	outer := chi.NewRouteContext()
	outer.RouteMethod = http.MethodPost
	outer.RoutePath = "/mcp"
	ctx := context.WithValue(context.Background(), chi.RouteCtxKey, outer)
	ctx = context.WithValue(ctx, ctxUserKey, &users.User{ID: "op", Role: users.RoleAdmin})

	body, rec := previewFetch(ctx, site, "example.com", "/")
	if rec.Code != http.StatusOK || body == "" {
		t.Fatalf("preview → %d, %d bytes; the caller's route state routed the visit (browsers get 200)", rec.Code, len(body))
	}
	if sawUser != nil {
		t.Fatal("the synthetic visit carried the operator's identity — the preview shows what the operator sees, not a visitor")
	}
}

// Detaching the values must not detach the deadline: a preview abandoned by its
// caller stops.
func TestThePreviewStillEndsWithItsCaller(t *testing.T) {
	parent, cancel := context.WithCancel(context.Background())
	vctx, done := visitorContext(parent)
	defer done()
	cancel()
	select {
	case <-vctx.Done():
	case <-time.After(time.Second):
		t.Fatal("cancelling the caller did not cancel the preview's visit")
	}

	dl := time.Now().Add(time.Hour)
	withDL, stop := context.WithDeadline(context.Background(), dl)
	defer stop()
	v2, done2 := visitorContext(withDL)
	defer done2()
	if got, ok := v2.Deadline(); !ok || !got.Equal(dl) {
		t.Fatalf("deadline = %v %v, want the caller's %v", got, ok, dl)
	}
}
