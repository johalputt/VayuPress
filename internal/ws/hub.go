// SPDX-License-Identifier: Apache-2.0

// Package ws provides a WebSocket event streaming hub for VayuPress.
// Clients subscribe via /ws; events emitted to the hub are broadcast to all
// connected clients in real-time.
package ws

import (
	"encoding/json"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/johalputt/vayupress/internal/logging"
)

// Stream limits, ported from the hardened vayutalk hub (audit H8). Without a
// global cap and a per-peer cap, any client able to reach the endpoint could
// pin unbounded goroutines and file descriptors: writes used to have their
// errors discarded, so after the server-wide WriteTimeout every stale socket
// became an immortal zombie until process restart.
const (
	maxGlobalStreams = 500
	maxStreamsPerIP  = 10

	// heartbeat keeps intermediaries from silently killing idle streams and,
	// just as importantly, converts dead peers into a write error we act on.
	streamHeartbeatEvery = 15 * time.Second
	streamWriteTimeout   = 10 * time.Second
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
	perIP   map[string]int
}

// New creates a Hub with per-client buffer size bufSize.
func New(bufSize int) *Hub {
	if bufSize <= 0 {
		bufSize = 64
	}
	return &Hub{
		clients: make(map[chan []byte]struct{}),
		bufSize: bufSize,
		perIP:   make(map[string]int),
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

// subscribe registers a new stream for clientIP unless the global or
// per-peer limit is already saturated.
func (h *Hub) subscribe(clientIP string) (chan []byte, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.clients) >= maxGlobalStreams || h.perIP[clientIP] >= maxStreamsPerIP {
		return nil, false
	}
	ch := make(chan []byte, h.bufSize)
	h.clients[ch] = struct{}{}
	h.perIP[clientIP]++
	return ch, true
}

func (h *Hub) unsubscribe(ch chan []byte, clientIP string) {
	h.mu.Lock()
	delete(h.clients, ch)
	if n := h.perIP[clientIP]; n <= 1 {
		delete(h.perIP, clientIP)
	} else {
		h.perIP[clientIP] = n - 1
	}
	h.mu.Unlock()
	close(ch)
}

// ServeHTTP implements http.Handler for Server-Sent Events (/ws endpoint).
// Uses SSE (text/event-stream) which requires no external dependency.
//
// Every write carries a deadline and its failure ends the stream: with errors
// ignored this loop previously spun forever on sockets that had died at the
// TCP layer, one goroutine and one buffered channel each.
func (h *Hub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	clientIP := r.RemoteAddr
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		clientIP = host
	}
	ch, ok := h.subscribe(clientIP)
	if !ok {
		http.Error(w, "too many streams", http.StatusServiceUnavailable)
		return
	}
	defer h.unsubscribe(ch, clientIP)

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	rc := http.NewResponseController(w)
	heartbeat := time.NewTicker(streamHeartbeatEvery)
	defer heartbeat.Stop()

	logging.LogJSON(logging.LogFields{Level: "info", Component: "ws", Msg: "client connected"})
	defer logging.LogJSON(logging.LogFields{Level: "info", Component: "ws", Msg: "client disconnected"})

	write := func(b []byte) bool {
		_ = rc.SetWriteDeadline(time.Now().Add(streamWriteTimeout))
		if _, err := w.Write(b); err != nil {
			return false
		}
		flusher.Flush()
		return true
	}

	for {
		select {
		case <-r.Context().Done():
			return
		case <-heartbeat.C:
			// SSE comment — ignored by EventSource, keeps proxies honest and
			// surfaces dead peers as a failed write instead of a zombie.
			if !write([]byte(": ping\n\n")) {
				return
			}
		case msg, open := <-ch:
			if !open {
				return
			}
			if !write([]byte("data: ")) || !write(msg) || !write([]byte("\n")) {
				return
			}
		}
	}
}
