// SPDX-License-Identifier: Apache-2.0

package vayushield

// ja4.go — edge TLS fingerprint channel (2025 plan Wave 4, item 7).
//
// When nginx (or any trusted proxy) terminates TLS it can compute the JA4
// fingerprint and forward it as a request header. The operator names that
// header via Config.JA4Header — empty means OFF: a header nobody configured is
// a field an attacker chooses, exactly like client-supplied Geo. When enabled,
// every classified request's fingerprint is folded into the auxiliary
// reputation brain under the "ja4:" namespace, so a poisoned TLS fingerprint
// stays poisoned across IP rotation, and reward proofs rehabilitate it.

import (
	"net/http"
	"strings"

	"github.com/johalputt/vayupress/internal/vayushield/brain"
)

const (
	ja4Namespace  = "ja4:"
	maxJA4KeyLen  = 64 // JA4 strings are 36 chars; cap guards against junk headers
	ja4BlockDelta = -0.5
	ja4ProofDelta = 0.4
)

// JA4Of extracts the trusted JA4 fingerprint for this request, or "" when the
// channel is off or the header is absent/oversized.
func (m *Manager) JA4Of(r *http.Request) string {
	if m.cfg.JA4Header == "" || r == nil {
		return ""
	}
	v := strings.TrimSpace(r.Header.Get(m.cfg.JA4Header))
	if v == "" || len(v) > maxJA4KeyLen {
		return ""
	}
	return v
}

// observeJA4 folds this request's decision into the fingerprint's standing.
func (m *Manager) observeJA4(r *http.Request, a Action) {
	if m.campaignBrain == nil {
		return
	}
	f := m.JA4Of(r)
	if f == "" {
		return
	}
	d := campaignDeltaFor(a)
	if d != 0 {
		m.campaignBrain.Observe(ja4Namespace+f, d)
	}
}

// rewardJA4 refunds the fingerprint when its traffic proves itself.
func (m *Manager) rewardJA4(r *http.Request) {
	if m.campaignBrain == nil {
		return
	}
	f := m.JA4Of(r)
	if f != "" {
		m.campaignBrain.Observe(ja4Namespace+f, ja4ProofDelta)
	}
}

// JA4Standing returns the decayed reputation of the request's fingerprint
// (Neutral when the channel is off or unset).
func (m *Manager) JA4Standing(r *http.Request) float64 {
	if m.campaignBrain == nil {
		return brain.Neutral
	}
	f := m.JA4Of(r)
	if f == "" {
		return brain.Neutral
	}
	return m.campaignBrain.Standing(ja4Namespace + f)
}
