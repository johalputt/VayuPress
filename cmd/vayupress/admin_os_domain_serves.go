// SPDX-License-Identifier: Apache-2.0

package main

// admin_os_domain_serves.go — changing what a domain serves, after it exists.
//
// # The gap this closes
//
// A domain's site type and its mail switch were chosen once, on the "Add a
// domain" form, and then frozen forever. `domain.Registry.Update` had existed
// the whole time and no handler ever called it, so the registry could express
// the change and the panel could not ask for it.
//
// The practical consequence, in an operator's words: "if I want website, blog
// and mail on a domain there is no option". The combination was always
// representable — a business site at "/" with the blog at "/blog", plus mail —
// but only if it happened to be picked at the moment the domain was added, and
// never afterwards.
//
// # What mail_enabled actually does, since the copy has to be true
//
// It provisions, it presents, AND — for a secondary domain — it gates inbound
// delivery:
//
//   - setup-vayudomain.sh adds mail.<host> to the domain's certificate when it
//     is on (and only when that name resolves);
//   - the client's own site page shows or hides its mail card;
//   - the DNS page lists the mail records for the domain;
//   - the mailbox allowance is meaningful only while it is on;
//   - the receiving server declines every recipient on the domain while it is
//     off, so senders get a bounce.
//
// THIS BLOCK USED TO SAY THE OPPOSITE, in the present tense: "It is NOT
// consulted by the mail stack … switching mail off does not stop delivery to a
// mailbox that already exists." The card repeated it, and it was wrong.
// SMTPServer.recipientAccepted asks Config.AcceptsMailDomain, which for a
// non-primary host asks MailAccepts — wired in vayuos.go to
// App.acceptsSecondaryMailDomain, which reads d.MailEnabled live from this very
// registry. The engine's isLocalRecipient asks the same predicate.
//
// The cost of the wrong version was the shape that matters: it named "hides its
// mail card from the client" as the reason to flip the switch, and denied the
// consequence of doing so. An operator following it stops a client's incoming
// mail and is told on the same screen that they have not.
//
// What turning it off does NOT do, and the card has to keep saying so: nothing
// is deleted, and no one is locked out. The predicate reaches three places in
// the mail stack and none of them is authentication:
//
//   - smtpd's RCPT gate (the delivery refusal described above);
//   - the engine's local-recipient check (the same refusal, from the other side);
//   - validRecoveryContact, which REFUSES a recovery address on a domain this
//     install hosts — so turning mail off makes such an address acceptable
//     rather than rejected. That is a widening, and it costs nobody access.
//
// Sign-in is untouched, so existing mailboxes and their stored mail stay
// readable over IMAP, POP3 and webmail.
//
// THAT LIST USED TO SAY "exactly two call sites", and it was wrong: the third
// arrives as an `accepts func(domain string) bool` parameter and never names
// AcceptsMailDomain, so a search for the name did not find it. The count was
// asserted in the changelog and in this comment before anyone counted. It cost
// nothing here because the third site is a widening, but a precise number stated
// without checking is the same defect as a control claimed without one — hence
// mail_switch_claim_test.go now pins the SET of call sites, in both spellings,
// so a fourth fails the build and forces this paragraph to be re-earned.

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/johalputt/vayupress/internal/domain"
)

