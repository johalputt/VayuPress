// SPDX-License-Identifier: Apache-2.0

// handlers_vayutalk.go — the VayuTalk HTTP surface: an ephemeral, end-to-end-
// encrypted messaging relay under /api/v1/talk/* (ADR-0131). The server never
// sees plaintext and never persists: envelopes live in a bounded in-memory
// store that a restart purges. Delivery is Server-Sent Events over stdlib
// net/http. Ciphertext and plaintext are NEVER logged.
package main

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	vtalk "github.com/johalputt/vayupress/internal/vayuos/vayutalk"
)

// streamWriteDeadline bounds a single SSE write so a stuck client socket cannot
// pin a server goroutine forever.
const streamWriteDeadline = 30 * time.Second

// streamHeartbeat is the interval between SSE keep-alive pings. Kept well under
// common reverse-proxy idle timeouts (nginx proxy_read_timeout defaults to 60s)
// so an idle conversation's stream is never mistaken for a dead connection and
// closed, and a genuinely dead peer is noticed within one interval.
const streamHeartbeat = 15 * time.Second

// vayuTalkEnabled reports whether the VayuTalk relay is serving.
func (a *App) vayuTalkEnabled() bool {
	return a.vayuTalk != nil && a.vayuTalk.Enabled()
}

// talkBearer extracts the bearer token from the Authorization header.
func talkBearer(r *http.Request) (string, bool) {
	h := r.Header.Get("Authorization")
	const prefix = "Bearer "
	if len(h) <= len(prefix) || !strings.EqualFold(h[:len(prefix)], prefix) {
		return "", false
	}
	return strings.TrimSpace(h[len(prefix):]), true
}

// talkAuth resolves the caller from a bearer token, writing a uniform 401 and
// returning ok=false on any failure.
func (a *App) talkAuth(w http.ResponseWriter, r *http.Request) (email string, ok bool) {
	token, present := talkBearer(r)
	if !present {
		writeAPIError(w, r, http.StatusUnauthorized, "invalid-token", "Authentication required", "")
		return "", false
	}
	email, ok = a.vayuTalk.Authenticate(token)
	if !ok {
		writeAPIError(w, r, http.StatusUnauthorized, "invalid-token", "Authentication required", "")
		return "", false
	}
	return email, true
}

// handleTalkConnect authenticates a mailbox credential and issues a short-lived
// bearer token. Auth is the mail-sync credential scope (device approval
// enforced) wrapped in the shared brute-force throttle; ANY failure returns a
// uniform 401 so the endpoint cannot enumerate which addresses exist.
func (a *App) handleTalkConnect(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !a.vayuTalkEnabled() {
		writeAPIError(w, r, http.StatusServiceUnavailable, "vayutalk-disabled", "VayuTalk is not available", "")
		return
	}
	var body struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16*1024)).Decode(&body); err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "bad-json", "Invalid request body", "")
		return
	}
	emailAddr := strings.TrimSpace(strings.ToLower(body.Email))
	if emailAddr == "" || body.Password == "" {
		writeAPIError(w, r, http.StatusBadRequest, "validation_error", "Email and password are required", "")
		return
	}
	token, ok := a.vayuTalk.Connect(r.Context(), emailAddr, body.Password)
	if !ok {
		writeAPIError(w, r, http.StatusUnauthorized, "invalid-credentials", "That email and password don't match", "")
		return
	}
	writeJSON(w, r, http.StatusOK, map[string]interface{}{
		"token":      token,
		"expires_in": vtalk.ConnectTTLSeconds,
	})
}

// handleTalkStream opens a Server-Sent Events stream: it flushes any queued
// store-mode envelopes, then streams live envelopes, receipts and heartbeats.
// The stream is bound to the request context (client disconnect cancels it),
// each write carries a deadline, and a heartbeat keeps intermediaries open.
func (a *App) handleTalkStream(w http.ResponseWriter, r *http.Request) {
	if !a.vayuTalkEnabled() {
		writeAPIError(w, r, http.StatusServiceUnavailable, "vayutalk-disabled", "VayuTalk is not available", "")
		return
	}
	email, ok := a.talkAuth(w, r)
	if !ok {
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeAPIError(w, r, http.StatusInternalServerError, "stream-unsupported", "Streaming is not supported", "")
		return
	}
	queued, ch, cancel, err := a.vayuTalk.Subscribe(email)
	if err != nil {
		// Global or per-user stream cap reached.
		writeAPIError(w, r, http.StatusTooManyRequests, "stream-limit", "Too many concurrent streams", "")
		return
	}
	defer cancel()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Connection", "keep-alive")
	// Defeat reverse-proxy response buffering (nginx and friends buffer by
	// default): without this a proxy in front of VayuPress holds the SSE bytes
	// instead of forwarding them, so the app never receives pushed envelopes or
	// receipts even though its POST /send works. The web stream sets the same
	// header; the app's API stream needs it just as much.
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	rc := http.NewResponseController(w)
	ctx := r.Context()

	// Flush queued store-mode envelopes first (in arrival order), then go live.
	for _, env := range queued {
		if !writeSSE(w, rc, "envelope", vtalk.EnvelopePayloadFor(env)) {
			return
		}
	}
	flusher.Flush()

	ping := time.NewTicker(streamHeartbeat)
	defer ping.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case evt, ok := <-ch:
			if !ok {
				return // evicted: a newer stream for this user took over
			}
			if !writeSSE(w, rc, evt.Type, evt.Payload) {
				return
			}
			flusher.Flush()
		case <-ping.C:
			if !writeSSE(w, rc, "ping", struct{}{}) {
				return
			}
			flusher.Flush()
		}
	}
}

