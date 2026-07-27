// SPDX-License-Identifier: Apache-2.0

package render

import (
	"crypto/sha256"
	"encoding/hex"
	"html/template"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
)

// PortalJS is the VayuPortal membership widget: a floating launch button plus a
// slide-in panel offering sign-up, passwordless sign-in, "Sign in with
// VayuMail" (mailbox credentials + optional TOTP), and a signed-in account
// view. It is a self-bootstrapping, dependency-free, same-origin script so it
// satisfies the strict `script-src 'self'` CSP without a nonce and works even
// on disk-cached public pages.
//
// It renders nothing unless GET /api/v1/members/me reports membership enabled,
// and it transparently upgrades the existing nav "Sign in" / "Sign up" links to
// open the panel instead of navigating away. No third-party code, no inline
// event handlers (all listeners are attached programmatically).
//
// NOTE: the source below must not contain back-tick characters — it is embedded
// in a Go raw string literal.
const PortalJS = `(function () {
  'use strict';
  if (window.__vpPortalLoaded) { return; }
  window.__vpPortalLoaded = true;

  var ICON_USER = '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M20 21v-2a4 4 0 0 0-4-4H8a4 4 0 0 0-4 4v2"></path><circle cx="12" cy="7" r="4"></circle></svg>';
  var ICON_MAIL = '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><rect x="3" y="5" width="18" height="14" rx="2"></rect><path d="m3 7 9 6 9-6"></path></svg>';
  // A drawn X rather than the &times; glyph, which sits off-centre in many fonts
  // and made the button look misaligned at small sizes.
  var ICON_CLOSE = '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" aria-hidden="true"><path d="M18 6 6 18M6 6l12 12"></path></svg>';
  var ICON_CHEV = '<svg class="vp-acc__chev" viewBox="0 0 24 24" width="16" height="16" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="m6 9 6 6 6-6"></path></svg>';

  // acc builds one Monetization-style accordion row: an icon tile, a title with a
  // one-line subtitle, a status chip, and a rotating chevron; the body reveals on
  // expand. Native <details>, so it needs no JavaScript and stays CSP-safe.
  function acc(icon, title, sub, chip, body, open) {
    return '<details class="vp-acc"' + (open ? ' open' : '') + '>' +
      '<summary class="vp-acc__sum">' +
      '<span class="vp-acc__ic" aria-hidden="true">' + icon + '</span>' +
      '<span class="vp-acc__head"><span class="vp-acc__title">' + esc(title) + '</span>' +
      '<span class="vp-acc__sub">' + esc(sub) + '</span></span>' +
      (chip || '') + ICON_CHEV +
      '</summary>' +
      '<div class="vp-acc__body">' + body + '</div></details>';
  }

  function chip(label, on) {
    return '<span class="vp-chip' + (on ? ' vp-chip--on' : '') + '">' + esc(label) + '</span>';
  }

  var state = { enabled: false, vayumail: false, auth: false, member: null };
  var view = 'signup';
  var lastFocus = null;
  var trigger, overlay, panel, body;

  function el(tag, cls, html) {
    var e = document.createElement(tag);
    if (cls) { e.className = cls; }
    if (html != null) { e.innerHTML = html; }
    return e;
  }

  function esc(s) {
    return String(s == null ? '' : s).replace(/[&<>"']/g, function (c) {
      return { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c];
    });
  }

  function brandName() {
    var b = document.querySelector('.vayu-nav-brand');
    var t = b ? b.textContent.trim() : '';
    return t || 'Membership';
  }

  function postJSON(url, data) {
    return fetch(url, {
      method: 'POST',
      credentials: 'same-origin',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(data || {}),
    }).then(function (r) {
      return r.json().catch(function () { return {}; }).then(function (b) {
        return { ok: r.ok, status: r.status, body: b };
      });
    });
  }

  // ── Views ──────────────────────────────────────────────────────────────────

  function vmButton() {
    if (!state.vayumail) { return ''; }
    return '<div class="vp-portal-or">or</div>' +
      '<button type="button" class="vp-portal-btn vp-portal-btn--ghost vp-portal-vmbtn" data-vp-go="vayumail">' +
      ICON_MAIL + '<span>Sign in with VayuMail</span></button>';
  }

  function viewSignup() {
    return '<h2 class="vp-portal-title">Become a member</h2>' +
      '<p class="vp-portal-sub">Join free and unlock every story. No password to remember — we email you a one-time sign-in link.</p>' +
      '<form class="vp-portal-form" data-vp-form="magic" novalidate>' +
      '<label class="vp-portal-label" for="vp-su-email">Email address</label>' +
      '<input class="vp-portal-input" id="vp-su-email" type="email" name="email" required autocomplete="email" placeholder="you@example.com">' +
      '<button class="vp-portal-btn" type="submit">Sign up free</button>' +
      '</form>' + vmButton() +
      '<ul class="vp-portal-perks"><li>Full access to members-only posts</li><li>New stories delivered to your inbox</li><li>One link to sign in on any device</li></ul>' +
      '<p class="vp-portal-foot">Already a member? <button type="button" class="vp-portal-link" data-vp-go="signin">Sign in</button></p>' +
      '<div class="vp-portal-msg" aria-live="polite"></div>';
  }

  function viewSignin() {
    return '<h2 class="vp-portal-title">Sign in</h2>' +
      '<p class="vp-portal-sub">Enter your email and we will send a one-time sign-in link. No password required.</p>' +
      '<form class="vp-portal-form" data-vp-form="magic" novalidate>' +
      '<label class="vp-portal-label" for="vp-si-email">Email address</label>' +
      '<input class="vp-portal-input" id="vp-si-email" type="email" name="email" required autocomplete="email" placeholder="you@example.com">' +
      '<button class="vp-portal-btn" type="submit">Email me a sign-in link</button>' +
      '</form>' + vmButton() +
      '<p class="vp-portal-foot">New here? <button type="button" class="vp-portal-link" data-vp-go="signup">Create a free account</button></p>' +
      '<div class="vp-portal-msg" aria-live="polite"></div>';
  }

  function viewVayuMail(totp) {
    var code = totp
      ? '<label class="vp-portal-label" for="vp-vm-code">Two-factor code</label>' +
        '<input class="vp-portal-input" id="vp-vm-code" type="text" name="code" inputmode="numeric" autocomplete="one-time-code" placeholder="123456" maxlength="6">'
      : '';
    return '<h2 class="vp-portal-title">Sign in with VayuMail</h2>' +
      '<p class="vp-portal-sub">Use your VayuMail mailbox address and password.</p>' +
      '<form class="vp-portal-form" data-vp-form="vayumail" novalidate>' +
      '<label class="vp-portal-label" for="vp-vm-email">Email address</label>' +
      '<input class="vp-portal-input" id="vp-vm-email" type="email" name="email" required autocomplete="username" placeholder="you@example.com">' +
      '<label class="vp-portal-label" for="vp-vm-pass">Password</label>' +
      '<input class="vp-portal-input" id="vp-vm-pass" type="password" name="password" required autocomplete="current-password" placeholder="Your password">' +
      code +
      '<button class="vp-portal-btn" type="submit">Sign in</button>' +
      '</form>' +
      '<p class="vp-portal-foot"><button type="button" class="vp-portal-link" data-vp-go="signin">Use a sign-in link instead</button>' +
      ' · <a class="vp-portal-link" href="/mail/recover">Forgot your password?</a></p>' +
      '<div class="vp-portal-msg" aria-live="polite"></div>';
  }

  function viewAccount() {
    var m = state.member || {};
    var name = m.name || 'there';
    var initial = (name.charAt(0) || '?').toUpperCase();
    var plan = m.paid ? 'Premium member' : 'Free member';
    var mailBtn = '';
    if (m.mail) {
      mailBtn = m.mail.console
        ? '<a class="vp-portal-btn" href="/os">Open VayuOS console</a>'
        : '<a class="vp-portal-btn" href="/os/vayumail/inbox">Open VayuMail</a>';
    }
    var avatarInner = m.avatar
      ? '<img class="vp-portal-avatar-img" src="' + esc(m.avatar) + '" alt="">'
      : esc(initial);

    // Identity first, then one collapsed row per area of the account. The previous
    // view was a flat stack of seven equal-weight buttons, which said nothing about
    // what each did or what state it was in; these rows carry the same icon ·
    // title · subtitle · status-chip grammar as the console, so the panel can be
    // read at a glance instead of scanned.
    var rows = acc('&#128142;', 'Membership', plan, chip(m.paid ? 'Active' : 'Free', m.paid),
      '<p class="vp-acc__note">' + (m.paid
        ? 'Your premium access is active. Billing dates and payment history live on your account page.'
        : 'You are on the free plan. Premium unlocks members-only posts and a private mail address.') + '</p>' +
      '<div class="vp-portal-actions">' +
      // One primary action per row. For a free member that is "See plans"; managing
      // the account is the secondary path, so it stays a ghost button rather than
      // competing with it as a second filled button.
      '<a class="vp-portal-btn vp-portal-btn--ghost" href="/members/account">Manage account</a>' +
      (m.paid ? '' : '<a class="vp-portal-btn" href="/pricing">See plans</a>') +
      '</div>', true);

    if (m.mail || m.paid) {
      var mailSub = m.mail ? 'Mailbox ready' : 'Included with your plan';
      rows += acc('&#9993;', 'VayuMail', mailSub, chip(m.mail ? 'Ready' : 'Claim', !!m.mail),
        '<p class="vp-acc__note">' + (m.mail
          ? 'Your private address is live. Open it here or in the console.'
          : 'Your plan includes a private address on this domain — pick a name to claim it.') + '</p>' +
        '<div class="vp-portal-actions">' + mailBtn +
        '<button type="button" class="vp-portal-btn vp-portal-btn--ghost" data-vp-go="mailbox">Your mailbox</button>' +
        '</div>');
    }

    rows += acc('&#128172;', 'Your comments', 'Replies and their status', '',
      '<p class="vp-acc__note">See where you commented and whether each one is live or still awaiting review.</p>' +
      '<div class="vp-portal-actions">' +
      '<button type="button" class="vp-portal-btn vp-portal-btn--ghost" data-vp-go="activity">Open your comments</button>' +
      '</div>');

    rows += acc('&#128227;', 'Advertise here', 'Reach this audience', '',
      '<p class="vp-acc__note">Book a slot on this site and manage your creatives.</p>' +
      '<div class="vp-portal-actions">' +
      '<button type="button" class="vp-portal-btn vp-portal-btn--ghost" data-vp-go="advertise">Open advertising</button>' +
      '</div>');

    return '<div class="vp-portal-account-id">' +
      '<div class="vp-portal-avatar">' + avatarInner + '</div>' +
      '<div class="vp-portal-idtext"><div class="vp-portal-acc-name">' + esc(name) + '</div>' +
      '<div class="vp-portal-acc-mail">' + esc(m.email || '') + '</div></div>' +
      chip(m.paid ? 'Premium' : 'Free', m.paid) + '</div>' +
      '<div class="vp-acc-stack">' + rows + '</div>' +
      // Sign out sits last and quiet: it is the one action you never want to hit by
      // accident, so it is separated from the rows above rather than sharing weight.
      '<div class="vp-portal-signout">' +
      '<button type="button" class="vp-portal-btn vp-portal-btn--ghost" data-vp-logout>Sign out</button>' +
      '</div>' +
      '<div class="vp-portal-msg" aria-live="polite"></div>';
  }

  // Activity view: the member's own comments with their moderation status, so a
  // commenter can see where they replied and whether each is live or pending.
  function viewActivity() {
    return '<button type="button" class="vp-portal-link vp-portal-back" data-vp-go="account">&larr; Back</button>' +
      '<h2 class="vp-portal-title">Your activity</h2>' +
      '<p class="vp-portal-sub">Comments you have posted and their status.</p>' +
      '<div class="vp-portal-activity" data-vp-activity><div class="vp-portal-activity-loading">Loading your comments…</div></div>';
  }

  function statusBadge(s) {
    var map = {
      approved: ['✅ Live', 'ok'],
      pending: ['⏳ Awaiting review', 'pending'],
      rejected: ['🚫 Not approved', 'err'],
      spam: ['🚫 Not approved', 'err']
    };
    var e = map[s] || ['•', 'ok'];
    return '<span class="vp-portal-badge vp-portal-badge--' + e[1] + '">' + e[0] + '</span>';
  }

  function loadActivity() {
    var box = body.querySelector('[data-vp-activity]');
    if (!box) { return; }
    fetch('/api/v1/members/comments', { credentials: 'same-origin', headers: { 'Accept': 'application/json' } })
      .then(function (r) { return r.ok ? r.json() : { comments: [] }; })
      .then(function (d) {
        var list = (d && d.comments) || [];
        if (!list.length) {
          box.innerHTML = '<div class="vp-portal-activity-empty">📝 You haven\'t commented yet. Join a conversation on any story!</div>';
          return;
        }
        var html = '';
        list.forEach(function (c) {
          var where = c.title || c.slug || 'a post';
          var link = c.slug ? '/' + esc(c.slug) + '#comments' : '#';
          var when = (c.created_at || '').slice(0, 10);
          html += '<div class="vp-portal-activity-item">' +
            '<div class="vp-portal-activity-head">' + statusBadge(c.status) +
            '<span class="vp-portal-activity-when">' + esc(when) + '</span></div>' +
            '<div class="vp-portal-activity-body">' + esc(c.body || '') + '</div>' +
            '<a class="vp-portal-activity-link" href="' + link + '">on “' + esc(where) + '” →</a>' +
            '</div>';
        });
        box.innerHTML = html;
      })
      .catch(function () { box.innerHTML = '<div class="vp-portal-activity-empty">Could not load your activity.</div>'; });
  }

  // Advertise view: any signed-in member (paid or free) submits an image ad for a
  // placement, pays a flat fee, and the ad enters the operator's review queue —
  // it only goes live once the operator approves it. Server refuses member HTML;
  // only an image URL + link is accepted.
  function viewAdvertise() {
    return '<button type="button" class="vp-portal-link vp-portal-back" data-vp-go="account">&larr; Back</button>' +
      '<h2 class="vp-portal-title">Advertise here</h2>' +
      '<div class="vp-portal-mailbox" data-vp-advertise><div class="vp-portal-activity-loading">Loading…</div></div>';
  }

  function adPlacementOptions() {
    var ps = [
      ['header', 'Header (top of page)'],
      ['above_post', 'Above the post'],
      ['below_post', 'Below the post'],
      ['sidebar', 'Sidebar'],
      ['footer', 'Footer']
    ];
    return ps.map(function (p) {
      return '<option value="' + p[0] + '">' + esc(p[1]) + '</option>';
    }).join('');
  }

  function adStatusBadge(s) {
    var map = {
      approved: ['✅ Live', 'ok'],
      pending_review: ['⏳ In review', 'pending'],
      pending_payment: ['💳 Awaiting payment', 'pending'],
      rejected: ['🚫 Not approved', 'err']
    };
    var e = map[s] || ['•', 'ok'];
    return '<span class="vp-portal-badge vp-portal-badge--' + e[1] + '">' + e[0] + '</span>';
  }

  function loadAdvertise() {
    var box = body.querySelector('[data-vp-advertise]');
    if (!box) { return; }
    fetch('/api/v1/members/ads', { credentials: 'same-origin', headers: { 'Accept': 'application/json' } })
      .then(function (r) { return r.ok ? r.json() : {}; })
      .then(function (d) {
        d = d || {};
        var mine = '';
        if (d.ads && d.ads.length) {
          mine = '<div class="vp-portal-sub" style="margin-top:1rem">Your ads</div>' +
            d.ads.map(function (a2) {
              return '<div class="vp-portal-plan" style="margin-bottom:.5rem">' +
                '<div class="vp-portal-plan-label">' + esc(a2.placement || '') + ' ' + adStatusBadge(a2.status) + '</div>' +
                '<div class="vp-portal-plan-name" style="font-size:.85rem;word-break:break-all">' + esc(a2.image_url || '') + '</div></div>';
            }).join('');
        }
        if (!d.available) {
          box.innerHTML = '<p class="vp-portal-sub">Advertising is not open on this site right now. Please check back later.</p>' + mine;
          return;
        }
        box.innerHTML =
          '<p class="vp-portal-sub">Promote your product on this site. Submit an image ad and pick where it appears — ' +
          '<strong>' + esc(d.price || '') + '</strong> per placement. Every ad is reviewed before it goes live.</p>' +
          '<form class="vp-portal-form" data-vp-ad-form novalidate>' +
          '<label class="vp-portal-label" for="vp-ad-place">Where should it appear?</label>' +
          '<select class="vp-portal-input" id="vp-ad-place">' + adPlacementOptions() + '</select>' +
          '<label class="vp-portal-label" for="vp-ad-img">Image URL</label>' +
          '<input class="vp-portal-input" id="vp-ad-img" type="url" autocomplete="off" spellcheck="false" placeholder="https://…/banner.png">' +
          '<label class="vp-portal-label" for="vp-ad-link">Link URL (where clicks go)</label>' +
          '<input class="vp-portal-input" id="vp-ad-link" type="url" autocomplete="off" spellcheck="false" placeholder="https://your-site.example">' +
          '<label class="vp-portal-label" for="vp-ad-alt">Alt text</label>' +
          '<input class="vp-portal-input" id="vp-ad-alt" type="text" autocomplete="off" placeholder="Describe your ad">' +
          '<button class="vp-portal-btn" type="submit">Continue to payment · ' + esc(d.price || '') + '</button>' +
          '</form>' +
          '<p class="vp-portal-foot">After payment your ad waits in the review queue. You’ll be notified once it’s live.</p>' +
          '<div class="vp-portal-msg" aria-live="polite"></div>' + mine;
        wireAdvertiseForm();
      })
      .catch(function () { box.innerHTML = '<div class="vp-portal-activity-empty">Could not load advertising.</div>'; });
  }

  function wireAdvertiseForm() {
    var form = body.querySelector('[data-vp-ad-form]');
    if (!form) { return; }
    form.addEventListener('submit', function (e) {
      e.preventDefault();
      var place = (form.querySelector('#vp-ad-place').value || '').trim();
      var img = (form.querySelector('#vp-ad-img').value || '').trim();
      var link = (form.querySelector('#vp-ad-link').value || '').trim();
      var alt = (form.querySelector('#vp-ad-alt').value || '').trim();
      if (!img) { msg('An image URL is required.', 'err'); return; }
      var btn = form.querySelector('.vp-portal-btn');
      btn.disabled = true; btn.textContent = 'Starting checkout…';
      postJSON('/api/v1/members/ads', { placement: place, image_url: img, link_url: link, alt: alt }).then(function (res) {
        btn.disabled = false;
        if (res.ok && res.body && res.body.mode === 'stripe' && res.body.checkout_url) {
          window.top.location.href = res.body.checkout_url;
          return;
        }
        if (res.ok && res.body && res.body.reference) {
          msg('Order ' + res.body.reference + ' created. ' + (res.body.instructions || 'Follow the payment instructions to complete it; your ad enters review once paid.'), 'ok');
          form.reset();
          return;
        }
        msg((res.body && res.body.error && res.body.error.message) || 'Could not start checkout.', 'err');
        btn.textContent = 'Continue to payment';
      });
    });
  }

  // Mailbox view: a paid member on a mail-enabled tier claims their included
  // VayuMail address (generic only — reserved names are refused server-side),
  // sets a password, and gets a private PGP mailbox with their tier's quota.
  function viewMailbox() {
    return '<button type="button" class="vp-portal-link vp-portal-back" data-vp-go="account">&larr; Back</button>' +
      '<h2 class="vp-portal-title">Your mailbox</h2>' +
      '<div class="vp-portal-mailbox" data-vp-mailbox><div class="vp-portal-activity-loading">Loading…</div></div>';
  }

  function loadMailbox() {
    var box = body.querySelector('[data-vp-mailbox]');
    if (!box) { return; }
    fetch('/api/v1/members/mailbox', { credentials: 'same-origin', headers: { 'Accept': 'application/json' } })
      .then(function (r) { return r.ok ? r.json() : {}; })
      .then(function (d) {
        d = d || {};
        if (d.has_mailbox && d.address) {
          box.innerHTML = purchasedHTML(d) +
            '<p class="vp-portal-sub">Your private mailbox is ready 🎉</p>' +
            '<div class="vp-portal-plan"><div class="vp-portal-plan-label">Address</div>' +
            '<div class="vp-portal-plan-name">' + esc(d.address) + '</div></div>' +
            '<p class="vp-portal-foot">' + (d.quota_mb ? esc(String(d.quota_mb)) + ' MB storage' : 'Unlimited storage') + ' · PGP encryption + WKD · VayuTalk secure chat included.</p>' +
            '<a class="vp-portal-btn" href="/os/vayumail/inbox">Open VayuMail</a>' +
            '<p class="vp-portal-foot">On your phone, open the VayuMail app and sign in with this address to finish device setup — that also unlocks end-to-end encrypted VayuTalk chat.</p>';
          wireActivateForms();
          return;
        }
        if (!d.entitled) {
          box.innerHTML = '<p class="vp-portal-sub">Your current plan does not include a mailbox.</p>' +
            '<a class="vp-portal-btn" href="/pricing">See plans with a mailbox</a>';
          return;
        }
        var domain = d.domain || '';
        box.innerHTML = purchasedHTML(d) +
          '<p class="vp-portal-sub">Pick your address — you get a private mailbox with PGP encryption' +
          (d.quota_mb ? ', ' + esc(String(d.quota_mb)) + ' MB storage' : '') + ' and end-to-end encrypted VayuTalk chat, all included. Premium (vanity) names can be bought below.</p>' +
          '<form class="vp-portal-form" data-vp-mailbox-form novalidate>' +
          '<label class="vp-portal-label" for="vp-mb-local">Choose your address</label>' +
          '<div style="display:flex;gap:.4rem;align-items:center;flex-wrap:wrap">' +
          '<input class="vp-portal-input" id="vp-mb-local" type="text" autocomplete="off" spellcheck="false" placeholder="yourname" style="text-transform:lowercase;flex:1;min-width:8rem">' +
          '<span style="opacity:.7;font-family:monospace">@' + esc(domain) + '</span></div>' +
          '<div data-vp-mbavail style="font-size:.85rem;min-height:1.2em;margin:.25rem 0"></div>' +
          '<button type="button" class="vp-portal-btn" data-vp-mbbuy hidden style="margin:.1rem 0 .4rem"></button>' +
          '<label class="vp-portal-label" for="vp-mb-pass">Set a mailbox password</label>' +
          '<input class="vp-portal-input" id="vp-mb-pass" type="password" autocomplete="new-password" placeholder="At least 8 characters">' +
          (d.terms ?
            '<details style="margin:.55rem 0;border:1px solid rgba(128,128,128,.3);border-radius:.45rem;padding:.4rem .6rem">' +
            '<summary style="cursor:pointer;font-size:.85rem;opacity:.85">Mailbox terms — please read</summary>' +
            '<div style="max-height:9rem;overflow:auto;font-size:.8rem;opacity:.82;margin-top:.4rem;white-space:pre-wrap">' + esc(d.terms) + '</div></details>' +
            '<label style="display:flex;gap:.45rem;align-items:flex-start;font-size:.85rem;margin:.2rem 0 .5rem"><input type="checkbox" id="vp-mb-terms" style="margin-top:.2rem"><span>I have read and agree to the mailbox terms.</span></label>'
            : '') +
          '<button class="vp-portal-btn" type="submit">Claim my mailbox</button>' +
          '</form>' +
          '<div class="vp-portal-msg" aria-live="polite"></div>';
        wireMailboxForm(domain);
        wireActivateForms();
      })
      .catch(function () { box.innerHTML = '<div class="vp-portal-activity-empty">Could not load your mailbox.</div>'; });
  }

  // purchasedHTML renders the "activate a premium address you've bought" block for
  // any paid-but-unclaimed grants the member holds.
  function purchasedHTML(d) {
    if (!d || !d.purchased || !d.purchased.length) { return ''; }
    var items = d.purchased.map(function (p) {
      return '<div class="vp-portal-plan" style="margin-bottom:.6rem"><div class="vp-portal-plan-label">Purchased — activate</div>' +
        '<div class="vp-portal-plan-name">' + esc(p.address) + '</div>' +
        '<form class="vp-portal-form" data-vp-activate-form data-localpart="' + esc(p.localpart) + '" novalidate style="margin-top:.4rem">' +
        '<input class="vp-portal-input" type="password" data-vp-actpass autocomplete="new-password" placeholder="Set a password (min 8)">' +
        (d.terms ? '<label style="display:flex;gap:.4rem;align-items:flex-start;font-size:.8rem;margin:.3rem 0"><input type="checkbox" data-vp-actterms style="margin-top:.2rem"><span>I agree to the mailbox terms.</span></label>' : '') +
        '<button class="vp-portal-btn" type="submit">Activate ' + esc(p.localpart) + '</button></form></div>';
    }).join('');
    return '<div class="vp-portal-sub">You’ve purchased ' + d.purchased.length + ' premium address' + (d.purchased.length > 1 ? 'es' : '') + ' — set a password to activate:</div>' + items + '<div class="vp-portal-msg" aria-live="polite"></div>';
  }

  function wireActivateForms() {
    var forms = body.querySelectorAll('[data-vp-activate-form]');
    Array.prototype.forEach.call(forms, function (form) {
      form.addEventListener('submit', function (e) {
        e.preventDefault();
        var lp = form.getAttribute('data-localpart') || '';
        var pass = (form.querySelector('[data-vp-actpass]') || {}).value || '';
        var tb = form.querySelector('[data-vp-actterms]');
        var accepted = tb ? tb.checked : true;
        if (pass.length < 8) { msg('Password must be at least 8 characters.', 'err'); return; }
        if (tb && !accepted) { msg('Please accept the mailbox terms to continue.', 'err'); return; }
        var btn = form.querySelector('.vp-portal-btn');
        btn.disabled = true; btn.textContent = 'Activating…';
        postJSON('/api/v1/members/mailbox/premium/activate', { localpart: lp, password: pass, accept_terms: accepted }).then(function (res) {
          btn.disabled = false; btn.textContent = 'Activate ' + lp;
          if (res.ok) { loadMailbox(); }
          else { msg((res.body && res.body.error && res.body.error.message) || 'Could not activate that address.', 'err'); }
        });
      });
    });
  }

  function wireMailboxForm(domain) {
    var local = body.querySelector('#vp-mb-local');
    var avail = body.querySelector('[data-vp-mbavail]');
    var buy = body.querySelector('[data-vp-mbbuy]');
    var form = body.querySelector('[data-vp-mailbox-form]');
    var t = null, buyLocal = '';
    function check() {
      var v = (local.value || '').trim().toLowerCase();
      if (buy) { buy.hidden = true; }
      if (!v) { avail.textContent = ''; avail.style.color = ''; return; }
      fetch('/api/v1/members/mailbox/available?localpart=' + encodeURIComponent(v), { credentials: 'same-origin' })
        .then(function (r) { return r.json(); })
        .then(function (d) {
          if (d && d.available) { avail.textContent = '✓ ' + v + '@' + domain + ' is available'; avail.style.color = '#22c55e'; }
          else if (d && d.premium) {
            avail.textContent = '✦ ' + v + '@' + domain + ' is a premium address' + (d.price ? ' — ' + d.price : '') + ' (not on the free claim)'; avail.style.color = '#f59e0b';
            if (buy) { buyLocal = v; buy.textContent = 'Buy ' + v + '@' + domain + (d.price ? ' — ' + d.price : ''); buy.hidden = false; }
          }
          else { avail.textContent = '✕ ' + ((d && d.reason) || 'Not available'); avail.style.color = '#ef4444'; }
        }).catch(function () {});
    }
    if (local) { local.addEventListener('input', function () { clearTimeout(t); t = setTimeout(check, 300); }); }
    if (buy) {
      buy.addEventListener('click', function () {
        if (!buyLocal) { return; }
        buy.disabled = true; var lbl = buy.textContent; buy.textContent = 'Starting checkout…';
        postJSON('/api/v1/members/mailbox/premium/checkout', { localpart: buyLocal }).then(function (res) {
          buy.disabled = false; buy.textContent = lbl;
          if (res.ok && res.body && res.body.checkout_url) { window.location.href = res.body.checkout_url; return; }
          if (res.ok && res.body && res.body.reference) { msg('To buy ' + buyLocal + '@' + domain + ', pay ' + (res.body.amount || '') + ' quoting reference ' + res.body.reference + '. ' + (res.body.instructions || '') + ' Your address unlocks once payment is confirmed.', ''); return; }
          msg((res.body && res.body.error && res.body.error.message) || 'Could not start checkout.', 'err');
        });
      });
    }
    if (form) {
      form.addEventListener('submit', function (e) {
        e.preventDefault();
        var v = (local.value || '').trim().toLowerCase();
        var pass = (body.querySelector('#vp-mb-pass').value) || '';
        var termsBox = body.querySelector('#vp-mb-terms');
        var accepted = termsBox ? termsBox.checked : true;
        if (!v) { msg('Choose an address.', 'err'); return; }
        if (pass.length < 8) { msg('Password must be at least 8 characters.', 'err'); return; }
        if (termsBox && !accepted) { msg('Please accept the mailbox terms to continue.', 'err'); return; }
        var btn = form.querySelector('.vp-portal-btn');
        btn.disabled = true; btn.textContent = 'Claiming…';
        postJSON('/api/v1/members/mailbox/claim', { localpart: v, password: pass, accept_terms: accepted }).then(function (res) {
          btn.disabled = false; btn.textContent = 'Claim my mailbox';
          if (res.ok) { loadMailbox(); }
          else { msg((res.body && res.body.error && res.body.error.message) || 'Could not claim that address.', 'err'); }
        });
      });
    }
  }

  function render() {
    if (!body) { return; }
    var content;
    if (state.auth) { content = (view === 'activity') ? viewActivity() : (view === 'mailbox') ? viewMailbox() : (view === 'advertise') ? viewAdvertise() : viewAccount(); }
    else if (view === 'signin') { content = viewSignin(); }
    else if (view === 'vayumail') { content = viewVayuMail(false); }
    else { content = viewSignup(); }

    body.innerHTML = content;
    wire();
    var first = body.querySelector('input, a, button:not(.vp-portal-close)');
    if (first) { try { first.focus(); } catch (e) {} }
  }

  function msg(text, kind) {
    var box = body.querySelector('.vp-portal-msg');
    if (!box) { return; }
    box.className = 'vp-portal-msg vp-portal-notice vp-portal-notice--' + (kind || 'ok');
    box.textContent = text;
  }

  // ── Wiring ───────────────────────────────────────────────────────────────

  function wire() {
    body.querySelectorAll('[data-vp-go]').forEach(function (b) {
      b.addEventListener('click', function () { view = b.getAttribute('data-vp-go'); render(); });
    });

    var magic = body.querySelector('form[data-vp-form="magic"]');
    if (magic) {
      magic.addEventListener('submit', function (e) {
        e.preventDefault();
        var email = (magic.querySelector('[name=email]').value || '').trim();
        if (!email) { msg('Please enter your email address.', 'err'); return; }
        var btn = magic.querySelector('.vp-portal-btn');
        btn.disabled = true; btn.textContent = 'Sending your link...';
        postJSON('/api/v1/members/login', { email: email }).then(function (res) {
          if (res.ok) { msg('Check your inbox — we just emailed you a secure sign-in link. It is valid for 30 minutes.', 'ok'); magic.reset(); }
          else { msg('Something went wrong. Please try again.', 'err'); }
          btn.disabled = false; btn.textContent = view === 'signin' ? 'Email me a sign-in link' : 'Sign up free';
        });
      });
    }

    var vm = body.querySelector('form[data-vp-form="vayumail"]');
    if (vm) {
      vm.addEventListener('submit', function (e) {
        e.preventDefault();
        var email = (vm.querySelector('[name=email]').value || '').trim();
        var pass = vm.querySelector('[name=password]').value || '';
        var codeEl = vm.querySelector('[name=code]');
        var code = codeEl ? (codeEl.value || '').trim() : '';
        if (!email || !pass) { msg('Email and password are required.', 'err'); return; }
        var btn = vm.querySelector('.vp-portal-btn');
        btn.disabled = true; btn.textContent = 'Signing in...';
        postJSON('/api/v1/members/vayumail-login', { email: email, password: pass, code: code }).then(function (res) {
          btn.disabled = false; btn.textContent = 'Sign in';
          if (res.ok && res.body && res.body.authenticated) {
            state.auth = true; state.member = res.body.member || null; render();
            return;
          }
          var ec = res.body && res.body.error && res.body.error.code;
          if (ec === 'totp-required') {
            // Re-render with the code field, preserving what was typed.
            var keepEmail = email, keepPass = pass;
            body.innerHTML = viewVayuMail(true);
            wire();
            body.querySelector('[name=email]').value = keepEmail;
            body.querySelector('[name=password]').value = keepPass;
            var cf = body.querySelector('[name=code]'); if (cf) { cf.focus(); }
            msg('This account uses two-factor authentication — enter your 6-digit code.', 'ok');
            return;
          }
          var m = (res.body && res.body.error && res.body.error.message) || 'That email and password do not match.';
          msg(m, 'err');
        });
      });
    }

    if (state.auth && view === 'activity') { loadActivity(); }
    if (state.auth && view === 'mailbox') { loadMailbox(); }
    if (state.auth && view === 'advertise') { loadAdvertise(); }

    var out = body.querySelector('[data-vp-logout]');
    if (out) {
      out.addEventListener('click', function () {
        out.disabled = true;
        fetch('/members/logout', { method: 'POST', credentials: 'same-origin' })
          .then(function () { window.location.reload(); })
          .catch(function () { window.location.reload(); });
      });
    }
  }

  // ── Shell (button + overlay) ───────────────────────────────────────────────

  function open(initialView) {
    if (initialView && !state.auth) { view = initialView; }
    lastFocus = document.activeElement;
    render();
    overlay.classList.add('is-open');
    overlay.setAttribute('aria-hidden', 'false');
    document.documentElement.style.overflow = 'hidden';
  }

  function close() {
    overlay.classList.remove('is-open');
    overlay.setAttribute('aria-hidden', 'true');
    document.documentElement.style.overflow = '';
    if (lastFocus && lastFocus.focus) { try { lastFocus.focus(); } catch (e) {} }
  }

  function buildShell() {
    trigger = el('button', 'vp-portal-trigger', ICON_USER);
    trigger.type = 'button';
    trigger.setAttribute('aria-label', 'Open membership menu');
    if (state.auth && state.member) {
      var n = (state.member.name || '?').charAt(0).toUpperCase();
      trigger.classList.add('vp-portal-trigger--member');
      trigger.innerHTML = esc(n);
    }
    trigger.addEventListener('click', function () { open(); });
    document.body.appendChild(trigger);

    overlay = el('div', 'vp-portal-overlay');
    overlay.setAttribute('aria-hidden', 'true');
    panel = el('div', 'vp-portal-panel');
    panel.setAttribute('role', 'dialog');
    panel.setAttribute('aria-modal', 'true');
    panel.setAttribute('aria-label', 'Membership');
    // The close button lives in a STICKY header, not absolutely positioned in the
    // panel. The panel is the scroll container, so an absolutely positioned button
    // scrolled away with the content: past the first screenful the X was simply not
    // where it appeared to be, which is why closing worked only sometimes. Sticky
    // keeps it pinned to the top of whatever is visible.
    var head = el('div', 'vp-portal-head');
    // The brand sits in the header rather than at the top of the body. In the body
    // it was a row that scrolled under the pinned header and clipped; here it gives
    // the bar an identity and stays put, which is what an app bar is for.
    var brand = el('div', 'vp-portal-brand');
    brand.innerHTML = '<img src="/static/favicon-light.png" alt="" width="24" height="24"><span>' +
      esc(brandName()) + '</span>';
    head.appendChild(brand);
    var closeBtn = el('button', 'vp-portal-close', ICON_CLOSE);
    closeBtn.type = 'button';
    closeBtn.setAttribute('aria-label', 'Close');
    // Bound on pointerup as well as click: on touch, a slight finger movement
    // between touchstart and touchend can cancel the synthetic click, so a tap that
    // visibly landed on the X did nothing. Guard against handling both.
    var closing = false;
    function requestClose(e) {
      if (closing) { return; }
      closing = true;
      if (e && e.preventDefault) { e.preventDefault(); }
      close();
      setTimeout(function () { closing = false; }, 350);
    }
    closeBtn.addEventListener('click', requestClose);
    closeBtn.addEventListener('pointerup', requestClose);
    head.appendChild(closeBtn);
    body = el('div', 'vp-portal-body');
    panel.appendChild(head);
    panel.appendChild(body);
    overlay.appendChild(panel);
    overlay.addEventListener('click', function (e) { if (e.target === overlay) { close(); } });
    document.addEventListener('keydown', function (e) { if (e.key === 'Escape' && overlay.classList.contains('is-open')) { close(); } });
    document.body.appendChild(overlay);

    var si = document.querySelector('.vayu-nav-signin');
    var su = document.querySelector('.vayu-nav-signup');
    if (state.auth && state.member) {
      // Signed in: drop the "Sign up" link and turn "Sign in" into the account
      // chip. A reader opens the account panel; a VayuOS operator (console_url set)
      // links straight to their dashboard, so the owner is recognised on their own
      // public site instead of being shown "Sign in / Sign up".
      if (su && su.parentNode) { su.parentNode.removeChild(su); }
      if (si) {
        si.classList.add('vayu-nav-member');
        var nm = state.member.name || 'Account';
        if (state.member.avatar) {
          si.textContent = '';
          var img = document.createElement('img');
          img.className = 'vayu-nav-avatar';
          img.src = state.member.avatar;
          img.alt = '';
          img.width = 22; img.height = 22;
          si.appendChild(img);
          si.appendChild(document.createTextNode(' ' + nm));
        } else {
          si.textContent = '👤 ' + nm;
        }
        if (state.member.console_url) {
          // Operator/staff: let the chip navigate to the console dashboard.
          si.classList.add('vayu-nav-operator');
          si.setAttribute('href', state.member.console_url);
        } else {
          si.setAttribute('href', '/members/account');
          si.addEventListener('click', function (e) { e.preventDefault(); open('account'); });
        }
      }
    } else {
      // Logged out: upgrade the nav Sign in / Sign up links to open the panel.
      if (si) { si.addEventListener('click', function (e) { e.preventDefault(); open('signin'); }); }
      if (su) { su.addEventListener('click', function (e) { e.preventDefault(); open('signup'); }); }
    }
  }

  function ensureCSS() {
    if (document.querySelector('link[data-vp-portal]')) { return; }
    var l = document.createElement('link');
    l.rel = 'stylesheet';
    l.href = '__PORTAL_CSS_HREF__';
    l.setAttribute('data-vp-portal', '');
    document.head.appendChild(l);
  }

  function init() {
    fetch('/api/v1/members/me', { credentials: 'same-origin', headers: { 'Accept': 'application/json' } })
      .then(function (r) { return r.ok ? r.json() : null; })
      .then(function (d) {
        if (!d || !d.enabled) { return; }
        state.enabled = true;
        state.vayumail = !!d.vayumail_enabled;
        state.auth = !!d.authenticated;
        state.member = d.member || null;
        ensureCSS();
        buildShell();
        // Expose a programmatic opener so other public widgets (e.g. the
        // comment box) can prompt sign-in through the same portal.
        window.vpPortalOpen = function (v) { open(v); };
      })
      .catch(function () {});
  }

  if (document.readyState !== 'loading') { init(); }
  else { document.addEventListener('DOMContentLoaded', init); }
})();`

