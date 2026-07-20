// vayuos_talk.go — the VayuTalk web client for VayuOS, mounted under
// /os/talk. It is the browser counterpart to VayuMail Mobile's chat
// screen: both speak to the SAME in-process relay (internal/vayuos/vayutalk),
// so a message typed in the web console and a message typed in the app are
// indistinguishable to the relay and fully interoperable end to end.
//
// Trust model (identical to webmail's transparent mail decryption): the browser
// never holds the mailbox's PGP private key. Instead the VayuPress server — the
// user's own sovereign machine, which already decrypts their encrypted mail for
// the reader — performs the PGP crypto on their behalf:
//
//   - send   : the server signs+encrypts the plaintext to the recipient's key
//     (EncryptAndSignFromEmail) and hands the armored ciphertext to the
//     relay. The relay, the network, and every intermediary see only
//     ciphertext — exactly as with the app.
//   - stream : the server subscribes to the relay for the logged-in mailbox,
//     decrypts each incoming envelope with that mailbox's key, and
//     pushes plaintext to the browser over Server-Sent Events. It does
//     NOT read-destroy on delivery — see handleVayuOSTalkStream for why
//     the console must not consume the recipient's shared queue.
//
// Nothing is persisted. Plaintext exists only for the lifetime of one request
// or one SSE write, and is NEVER logged. The session cookie authenticates the
// browser (same /os guard as the rest of the console); no bearer token is
// minted for this path.
package main

import (
	"encoding/base64"
	"encoding/json"
	htmpl "html/template"
	"net/http"
	"strings"
	"time"

	"github.com/johalputt/vayupress/internal/auth"
	"github.com/johalputt/vayupress/internal/config"
	"github.com/johalputt/vayupress/internal/render"
	vpgp "github.com/johalputt/vayupress/internal/vayuos/pgp"
	vtalk "github.com/johalputt/vayupress/internal/vayuos/vayutalk"
)

// talkIdentity resolves the VayuTalk chat identity for the signed-in session —
// the address this person messages as. It deliberately does NOT go through
// ownMailbox: that helper re-fetches the user from the database and so drops the
// in-memory mailbox address that a mailbox login attaches to a unified CMS
// account (and a pure CMS admin has no assigned mailbox row at all). Resolution,
// most-specific first:
//
//  1. the mailbox this session is bound to (set at login for mailbox holders);
//  2. the account's own email, when it is an address on the mail domain — i.e.
//     "chat as the identity I signed in with";
//  3. admin fallback: the first active mailbox on the server (an admin owns them
//     all), else postmaster@domain.
//
// Every branch returns an address the caller legitimately controls, so nobody
// can chat as an identity that isn't theirs. Keys are minted on demand, so the
// address need not have a pre-existing PGP key.
func (a *App) talkIdentity(r *http.Request) string {
	// In the Tor world the identity is an ANONYMOUS, rotatable handle — never a
	// mailbox address — so chat is not linked to a mail account (ADR-0141).
	if config.Cfg.OnionMode {
		return a.talkAnonAddress(r.Context())
	}
	domain := ""
	if a.vayuMail != nil && a.vayuMail.Config().Enabled {
		domain = strings.ToLower(strings.TrimSpace(a.vayuMail.Config().Domain))
	}
	if u := currentUser(r); u != nil {
		if ma := strings.TrimSpace(strings.ToLower(u.MailAddress)); ma != "" {
			return ma
		}
		if e := strings.TrimSpace(strings.ToLower(u.Email)); e != "" && domain != "" && strings.HasSuffix(e, "@"+domain) {
			return e
		}
	}
	if a.isAdminRequest(r) {
		if boxes := a.appPasswordMailboxes(r); len(boxes) > 0 {
			return strings.ToLower(strings.TrimSpace(boxes[0]))
		}
		if domain != "" {
			return "postmaster@" + domain
		}
	}
	return ""
}

