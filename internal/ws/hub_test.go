package ws_test

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/johalputt/vayupress/internal/ws"
)

func TestBroadcast(t *testing.T) {
	h := ws.New(4)

	// Use SSE handler with a recorder
	rec := httptest.NewRecorder()
	req, _ := context.WithTimeout(context.Background(), 200*time.Millisecond)
	r := httptest.NewRequest("GET", "/ws", nil).WithContext(req)

	done := make(chan struct{})
	go func() {
		defer close(done)
		h.ServeHTTP(rec, r)
	}()

	time.Sleep(20 * time.Millisecond)
	h.Broadcast(ws.Message{Type: "test", Payload: "hello"})
	<-done

	body := rec.Body.String()
	if !strings.Contains(body, `"test"`) {
		t.Errorf("expected event in body, got: %s", body)
	}
}

func TestConnectedCount(t *testing.T) {
	h := ws.New(2)
	if h.ConnectedCount() != 0 {
		t.Error("expected 0 clients")
	}
}