// writeSSE writes one Server-Sent Event with a per-write deadline. It returns
// false when the write fails (the caller then ends the stream).
func writeSSE(w http.ResponseWriter, rc *http.ResponseController, event string, payload interface{}) bool {
	data, err := json.Marshal(payload)
	if err != nil {
		return false
	}
	_ = rc.SetWriteDeadline(time.Now().Add(streamWriteDeadline))
	if _, err := w.Write([]byte("event: " + event + "\ndata: ")); err != nil {
		return false
	}
	if _, err := w.Write(data); err != nil {
		return false
	}
	if _, err := w.Write([]byte("\n\n")); err != nil {
		return false
	}
	return true
}

// handleTalkSend accepts an opaque ciphertext envelope and routes it per mode.
// live: delivered only if the recipient is online now (never queued). store:
// delivered live if online, else queued in memory with a clamped TTL.
func (a *App) handleTalkSend(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !a.vayuTalkEnabled() {
		writeAPIError(w, r, http.StatusServiceUnavailable, "vayutalk-disabled", "VayuTalk is not available", "")
		return
	}
	from, ok := a.talkAuth(w, r)
	if !ok {
		return
	}
	var body struct {
		To         string `json:"to"`
		Ciphertext string `json:"ciphertext"`
		TTLSeconds int    `json:"ttl_seconds"`
		Mode       string `json:"mode"`
	}
	// Allow generously for base64 expansion over the 64 KiB decoded cap.
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 256*1024)).Decode(&body); err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "bad-json", "Invalid request body", "")
		return
	}
	to := strings.TrimSpace(strings.ToLower(body.To))
	mode := strings.TrimSpace(body.Mode)
	if to == "" {
		writeAPIError(w, r, http.StatusBadRequest, "validation_error", "Recipient is required", "")
		return
	}
	if mode != "live" && mode != "store" {
		writeAPIError(w, r, http.StatusBadRequest, "validation_error", "mode must be live or store", "")
		return
	}
	ciphertext, err := base64.StdEncoding.DecodeString(body.Ciphertext)
	if err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "bad-ciphertext", "Ciphertext must be base64", "")
		return
	}
	id, delivered, queued, err := a.vayuTalk.Send(from, to, ciphertext, body.TTLSeconds, mode)
	if err != nil {
		switch {
		case errors.Is(err, vtalk.ErrCiphertextTooLarge):
			writeAPIError(w, r, http.StatusRequestEntityTooLarge, "ciphertext-too-large", "Message too large", "")
		case errors.Is(err, vtalk.ErrRecipientQueueFull), errors.Is(err, vtalk.ErrGlobalQueueFull):
			writeAPIError(w, r, http.StatusTooManyRequests, "queue-full", "Message queue is full", "")
		default:
			writeAPIError(w, r, http.StatusInternalServerError, "send-error", "Could not send message", "")
		}
		return
	}
	writeJSON(w, r, http.StatusOK, map[string]interface{}{
		"id":        id,
		"delivered": delivered,
		"queued":    queued,
	})
}

// handleTalkAck read-destroys an envelope: it is deleted from the store
// immediately and, if the sender is streaming, a read receipt is emitted to it.
// Ownership-bound (audit): only the envelope's RECIPIENT may ack it — an
// authenticated peer naming someone else's id gets a 404 and the mail
// survives, instead of destroying another user's message from afar.
func (a *App) handleTalkAck(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !a.vayuTalkEnabled() {
		writeAPIError(w, r, http.StatusServiceUnavailable, "vayutalk-disabled", "VayuTalk is not available", "")
		return
	}
	email, ok := a.talkAuth(w, r)
	if !ok {
		return
	}
	var body struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16*1024)).Decode(&body); err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "bad-json", "Invalid request body", "")
		return
	}
	id := strings.TrimSpace(body.ID)
	if id == "" {
		writeAPIError(w, r, http.StatusBadRequest, "validation_error", "id is required", "")
		return
	}
	if !a.vayuTalk.AckAs(id, email) {
		// Unknown id or not the recipient: indistinguishable on purpose.
		writeAPIError(w, r, http.StatusNotFound, "not-found", "no such envelope", "")
		return
	}
	writeJSON(w, r, http.StatusOK, map[string]interface{}{})
}

// handleTalkPubkey returns a recipient's armored PGP public key so the client
// can encrypt to them. 404 when no key exists.
func (a *App) handleTalkPubkey(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !a.vayuTalkEnabled() {
		writeAPIError(w, r, http.StatusServiceUnavailable, "vayutalk-disabled", "VayuTalk is not available", "")
		return
	}
	if _, ok := a.talkAuth(w, r); !ok {
		return
	}
	email := strings.TrimSpace(strings.ToLower(r.URL.Query().Get("email")))
	if email == "" {
		writeAPIError(w, r, http.StatusBadRequest, "validation_error", "email is required", "")
		return
	}
	armored, fingerprint, err := a.vayuTalk.PubKey(email)
	if err != nil || armored == "" {
		writeAPIError(w, r, http.StatusNotFound, "no-key", "No public key for that address", "")
		return
	}
	writeJSON(w, r, http.StatusOK, map[string]interface{}{
		"email":              email,
		"armored_public_key": armored,
		"fingerprint":        fingerprint,
	})
}