// talkIdentities lists every address this session may chat as, default first.
// A normal mailbox holder gets exactly one (their own); an admin — who owns
// every mailbox on the server — may pick any active mailbox, which is what the
// "chat as ▾" switcher offers. The default (talkIdentity) always leads, so an
// admin whose fallback is postmaster@domain still sees it even if it isn't a
// provisioned account.
func (a *App) talkIdentities(r *http.Request) []string {
	def := a.talkIdentity(r)
	if def == "" {
		return nil
	}
	// The Tor world has exactly one identity — the anonymous handle — with no
	// "chat as" mailbox switcher (there are no mailbox identities to pick from).
	if config.Cfg.OnionMode {
		return []string{def}
	}
	out := []string{def}
	if a.isAdminRequest(r) {
		seen := map[string]bool{def: true}
		for _, m := range a.appPasswordMailboxes(r) {
			m = strings.ToLower(strings.TrimSpace(m))
			if m != "" && !seen[m] {
				seen[m] = true
				out = append(out, m)
			}
		}
	}
	return out
}

// talkSelf resolves the identity for one request, honouring an explicit
// selection (the switcher's `as` value) but ONLY when it is one this session is
// entitled to use — otherwise it falls back to the default. This is the
// authorization boundary for "chat as": a non-admin can never select another
// mailbox, and an admin is confined to mailboxes on their own server.
func (a *App) talkSelf(r *http.Request, requested string) string {
	allowed := a.talkIdentities(r)
	if len(allowed) == 0 {
		return ""
	}
	requested = strings.ToLower(strings.TrimSpace(requested))
	if requested != "" {
		for _, m := range allowed {
			if m == requested {
				return requested
			}
		}
	}
	return allowed[0]
}

// formatSafety renders a PGP fingerprint as a "safety number" for out-of-band
// verification. It normalises exactly as VayuMail Mobile's chat.SafetyNumber
// does — colons and spaces stripped, uppercased, split into groups of four — so
// the groups shown here match the app's Verify-contact screen character for
// character. (The app additionally wraps every fifth group onto a new line;
// here the groups flow and wrap naturally, which does not change the digits a
// user compares.)
func formatSafety(fp string) string {
	fp = strings.ToUpper(strings.Map(func(r rune) rune {
		if r == ' ' || r == ':' {
			return -1
		}
		return r
	}, fp))
	var b strings.Builder
	for i := 0; i < len(fp); i += 4 {
		end := i + 4
		if end > len(fp) {
			end = len(fp)
		}
		if i > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(fp[i:end])
	}
	return b.String()
}

// handleVayuOSTalkPeer resolves a recipient's VayuTalk key. The browser calls it
// (a) when a conversation opens, so the user is told up front whether the
// address is reachable — turning a silent "can't send" into a clear reason —
// and (b) to show the peer's fingerprint as a safety number for out-of-band
// verification. Read-only and session-authenticated; no plaintext involved.
func (a *App) handleVayuOSTalkPeer(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !a.vayuTalkEnabled() {
		writeAPIError(w, r, http.StatusServiceUnavailable, "vayutalk-disabled", "VayuTalk is not available", "")
		return
	}
	if a.talkIdentity(r) == "" {
		writeAPIError(w, r, http.StatusForbidden, "no-mailbox", "No mailbox is assigned to your account", "")
		return
	}
	email := strings.TrimSpace(strings.ToLower(r.URL.Query().Get("email")))
	if email == "" {
		writeAPIError(w, r, http.StatusBadRequest, "validation_error", "email is required", "")
		return
	}
	_, fp, err := a.vayuTalk.PubKey(email)
	if err != nil || fp == "" {
		writeJSON(w, r, http.StatusOK, map[string]interface{}{"email": email, "found": false})
		return
	}
	writeJSON(w, r, http.StatusOK, map[string]interface{}{
		"email":       email,
		"found":       true,
		"fingerprint": fp,
		"safety":      formatSafety(fp),
	})
}

