// SPDX-License-Identifier: Apache-2.0

package main

// tor_talk_deliver.go — the INBOUND half of onion-to-onion VayuTalk delivery
// (ADR-0142, Phase 2). A remote peer on another .onion, having fetched our
// published key, encrypts a message to us and POSTs the ciphertext envelope here
// over Tor; we drop it into our own in-process relay so our live stream receives
// it exactly like a local message.
//
// Trust & abuse posture:
//   - Closed by default. The route only functions in the Tor world AND when the
//     operator has opted into federation (talk.onion_federation). Otherwise it
//     404s, so it is not even discoverable on installs that have not enabled it.
//   - Not an open relay. We accept an envelope ONLY when it is addressed to our
//     OWN current anonymous code — never for an arbitrary recipient string — so it
//     cannot be used to fill another identity's queue.
//   - Bounded. The envelope rides the same 64 KiB ciphertext cap and the same
//     per-recipient / global queue caps as every other message, plus the public
//     discovery rate-limit on the route. The payload is opaque ciphertext; we
//     never decrypt here.

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/johalputt/vayupress/internal/config"
	vtalk "github.com/johalputt/vayupress/internal/vayuos/vayutalk"
)

// handleTalkOnionDeliver accepts a ciphertext envelope delivered from a remote
// .onion and enqueues it in the local relay for our anonymous identity.
func (a *App) handleTalkOnionDeliver(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	// Closed unless we are in the Tor world with federation switched on. A 404
	// (not 503) keeps the endpoint undiscoverable on installs that never opted in.
	if !a.vayuTalkEnabled() || !config.Cfg.OnionMode || !a.talkOnionFederationEnabled(r.Context()) {
		http.NotFound(w, r)
		return
	}
	self := a.talkAnonAddress(r.Context())
	if self == "" {
		http.NotFound(w, r)
		return
	}

	var body struct {
		To         string `json:"to"`
		From       string `json:"from"`
		Ciphertext string `json:"ciphertext"`
		TTLSeconds int    `json:"ttl_seconds"`
		Mode       string `json:"mode"`
	}
	// Generous limit for base64 expansion over the 64 KiB decoded cap.
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 256*1024)).Decode(&body); err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "bad-json", "Invalid request body", "")
		return
	}
	to := strings.TrimSpace(strings.ToLower(body.To))
	from := strings.TrimSpace(strings.ToLower(body.From))
	mode := strings.TrimSpace(body.Mode)
	if mode == "" {
		mode = "store"
	}
	// Only accept messages addressed to our OWN current code — never a relay for
	// some other identity's queue.
	if to != strings.ToLower(self) {
		writeAPIError(w, r, http.StatusForbidden, "not-for-us", "This code is not hosted here.", "")
		return
	}
	// The sender must present a well-formed onion handle so a reply is possible and
	// junk is rejected. We do not (here) prove the sender owns it — the recipient's
	// signature check (a later phase) is what authenticates the author.
	if from == "" || !talkHostIsOnion(hostPart(from)) {
		writeAPIError(w, r, http.StatusBadRequest, "bad-sender", "A valid onion sender code is required.", "")
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

	id, delivered, queued, serr := a.vayuTalk.Send(from, self, ciphertext, body.TTLSeconds, mode)
	if serr != nil {
		switch {
		case errors.Is(serr, vtalk.ErrCiphertextTooLarge):
			writeAPIError(w, r, http.StatusRequestEntityTooLarge, "ciphertext-too-large", "Message too large", "")
		case errors.Is(serr, vtalk.ErrRecipientQueueFull), errors.Is(serr, vtalk.ErrGlobalQueueFull):
			writeAPIError(w, r, http.StatusTooManyRequests, "queue-full", "Message queue is full", "")
		default:
			writeAPIError(w, r, http.StatusInternalServerError, "deliver-error", "Could not accept message", "")
		}
		return
	}
	writeJSON(w, r, http.StatusOK, map[string]interface{}{
		"id":        id,
		"delivered": delivered,
		"queued":    queued,
	})
}