// handleOSDomainServes changes a domain's site type and/or its mail switch.
//
// Both travel together because they are one decision on the page — "what does
// this domain do" — and splitting them into two endpoints would let a client
// apply half of it and leave the panel showing a state the registry never
// reached.
func (a *App) handleOSDomainServes(w http.ResponseWriter, r *http.Request) {
	if a.domains == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "unavailable", "domain registry not initialised", "")
		return
	}
	if !a.isAdminRequest(r) {
		writeAPIError(w, r, http.StatusForbidden, "forbidden", "admin role required", "")
		return
	}
	id := chi.URLParam(r, "id")
	var body struct {
		SiteType    string `json:"site_type"`
		MailEnabled bool   `json:"mail_enabled"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 512)).Decode(&body); err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "bad-request", "invalid JSON", "")
		return
	}
	// An empty site type is the historic "blog" default everywhere else in this
	// codebase (EffectiveSiteType), so accepting it here keeps one meaning for
	// one value rather than inventing a second.
	if body.SiteType == "" {
		body.SiteType = domain.SiteBlog
	}
	// Refuse an unknown type rather than storing it. The registry validates too,
	// but a rejection that names the accepted values is the difference between
	// an operator fixing a typo and an operator filing a bug.
	if !knownSiteType(body.SiteType) {
		writeAPIError(w, r, http.StatusBadRequest, "bad-request",
			"unknown site type "+body.SiteType, "")
		return
	}

	before, err := a.domains.ByID(r.Context(), id)
	if err != nil {
		writeAPIError(w, r, http.StatusNotFound, "not-found", "no such domain", "")
		return
	}
	// The PRIMARY domain's site type is the install's own site.mode, set on
	// Settings and mirrored into the registry at boot. Editing it here would put
	// two controls on one value and let them disagree, which is how a panel
	// starts lying about the install it describes.
	if before.IsPrimary {
		writeAPIError(w, r, http.StatusBadRequest, "primary-domain",
			"this is the install's primary domain — change what it serves in Settings, "+
				"which is the one control for that value", "")
		return
	}

	d, err := a.domains.Update(r.Context(), id, body.SiteType, body.MailEnabled)
	if err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "update-failed", err.Error(), "")
		return
	}

	// Turning mail ON is not complete until the domain's certificate carries
	// mail.<host>, and only a provisioning run can do that. Say so in the
	// response rather than letting a green tick imply finished work.
	needsProvision := body.MailEnabled && !before.MailEnabled
	writeJSON(w, r, http.StatusOK, map[string]any{
		"status":          "ok",
		"site_type":       d.SiteType,
		"mail_enabled":    d.MailEnabled,
		"needs_provision": needsProvision,
	})
}

// knownSiteType reports whether v is one of the catalogued site types.
//
// Reads the same siteTypeOptions the form and the labels use, so a type added
// to that catalogue is accepted here without a second list to keep in step —
// two copies of a truth is how the theme exporter leaked a credential.
func knownSiteType(v string) bool {
	for _, o := range siteTypeOptions {
		if o.Value == v {
			return true
		}
	}
	return false
}

// domainServesCard is the operator's control for what a domain does.
//
// The labels are written as OUTCOMES rather than as the enum values behind
// them. "business_subpath" tells an operator nothing; "your website at the
// root, your blog at /blog" tells them exactly what a visitor will see, which
// is the question they are actually answering.
func domainServesCard(d domain.Domain, mailOnInstall bool) string {
	if d.IsPrimary {
		return `<div class="card">
  <div class="settings-block-title">What this domain serves</div>
  <p class="text-sm muted">This is the install's primary domain. What it serves is the install's own
  site mode, set on <a href="/os/settings">Settings</a> — one value with one control, so the two can
  never disagree.</p>
</div>`
	}

	cur := d.EffectiveSiteType()
	var opts strings.Builder
	for _, o := range siteTypeOptions {
		sel := ""
		if o.Value == cur {
			sel = ` selected`
		}
		opts.WriteString(`<option value="` + htmlEscape(o.Value) + `"` + sel + `>` +
			htmlEscape(o.Label+" — "+servesOutcome(o.Value)) + `</option>`)
	}

	checked := ""
	if d.MailEnabled {
		checked = ` checked`
	}

	// The two states of the install-wide mail switch are different situations
	// and an operator needs them distinguished: a domain marked for mail on an
	// install where mail is off is not misconfigured, it is waiting.
	installNote := ""
	if !mailOnInstall {
		installNote = `<p class="text-sm muted"><strong>Mail is switched off for this whole install</strong>,
    so turning it on here marks the domain for mail without making any mailbox reachable yet.
    Turn mail on install-wide in <a href="/os/vayumail">VayuMail</a> first.</p>`
	}

	return `<div class="card" id="serves-card" data-id="` + htmlEscape(d.ID) + `">
  <div class="settings-block-title">What this domain serves</div>
  <p class="text-sm muted">A domain can serve a blog, a website, or both — and can carry branded mail
  alongside any of them. Change it whenever you like; nothing already published is moved or deleted.</p>
  <div class="form-grid">
    <label class="field"><span class="field-label">This domain serves</span>
      <select id="serves-type" class="input">` + opts.String() + `</select>
      <span class="field-hint">Choose “Business + /blog” when you want a website and a blog on the
      same domain — the website answers at the root and the blog at <code>/blog</code>.</span></label>
    <label class="field field--check"><input type="checkbox" id="serves-mail"` + checked + `>
      <span class="field-label">Branded mail on this domain</span></label>
  </div>
  ` + installNote + `
  <p class="text-sm muted"><strong>Turning mail on</strong> adds <code>mail.` + htmlEscape(d.Host) +
		`</code> to this domain's certificate — which happens on the next provisioning run, not
  instantly. Press <strong>Provision now</strong> above after saving.</p>
  <p class="text-sm muted"><strong>Turning mail off</strong> stops this domain being set up for mail,
  hides its mail card from the client, and <strong>refuses new mail for it</strong>: the receiving
  server declines every address on <code>` + htmlEscape(d.Host) + `</code>, so anyone writing to a
  mailbox here gets a bounce. Nothing is deleted and nobody is locked out — existing mailboxes and
  their stored mail stay readable in webmail, IMAP and POP3. If you meant to close one mailbox,
  delete it in VayuMail instead; this switch closes the whole domain to incoming mail.</p>
  <div class="vm-row">
    <button type="button" class="btn btn--primary btn--sm" data-serves-save>Save</button>
    <span id="serves-status" class="text-sm muted" role="status" aria-live="polite"></span>
  </div>
</div>`
}

// servesOutcome describes a site type as a visitor experiences it.
func servesOutcome(v string) string {
	switch v {
	case domain.SiteBlog:
		return "your blog at the root"
	case domain.SiteBusiness:
		return "website at the root, blog on a blog. subdomain"
	case domain.SiteBusinessSubpath:
		return "website at the root, blog at /blog"
	case domain.SiteStatic:
		return "a static bundle you upload"
	case domain.SiteMailOnly:
		return "no public site — mail only"
	}
	return v
}

// domainServesScript wires the Save button.
//
// The domain id travels in a data attribute and is read from the DOM rather
// than interpolated into JavaScript. Nothing here can be broken by a quote in
// a value, because no value is ever spliced into source.
//
// Its own IIFE, deliberately. The site console's main script was once broken by
// text surgery and every control on the page went inert together, because a
// parse error binds NOTHING — a separate block means a mistake here cannot take
// Provision now, Issue login and Save allowance down with it.
func domainServesScript(nonce string) string {
	return `<script nonce="` + nonce + `">
(function(){'use strict';
function csrf(){var m=document.cookie.match(/(?:^|;\s*)vp_csrf=([^;]+)/);return m?decodeURIComponent(m[1]):'';}
var card=document.getElementById('serves-card');
var btn=document.querySelector('[data-serves-save]');
var st=document.getElementById('serves-status');
function show(t){if(st)st.textContent=t;}
if(!btn||!card)return;
var ID=card.getAttribute('data-id')||'';
if(!ID)return;
btn.addEventListener('click',function(){
  var t=document.getElementById('serves-type');
  var m=document.getElementById('serves-mail');
  if(!t||!m)return;
  btn.disabled=true;show('Saving…');
  fetch('/os/api/domains/'+encodeURIComponent(ID)+'/serves',
    {method:'POST',headers:{'Content-Type':'application/json','X-CSRF-Token':csrf()},
     body:JSON.stringify({site_type:t.value,mail_enabled:m.checked})})
    .then(function(r){return r.json().then(function(j){return {ok:r.ok,j:j};})
      .catch(function(){return {ok:r.ok,j:null};});})
    .then(function(res){
      btn.disabled=false;
      if(!res.ok){show((res.j&&res.j.error&&res.j.error.message)||'Could not save');return;}
      if(res.j&&res.j.needs_provision){
        show('Saved — press Provision now to add the mail name to this certificate.');
      }else{show('Saved.');}
    })
    .catch(function(e){btn.disabled=false;show('Error: '+e);});
});
})();
</script>`
}