// portalCSSToken is the placeholder the served script carries in place of the
// stylesheet URL, so the URL can be content-versioned at boot.
const portalCSSToken = "__PORTAL_CSS_HREF__"

// portal.css is served with a one-year immutable cache lifetime, which is only
// safe if its URL changes whenever its bytes do. The widget injects the
// stylesheet from script rather than from page HTML, so the version has to reach
// it through the script — hence the placeholder above, resolved here.
var (
	portalAssetMu  sync.RWMutex
	portalJSBody   = strings.Replace(PortalJS, portalCSSToken, "/static/css/portal.css", 1)
	portalJSVerStr = shortSum(portalJSBody)
)

// SetPortalCSSVersion records the content version of the on-disk portal.css and
// rebuilds the served script around it. Hashing the finished body means a
// stylesheet-only change also moves the script URL, so a client holding a cached
// script still learns about the new stylesheet.
func SetPortalCSSVersion(ver string) {
	href := "/static/css/portal.css"
	if ver != "" {
		href += "?v=" + ver
	}
	body := strings.Replace(PortalJS, portalCSSToken, href, 1)
	portalAssetMu.Lock()
	portalJSBody, portalJSVerStr = body, shortSum(body)
	portalAssetMu.Unlock()
}

// initPortalCSSVersion hashes portal.css from staticDir. It runs from Init,
// after syncEmbeddedStatic has refreshed the file on disk.
func initPortalCSSVersion(staticDir string) {
	if staticDir == "" {
		return
	}
	b, err := os.ReadFile(filepath.Join(staticDir, "css", "portal.css"))
	if err != nil {
		return
	}
	SetPortalCSSVersion(shortSum(string(b)))
}

