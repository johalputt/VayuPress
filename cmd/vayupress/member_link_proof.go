// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/johalputt/vayupress/internal/config"
	"github.com/johalputt/vayupress/internal/render"
	"github.com/johalputt/vayupress/internal/vayushield/challenge"
)

// A sign-in link to an address that is not a member yet is the one mail
// anybody can make this install send to anybody. In November 2026 senders
// posted the form from scripts, hundreds of times over two weeks, to mail
// links to parliament addresses that bounced back into the operator's
// Inbox. A member's own link is left as it was; a stranger's link must pass
// every gate below, each refused as silently as the budgets refuse, with the
// same answer, so none of them says whether an address is a member.

// linkProofTTL is how long a challenge may take to come back: long enough
// for someone to type their address after the page asked for it.
const linkProofTTL = 10 * time.Minute

// linkProofsSpent keeps each solved proof to one link. A proof is bound to
// nobody until it is spent, so without this one solution would buy a list's
// worth of links.
var linkProofsSpent = challenge.NewRedeemer()

// memberLinkStrangers is the whole install's ceiling on links to strangers.
// The per-source ban and the proof price one sender; a sender spread over a
// thousand addresses, each asking a few times, is held here. Forty new
// readers an hour is far above what a site's sign-ups come to, and far below
// what a sending domain's reputation survives.
const strangerLinksPerHour = 40

var memberLinkStrangers = newIngestLimiter(strangerLinksPerHour, time.Hour)

// linkProofSigner signs the challenges with a key of their own, made from
// the install's secret: a proof for Shield's check is not a proof for a
// link, and the other way round. With no secret there is no key, and no
// stranger is mailed.
func linkProofSigner() *challenge.Signer {
	if config.Cfg.APIKey == "" {
		return nil
	}
	return challenge.NewSigner([]byte("vayupress/sign-in-link/v1\x00" + config.Cfg.APIKey))
}

// GET /members/link-proof — a fresh challenge, never cached.
func (a *App) handleMemberLinkProof(w http.ResponseWriter, r *http.Request) {
	s := linkProofSigner()
	if s == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	p, err := s.IssuePoW(challenge.DefaultDifficulty, linkProofTTL)
	if err != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, r, http.StatusOK, p)
}

// GET /static/js/link-proof.js — the solver (render.LinkProofJS).
func (a *App) handleMemberLinkProofJS(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	publicAssetCache(w, r)
	fmt.Fprint(w, render.LinkProofJS)
}

// linkProofSpent reports whether proof (a challenge as the server issued
// it, in JSON) and nonce solve a challenge this install signed, unexpired and
// not spent before, and spends it.
func linkProofSpent(proof, nonce string) bool {
	s := linkProofSigner()
	if s == nil || proof == "" || len(proof) > 1024 || nonce == "" || len(nonce) > 32 {
		return false
	}
	var p challenge.PoW
	if json.NewDecoder(strings.NewReader(proof)).Decode(&p) != nil {
		return false
	}
	if s.VerifyPoW(p, nonce) != nil {
		return false
	}
	return linkProofsSpent.Claim(challenge.ProofID(p), p.Expires)
}

// strangerLinkRefused is the gates a link to an address that is not a member
// must pass, in order: the source has not now asked for too many strangers
// (which bans it for good, signin.LinkStrangers), it solved a proof, the
// address has never refused this install's mail for good, and the install
// is under its hourly ceiling. Each later gate runs only when the earlier
// ones passed, so a refused request spends nothing of the ceiling.
func (a *App) strangerLinkRefused(account, ip, email, proof, nonce string) bool {
	if a.signin.NoteLinkStranger(account, ip) {
		return true
	}
	if !linkProofSpent(proof, nonce) {
		return true
	}
	if a.vayuMail != nil && a.vayuMail.HardBounced(email) {
		return true
	}
	return !memberLinkStrangers.allow("install")
}