// talkMessageOut is the decrypted message shape pushed to the browser over SSE.
// It carries plaintext (the server has already opened the envelope) plus the
// routing/expiry metadata the UI needs to render and time-out the bubble.
type talkMessageOut struct {
	ID          string `json:"id"`
	From        string `json:"from"`
	Text        string `json:"text"`
	CreatedAt   string `json:"created_at"`
	ExpiresAt   string `json:"expires_at"`
	BurnSeconds int    `json:"burn_seconds"`
	Mode        string `json:"mode"`
	Verified    bool   `json:"verified"`
}

// handleVayuOSTalk renders the VayuTalk chat page. The heavy lifting is in
// admin-os-talk.js; this handler emits the CSP-clean shell, seeds the CSRF
// cookie the send POST reads back, and resolves the signed-in mailbox identity.
func (a *App) handleVayuOSTalk(w http.ResponseWriter, r *http.Request) {
	nonce := render.CSPNonce(r)
	cfg := a.getOSSettings(r.Context())
	// CSRF cookie so the JSON send POST passes the double-submit middleware,
	// re-issued on every page load (the token has a 1h life).
	if token := auth.GenerateCSRFToken(); token != "" {
		http.SetCookie(w, &http.Cookie{Name: "vp_csrf", Value: token, Path: "/", SameSite: http.SameSiteStrictMode, HttpOnly: false, Secure: csrfCookieSecure(), MaxAge: 3600})
	}

	var body strings.Builder
	tagline := `Ephemeral, end-to-end-encrypted chat — same relay as the app. Pick a self-destruct timer; messages burn a set time after they're read.`
	if config.Cfg.OnionMode {
		tagline = `Ephemeral, end-to-end-encrypted chat over your <code>.onion</code>. Your identity is an anonymous, rotatable code — not a mailbox. Share it so people can reach you; messages burn a set time after they're read.`
	}
	body.WriteString(`<div class="page-header"><h1>VayuTalk</h1><span class="muted text-sm">` + tagline + `</span></div>`)

	if !a.vayuTalkEnabled() {
		body.WriteString(`<div class="empty-state">VayuTalk is inactive. It runs automatically once mail is enabled (a <code>DOMAIN</code> is set); it is disabled only when <code>VAYUOS_TALK=off</code>.</div>`)
		writeOSHTML(w, adminOSLayout(nonce, "VayuTalk", "talk", cfg, htmpl.HTML(body.String())))
		return
	}

	idents := a.talkIdentities(r)
	if len(idents) == 0 {
		body.WriteString(`<div class="empty-state">VayuTalk needs a mailbox on this domain to use as your chat identity, and this server has no mailboxes yet. Create one under <a href="/os/vayumail/accounts">VayuMail → Accounts</a>, then reload.</div>`)
		writeOSHTML(w, adminOSLayout(nonce, "VayuTalk", "talk", cfg, htmpl.HTML(body.String())))
		return
	}
	self := idents[0]

	// Resolve our own fingerprint (minting the key if this is the first use) so
	// the verify panel can show the user their own safety number to read out.
	// Only the default identity is minted eagerly; switching to another mints
	// that one on demand via the peer endpoint, so a server with many mailboxes
	// never pays for keygen it isn't using.
	a.ensureTalkKeypair(self)
	_, selfFP, _ := a.vayuTalk.PubKey(self)

	esc := htmpl.HTMLEscapeString
	body.WriteString(`<div class="vtalk" data-self="` + esc(self) + `" data-self-fp="` + esc(formatSafety(selfFP)) + `">`)

	// Left rail: identity (a "chat as" switcher when more than one is available),
	// start-a-chat box, and the live conversation list (filled by JS).
	body.WriteString(`<aside class="vtalk-side">`)
	body.WriteString(`<div class="vtalk-identity">` + mailAvatarImg(self, a.mailboxAvatarSet()) + `<div class="vtalk-identity-meta">`)
	if len(idents) > 1 {
		body.WriteString(`<select class="vtalk-as" id="vtalk-as" aria-label="Chat as">`)
		for _, id := range idents {
			body.WriteString(`<option value="` + esc(id) + `">` + esc(id) + `</option>`)
		}
		body.WriteString(`</select>`)
	} else {
		body.WriteString(`<strong>` + esc(self) + `</strong>`)
	}
	body.WriteString(`<span class="vtalk-status" id="vtalk-status" data-state="connecting">Connecting…</span></div></div>`)
	// Tor world: the identity is an anonymous, rotatable code — offer copy + rotate.
	if config.Cfg.OnionMode {
		// Onion-to-onion federation status + toggle (ADR-0142): reaching a code on
		// another .onion is opt-in and experimental; surface its state and let the
		// operator flip it.
		fedOn := a.talkOnionFederationEnabled(r.Context())
		fedNote := `<p class="text-sm muted">Onion-to-onion delivery is <strong>off</strong> — you can be reached on this .onion; messaging a code on a different .onion is disabled.</p>`
		fedBtn := `<button type="button" class="btn btn--sm btn--primary" id="vtalk-fed" data-on="0">Enable onion-to-onion</button>`
		if fedOn {
			fedNote = `<p class="text-sm muted">Onion-to-onion delivery is <strong>on</strong> (experimental). Set <code>VAYUOS_TOR_SOCKS_ADDR</code> to your tor's SOCKS address for sending to work.</p>`
			fedBtn = `<button type="button" class="btn btn--sm btn--ghost" id="vtalk-fed" data-on="1">Disable onion-to-onion</button>`
		}
		body.WriteString(`<div class="vtalk-anon"><p class="text-sm muted">This is your anonymous code — share it so people can reach you.</p><div class="ak-cred-actions"><button type="button" class="btn btn--sm" data-copy="` + esc(self) + `">Copy code</button><button type="button" class="btn btn--sm btn--ghost" id="vtalk-rotate">Rotate</button></div>` + fedNote + `<div class="ak-cred-actions">` + fedBtn + `</div></div>`)
	}
	body.WriteString(`<form class="vtalk-newchat" id="vtalk-newchat"><input class="input input--sm" id="vtalk-peer" type="email" autocomplete="off" spellcheck="false" placeholder="name@domain" aria-label="Recipient address"><button class="btn btn--sm btn--primary" type="submit">Start</button></form>`)
	body.WriteString(`<ul class="vtalk-convos" id="vtalk-convos" aria-label="Conversations"></ul>`)
	body.WriteString(`</aside>`)

	// Right pane: thread header, message list, composer.
	body.WriteString(`<section class="vtalk-main" id="vtalk-main" data-empty="1">`)
	body.WriteString(`<div class="vtalk-thread-head" id="vtalk-thread-head"></div>`)
	body.WriteString(`<div class="vtalk-thread" id="vtalk-thread"><div class="vtalk-hint"><div class="vtalk-hint-badge">🔒</div><p>Pick a conversation or start a new one. Messages are end-to-end encrypted and self-destruct on a timer once they're read. Turn on 🔥 Live to keep nothing on the server at all.</p></div></div>`)
	body.WriteString(`<form class="vtalk-composer" id="vtalk-composer">`)
	body.WriteString(`<textarea class="vtalk-input" id="vtalk-input" rows="1" placeholder="Write a message…" aria-label="Message" disabled></textarea>`)
	body.WriteString(`<div class="vtalk-composer-actions">`)
	body.WriteString(`<label class="vtalk-opt"><span class="text-sm muted">Disappears</span><select class="input input--sm" id="vtalk-ttl" aria-label="Self-destruct timer (after reading)"><option value="5">5 sec after read</option><option value="60">1 min after read</option><option value="300" selected>5 min after read</option><option value="900">15 min after read</option><option value="1800">30 min after read</option><option value="3600">1 hour after read</option></select></label>`)
	body.WriteString(`<label class="vtalk-opt vtalk-live" title="Live mode: not stored on the server; vanishes the instant it is read. Both of you must be online."><input type="checkbox" id="vtalk-live"><span class="text-sm">🔥 Live</span></label>`)
	body.WriteString(`<span class="vtalk-opt--spacer"></span>`)
	body.WriteString(`<button class="btn btn--primary btn--sm" type="submit" id="vtalk-send" disabled>Send</button>`)
	body.WriteString(`</div></form>`)
	body.WriteString(`</section>`)

	body.WriteString(`</div>`) // .vtalk
	body.WriteString(`<script nonce="` + nonce + `" src="/os/static/js/admin-os-talk.js?v=` + assetVer("js/admin-os-talk.js") + `"></script>`)
	// Anonymous-code controls (Tor world): copy the code, or rotate to a fresh one.
	// Same-origin, CSRF-checked; reload after a rotate to pick up the new identity.
	body.WriteString(`<script nonce="` + nonce + `">(function(){
function csrfTok(){var m=document.cookie.match(/(?:^|;\s*)vp_csrf=([^;]+)/);return m?m[1]:'';}
var rot=document.getElementById('vtalk-rotate');
if(rot){rot.addEventListener('click',function(){rot.disabled=true;fetch('/os/talk/rotate',{method:'POST',credentials:'same-origin',headers:{'X-CSRF-Token':csrfTok()}}).then(function(r){if(r.ok){location.reload();}else{rot.disabled=false;}}).catch(function(){rot.disabled=false;});});}
var fed=document.getElementById('vtalk-fed');
if(fed){fed.addEventListener('click',function(){fed.disabled=true;fetch('/os/talk/federation',{method:'POST',credentials:'same-origin',headers:{'X-CSRF-Token':csrfTok()}}).then(function(r){if(r.ok){location.reload();}else{fed.disabled=false;}}).catch(function(){fed.disabled=false;});});}
Array.prototype.forEach.call(document.querySelectorAll('.vtalk-anon [data-copy]'),function(b){b.addEventListener('click',function(){var v=b.getAttribute('data-copy')||'',p=b.textContent;var d=function(){b.textContent='Copied';setTimeout(function(){b.textContent=p;},1400);};if(navigator.clipboard&&navigator.clipboard.writeText){navigator.clipboard.writeText(v).then(d,d);}else{d();}});});
})();</script>`)
	writeOSHTML(w, adminOSLayout(nonce, "VayuTalk", "talk", cfg, htmpl.HTML(body.String())))
}