func shortSum(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:8])
}

// PortalJSBody returns the widget script exactly as it should be served.
func PortalJSBody() string {
	portalAssetMu.RLock()
	defer portalAssetMu.RUnlock()
	return portalJSBody
}

// PortalJSVersion returns the cache-busting version of the served script body.
func PortalJSVersion() string {
	portalAssetMu.RLock()
	defer portalAssetMu.RUnlock()
	return portalJSVerStr
}

// membershipEnabled mirrors the install-wide membership setting so the renderer
// can decide, at render time, whether the portal widget is worth shipping. It is
// a single setting for the whole install (not per-domain), so one flag is
// correct here; the app refreshes it at boot and whenever the setting changes.
var membershipEnabled atomic.Bool

// SetMembershipEnabled updates the renderer's view of the membership setting.
func SetMembershipEnabled(on bool) { membershipEnabled.Store(on) }

// PortalJSLink returns the deferred <script> tag for the VayuPortal widget,
// versioned so neither a new build nor a restyle can be masked by a cached copy.
//
// It returns NOTHING when membership is switched off.
//
// The widget used to ship on every public page unconditionally, then discover at
// runtime that it had no work: its init() fetches /api/v1/members/me and returns
// immediately unless the response says membership is enabled. So an install with
// membership off made every visitor download ~10 KiB of JavaScript and pay an
// extra request, to reach the answer the SERVER ALREADY HAD when it rendered the
// page. Deciding on the client something the server knows is a round trip and a
// payload spent to learn nothing.
func PortalJSLink() template.HTML {
	if !membershipEnabled.Load() {
		return ""
	}
	return template.HTML(`<script src="/static/js/portal.js?v=` + PortalJSVersion() + `" defer></script>`)
}
