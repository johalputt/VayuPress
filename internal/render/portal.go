package render

import (
	"crypto/sha256"
	"encoding/hex"
	"html/template"
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
      '<p class="vp-portal-foot"><button type="button" class="vp-portal-link" data-vp-go="signin">Use a sign-in link instead</button></p>' +
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
        : '<a class="vp-portal-btn" href="/os/vayuos/mail/inbox">Open VayuMail</a>';
    }
    return '<div class="vp-portal-account-id">' +
      '<div class="vp-portal-avatar">' + esc(initial) + '</div>' +
      '<div><div class="vp-portal-acc-name">' + esc(name) + '</div>' +
      '<div class="vp-portal-acc-mail">' + esc(m.email || '') + '</div></div></div>' +
      '<div class="vp-portal-plan"><div class="vp-portal-plan-label">Your plan</div>' +
      '<div class="vp-portal-plan-name">' + esc(plan) + '</div></div>' +
      '<div class="vp-portal-actions">' +
      mailBtn +
      '<button type="button" class="vp-portal-btn vp-portal-btn--ghost" data-vp-go="activity">💬 Your comments</button>' +
      '<a class="vp-portal-btn vp-portal-btn--ghost" href="/members/account">Manage account</a>' +
      (m.paid ? '' : '<a class="vp-portal-btn" href="/pricing">See membership plans</a>') +
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

  function render() {
    if (!body) { return; }
    var content;
    if (state.auth) { content = (view === 'activity') ? viewActivity() : viewAccount(); }
    else if (view === 'signin') { content = viewSignin(); }
    else if (view === 'vayumail') { content = viewVayuMail(false); }
    else { content = viewSignup(); }

    body.innerHTML = '<div class="vp-portal-brand"><img src="/static/favicon-light.png" alt="" width="32" height="32"><span>' +
      esc(brandName()) + '</span></div>' + content;
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
            body.querySelector('.vp-portal-brand');
            var keepEmail = email, keepPass = pass;
            body.innerHTML = '<div class="vp-portal-brand"><img src="/static/favicon-light.png" alt="" width="32" height="32"><span>' +
              esc(brandName()) + '</span></div>' + viewVayuMail(true);
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
    var closeBtn = el('button', 'vp-portal-close', '&times;');
    closeBtn.type = 'button';
    closeBtn.setAttribute('aria-label', 'Close');
    closeBtn.addEventListener('click', close);
    body = el('div', 'vp-portal-body');
    panel.appendChild(closeBtn);
    panel.appendChild(body);
    overlay.appendChild(panel);
    overlay.addEventListener('click', function (e) { if (e.target === overlay) { close(); } });
    document.addEventListener('keydown', function (e) { if (e.key === 'Escape' && overlay.classList.contains('is-open')) { close(); } });
    document.body.appendChild(overlay);

    var si = document.querySelector('.vayu-nav-signin');
    var su = document.querySelector('.vayu-nav-signup');
    if (state.auth && state.member) {
      // Signed in: drop the "Sign up" link and turn "Sign in" into the member's
      // name, which opens the account panel (with logout) instead of navigating.
      if (su && su.parentNode) { su.parentNode.removeChild(su); }
      if (si) {
        si.classList.add('vayu-nav-member');
        si.textContent = '👤 ' + (state.member.name || 'Account');
        si.setAttribute('href', '/members/account');
        si.addEventListener('click', function (e) { e.preventDefault(); open('account'); });
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
    l.href = '/static/css/portal.css';
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

// portalJSHash versions the widget URL for cache-busting.
var portalJSHash = func() string {
	sum := sha256.Sum256([]byte(PortalJS))
	return hex.EncodeToString(sum[:8])
}()

// PortalJSLink returns the deferred <script> tag for the VayuPortal widget,
// versioned so a new build invalidates any cached copy.
func PortalJSLink() template.HTML {
	return template.HTML(`<script src="/static/js/portal.js?v=` + portalJSHash + `" defer></script>`)
}