// handleVayuOSTalkStream is the server-side-decrypting SSE bridge. It subscribes
// to the relay for the signed-in mailbox, flushes anything queued while the user
// was away, then streams live. Every envelope is decrypted with the mailbox's
// own key before it reaches the browser.
//
// It deliberately does NOT read-destroy (ack) on delivery. The relay keeps a
// single shared queue per recipient identity, and a mailbox can be open on
// several places at once — the phone app AND this console, both as the same
// address. If the console acked on delivery it would delete the queued envelope
// the instant it rendered it here, so the user's PHONE (which relies on flushing
// that queue when its stream reconnects) would never receive its copy — the
// message would "only appear in the web". So the console is a passive viewer:
// it shows messages and lets them expire by TTL, while the app remains the
// authoritative reader that read-destroys on reveal. Because we no longer ack,
// a reconnect re-flushes the queue, so admin-os-talk.js dedupes by message id.
func (a *App) handleVayuOSTalkStream(w http.ResponseWriter, r *http.Request) {
	if !a.vayuTalkEnabled() {
		http.Error(w, "VayuTalk unavailable", http.StatusServiceUnavailable)
		return
	}
	self := a.talkSelf(r, r.URL.Query().Get("as"))
	if self == "" {
		http.Error(w, "no mailbox assigned", http.StatusForbidden)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	queued, ch, cancel, err := a.vayuTalk.Subscribe(self)
	if err != nil {
		http.Error(w, "too many streams", http.StatusTooManyRequests)
		return
	}
	defer cancel()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no") // defeat proxy buffering of the stream
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	rc := http.NewResponseController(w)
	ctx := r.Context()

	// Flush envelopes queued while the user was offline (arrival order), each
	// decrypted for display. NOT acked — see the function doc: the console must
	// leave the shared queue intact so the recipient's app still gets its copy.
	for _, env := range queued {
		if txt, verified, ok := a.talkDecryptVerify(self, env.From, env.Ciphertext); ok {
			a.maybeFetchSenderKey(self, env.From, verified)
			out := talkMessageOut{
				ID: env.ID, From: env.From, Text: txt,
				CreatedAt:   env.CreatedAt.UTC().Format(time.RFC3339),
				ExpiresAt:   env.ExpiresAt.UTC().Format(time.RFC3339),
				BurnSeconds: env.BurnSeconds,
				Mode:        env.Mode,
				Verified:    verified,
			}
			if !writeSSE(w, rc, "message", out) {
				return
			}
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
				return // evicted: a newer stream for this mailbox took over
			}
			switch evt.Type {
			case "envelope":
				p, ok := evt.Payload.(vtalk.EnvelopePayload)
				if !ok {
					continue
				}
				raw, derr := base64.StdEncoding.DecodeString(p.Ciphertext)
				if derr != nil {
					continue
				}
				txt, verified, ok := a.talkDecryptVerify(self, p.From, raw)
				if !ok {
					continue
				}
				a.maybeFetchSenderKey(self, p.From, verified)
				out := talkMessageOut{ID: p.ID, From: p.From, Text: txt, CreatedAt: p.CreatedAt, ExpiresAt: p.ExpiresAt, BurnSeconds: p.BurnSeconds, Mode: p.Mode, Verified: verified}
				if !writeSSE(w, rc, "message", out) {
					return
				}
				flusher.Flush()
				// No ack: the console is a passive viewer, not the reader. Acking
				// here would delete the recipient's queued copy before their app
				// could flush it. See the function doc.
			case "receipt":
				if !writeSSE(w, rc, "receipt", evt.Payload) {
					return
				}
				flusher.Flush()
			}
		case <-ping.C:
			if !writeSSE(w, rc, "ping", struct{}{}) {
				return
			}
			flusher.Flush()
		}
	}
}

