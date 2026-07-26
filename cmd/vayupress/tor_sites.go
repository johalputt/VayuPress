// SPDX-License-Identifier: Apache-2.0

package main

// tor_sites.go — the PARENT (clearnet supervisor) side of "one-click add Tor
// site" (ADR-0141). The parent owns the tor control port, so it is the only
// process that can mint .onion addresses. A Tor site is a secondary domain that
// lives in the child (Tor world) registry; the child cannot mint onions itself,
// so this orchestration bridges the two instances:
//
//   - torWorldSites()  asks the child which Tor sites exist (their ids) so the
//     VayuTor engine can mint one dedicated onion per site.
//   - torWorldAssign() hands a freshly-minted onion back to the child, which
//     rewrites that site's placeholder host to the .onion and activates it.
//
// Both calls speak to the child over its own loopback port with its distinct API
// key (bearer), exactly like the enter-the-Tor-world proxy. They are wired into
// the VayuTor engine's Config (SpaceSites / SpaceSiteReady) so minting happens on
// the engine's normal reconcile loop, alongside the primary Space onion.

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"time"
)

// torWorldHTTP is the loopback client used for parent→child control calls. The
// timeout is generous for a loopback hop but bounded so a wedged child can never
// stall the engine's reconcile loop.
var torWorldHTTP = &http.Client{Timeout: 5 * time.Second}

// torWorldEndpoint returns the child's loopback base URL and bearer key, or
// ("","") when the Tor world is not running (nothing to talk to yet).
func (a *App) torWorldEndpoint() (base, key string) {
	if a.torSpace == nil || !a.torSpace.Running() {
		return "", ""
	}
	port := a.torSpace.Port()
	key = a.torSpace.APIKey()
	if port == 0 || key == "" {
		return "", ""
	}
	return "http://127.0.0.1:" + strconv.Itoa(port), key
}

// torWorldSites asks the Tor-world child for the id of every Tor site (secondary
// domain) that needs its own onion. The bool is the DEFINITIVENESS flag the engine
// relies on: true only when the child was reached and answered (even with zero
// sites); false on any error, so the engine leaves existing onions untouched and
// retries next tick rather than tearing a published .onion down on a transient
// hiccup.
func (a *App) torWorldSites() ([]string, bool) {
	base, key := a.torWorldEndpoint()
	if base == "" {
		return nil, false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/os/api/torworld/sites", nil)
	if err != nil {
		return nil, false
	}
	req.Header.Set("Authorization", "Bearer "+key)
	resp, err := torWorldHTTP.Do(req)
	if err != nil {
		return nil, false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
		return nil, false
	}
	var body struct {
		Sites []struct {
			ID string `json:"id"`
		} `json:"sites"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&body); err != nil {
		return nil, false
	}
	ids := make([]string, 0, len(body.Sites))
	for _, s := range body.Sites {
		if s.ID != "" {
			ids = append(ids, s.ID)
		}
	}
	return ids, true
}

// torWorldAssign tells the Tor-world child which onion was minted for a site, so
// the child rewrites that site's placeholder host to the .onion and activates it.
// Best-effort: the assign endpoint is idempotent, so a dropped call is simply
// retried when the engine reconciles again (the onion is already live by then).
func (a *App) torWorldAssign(id, onion string) {
	base, key := a.torWorldEndpoint()
	if base == "" || id == "" || onion == "" {
		return
	}
	payload, err := json.Marshal(map[string]string{"id": id, "host": onion})
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/os/api/torworld/assign", bytes.NewReader(payload))
	if err != nil {
		return
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	resp, err := torWorldHTTP.Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
}
