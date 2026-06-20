package webhooks

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

func newTestStore(t *testing.T, client *http.Client) *Store {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{
		`CREATE TABLE webhooks(id TEXT PRIMARY KEY,url TEXT NOT NULL,secret TEXT NOT NULL DEFAULT '',events TEXT NOT NULL DEFAULT '',active INTEGER NOT NULL DEFAULT 1,created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP)`,
		`CREATE TABLE webhook_deliveries(id TEXT PRIMARY KEY,webhook_id TEXT NOT NULL,event TEXT NOT NULL,status INTEGER NOT NULL DEFAULT 0,attempts INTEGER NOT NULL DEFAULT 0,last_error TEXT NOT NULL DEFAULT '',created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP)`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	return New(db, client)
}

func TestCreateValidation(t *testing.T) {
	s := newTestStore(t, nil)
	ctx := context.Background()
	if _, err := s.Create(ctx, "ftp://x", "", []string{"article.created.v1"}); err == nil {
		t.Error("expected scheme error")
	}
	if _, err := s.Create(ctx, "https://x.com/hook", "", nil); err == nil {
		t.Error("expected no-events error")
	}
	h, err := s.Create(ctx, "https://x.com/hook", "", []string{"article.created.v1"})
	if err != nil {
		t.Fatal(err)
	}
	if h.Secret == "" {
		t.Error("expected auto-generated secret")
	}
}

func TestDispatchSignsAndDelivers(t *testing.T) {
	var hits int32
	var gotSig, gotEvent string
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		gotSig = r.Header.Get("X-VayuPress-Signature")
		gotEvent = r.Header.Get("X-VayuPress-Event")
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(200)
	}))
	defer srv.Close()

	s := newTestStore(t, srv.Client())
	ctx := context.Background()
	h, _ := s.Create(ctx, srv.URL, "topsecret", []string{"article.created.v1"})

	s.Dispatch(ctx, "article.created.v1", map[string]string{"slug": "hi"})

	if atomic.LoadInt32(&hits) != 1 {
		t.Fatalf("expected 1 delivery, got %d", hits)
	}
	if gotEvent != "article.created.v1" {
		t.Errorf("event header = %q", gotEvent)
	}
	// Verify the HMAC signature matches the body under the secret.
	mac := hmac.New(sha256.New, []byte("topsecret"))
	mac.Write(gotBody)
	want := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	if gotSig != want {
		t.Errorf("signature mismatch:\n got %s\nwant %s", gotSig, want)
	}

	// A delivery record should exist.
	deliveries, _ := s.Deliveries(ctx, h.ID, 10)
	if len(deliveries) != 1 {
		t.Errorf("expected 1 delivery record, got %d", len(deliveries))
	}
}

func TestDispatchSkipsUnsubscribed(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(200)
	}))
	defer srv.Close()
	s := newTestStore(t, srv.Client())
	ctx := context.Background()
	s.Create(ctx, srv.URL, "x", []string{"article.deleted.v1"})
	s.Dispatch(ctx, "article.created.v1", nil) // not subscribed
	if atomic.LoadInt32(&hits) != 0 {
		t.Errorf("expected no delivery, got %d", hits)
	}
}