// handleVayuOSTalkSend encrypts a plaintext message to the recipient (signed as
// the signed-in mailbox) and hands the armored ciphertext to the relay. The
// browser sends plaintext; it leaves this process as ciphertext and never
// touches disk.
func (a *App) handleVayuOSTalkSend(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !a.vayuTalkEnabled() {
		writeAPIError(w, r, http.StatusServiceUnavailable, "vayutalk-disabled", "VayuTalk is not available", "")
		return
	}
	var body struct {
		To         string `json:"to"`
		Text       string `json:"text"`
		TTLSeconds int    `json:"ttl_seconds"`
		Mode       string `json:"mode"`
		As         string `json:"as"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 256*1024)).Decode(&body); err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "bad-json", "Invalid request body", "")
		return
	}
	// Resolve the sending identity, honouring the switcher's `as` value only if
	// this session is entitled to it (talkSelf enforces the boundary).
	self := a.talkSelf(r, body.As)
	if self == "" {
		writeAPIError(w, r, http.StatusForbidden, "no-mailbox", "No mailbox is assigned to your account", "")
		return
	}
	to := strings.TrimSpace(strings.ToLower(body.To))
	text := strings.TrimSpace(body.Text)
	mode := strings.TrimSpace(body.Mode)
	if mode == "" {
		mode = "store"
	}
	if to == "" || text == "" {
		writeAPIError(w, r, http.StatusBadRequest, "validation_error", "Recipient and message are required", "")
		return
	}
	if mode != "live" && mode != "store" {
		writeAPIError(w, r, http.StatusBadRequest, "validation_error", "mode must be live or store", "")
		return
	}

	// Onion-to-onion (ADR-0142): a recipient on a DIFFERENT .onion is reached by
	// federation over Tor, never the local in-process hub. sendOverOnion fetches
	// their key over Tor, encrypts locally, and delivers to their install — or
	// returns a distinct, honest reason for each failure (federation off, no Tor
	// SOCKS configured, key not reachable, peer refused).
	if talkRecipientRemoteOnion(self, to) {
		a.sendOverOnion(w, r, self, to, text, body.TTLSeconds, mode)
		return
	}

	// Resolve the recipient's key first (minting on demand for a local mailbox,
	// WKD lookup otherwise). A missing recipient key is the common, actionable
	// failure — surface it distinctly from a sender-side problem.
	if _, _, kerr := a.vayuTalk.PubKey(to); kerr != nil {
		msg := "No VayuTalk key found for " + to + ". They need a VayuMail mailbox on this server (or a published key) before you can message them."
		if config.Cfg.OnionMode {
			// Tor world: there is no onion-to-onion delivery yet, so a code on
			// another .onion genuinely cannot be reached. Say so plainly instead of
			// pretending the message was sent (ADR-0141).
			msg = "Can't reach " + to + " over Tor. VayuTalk in the Tor world can't yet deliver to a code hosted on a different .onion."
		}
		writeAPIError(w, r, http.StatusNotFound, "no-recipient-key", msg, "")
		return
	}
	// Ensure our own signing key exists (a mailbox that predates auto-keygen),
	// then sign+encrypt as the sender.
	a.ensureTalkKeypair(self)
	ciphertext, err := a.vayuPGP.EncryptAndSignFromEmail([]byte(text), to, self)
	if err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "sender-key",
			"Couldn't prepare your encryption key. Try reloading; if this persists, re-sync your key under VayuMail → PGP Keys.", "")
		return
	}

	id, delivered, queued, err := a.vayuTalk.Send(self, to, ciphertext, body.TTLSeconds, mode)
	if err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "send-error", "Could not send message", "")
		return
	}
	writeJSON(w, r, http.StatusOK, map[string]interface{}{
		"id":        id,
		"delivered": delivered,
		"queued":    queued,
	})
}

// talkDecryptVerify decrypts an armored VayuTalk envelope with the mailbox's own
// key AND reports whether it carries a valid signature from sender (ADR-0142).
// verified is true only when the sender's key is local and the signature checks
// out; the plaintext is returned regardless, so confidentiality never depends on
// being able to verify. It returns ok=false (never an error, never a log line) on
// any decryption failure so a single unreadable envelope can never break the
// stream or leak that it failed.
func (a *App) talkDecryptVerify(email, sender string, ciphertext []byte) (text string, verified, ok bool) {
	if a.vayuPGP == nil {
		return "", false, false
	}
	pt, ver, err := a.vayuPGP.DecryptAndVerifyForEmail(ciphertext, email, sender)
	if err != nil {
		return "", false, false
	}
	return string(pt), ver, true
}

// maybeFetchSenderKey, when a message from a DIFFERENT onion could not be verified
// (its sender key is not yet local), kicks off a best-effort over-Tor fetch so
// subsequent messages from that sender verify. Non-blocking; deduped inside
// ensureOnionSenderKey. Driven only by the operator's own authenticated stream.
func (a *App) maybeFetchSenderKey(self, from string, verified bool) {
	if verified || !talkRecipientRemoteOnion(self, from) {
		return
	}
	go a.ensureOnionSenderKey(from)
}

// ensureTalkKeypair mints a keypair for a local address on demand so a mailbox
// that predates auto-keygen can still send and receive VayuTalk messages. It is
// best-effort: a failure here surfaces later as a clean "no key" send error.
func (a *App) ensureTalkKeypair(email string) {
	if a.vayuPGP == nil {
		return
	}
	if _, err := a.vayuPGP.GetPublicKey(email); err == nil {
		return
	}
	name := email
	if i := strings.Index(name, "@"); i > 0 {
		name = name[:i]
	}
	_, _ = a.vayuPGP.EnsureKeypair(&vpgp.PGPUser{UserID: email, Name: name, Email: email})
}
