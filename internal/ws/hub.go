// Package ws provides a WebSocket event streaming hub for VayuPress.
// Clients subscribe via /ws; events emitted to the hub are broadcast to all
// connected clients in real-time.
package ws

import (
	"encoding/json"
	"net/http"
	"sync"

	"github.com/johalputt/vayupress/internal/logging"
)

// Message is a JSON-encodable event streamed to clients.
type Message struct {
	Type    string      `json:"type"`
	Payload interface{} `json:"payload,omitempty"`
}

// Hub manages WebSocket connections and broadcasts messages.
type Hub struct {
	mu      sync.RWMutex
	clients map[chan []byte]struct{}
	bufSize int
}

// New creates a Hub with per-client buffer size bufSize.
func New(bufSize int) *Hub {
	if bufSize <= 0 {
		bufSize = 64
	}
	return &Hub{
		clients: make(map[chan []byte]struct{}),
		bufSize: bufSize,
	}
}

// Broadcast sends msg to all connected clients (non-blocking; slow clients are dropped).
func (h *Hub) Broadcast(msg Message) {
	b, err := json.Marshal(msg)
	if err != nil {
		logging.LogJSON(logging.LogFields{Level: "warn", Component: "ws", Msg: "marshal: " + err.Error()})
		return
	}
	line := append(b, '\n')
	h.mu.RLock()
	defer h.mu.RUnlock()
	for ch := range h.clients {
		select {
		case ch <- line:
		default:
			// client too slow — skip to avoid blocking broadcaster
		}
	}
}

// ConnectedCount returns the number of connected clients.
func (h *Hub) ConnectedCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.clients)
}

func (h *Hub) subscribe() chan []byte {
	ch := make(chan []byte, h.bufSize)
	h.mu.Lock()
	h.clients[ch] = struct{}{}
	h.mu.Unlock()
	return ch
}

func (h *Hub) unsubscribe(ch chan []byte) {
	h.mu.Lock()
	delete(h.clients, ch)
	h.mu.Unlock()
	close(ch)
}

// ServeHTTP implements http.Handler for Server-Sent Events (/ws endpoint).
// Uses SSE (text/event-stream) which requires no external dependency.
func (h *Hub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	ch := h.subscribe()
	defer h.unsubscribe(ch)

	logging.LogJSON(logging.LogFields{Level: "info", Component: "ws", Msg: "client connected"})
	defer logging.LogJSON(logging.LogFields{Level: "info", Component: "ws", Msg: "client disconnected"})

	for {
		select {
		case <-r.Context().Done():
			return
		case msg, open := <-ch:
			if !open {
				return
			}
			_, _ = w.Write([]byte("data: "))
			_, _ = w.Write(msg)
			_, _ = w.Write([]byte("\n"))
			flusher.Flush()
		}
	}
}
