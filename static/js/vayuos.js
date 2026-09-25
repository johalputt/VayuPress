/* VayuOS · Still Air shell behaviour.
   Loaded only by the Still Air shell. Everything the classic shell also does
   (drawer, command bar, bell, PWA install, world switch) stays in admin-os.js
   and reaches this shell through the same hooks; this file adds only what the
   new shell introduces: popover menus and the colour-scheme control. The design
   switch lives in admin-os.js, because the classic console offers it too. No inline handlers, nothing evaluated: CSP script-src 'self'. */
(function () {
  'use strict';

  function csrf() {
    var m = document.cookie.match(/(?:^|;\s*)vp_csrf=([^;]+)/);
    return m ? decodeURIComponent(m[1]) : '';
  }
  function saveSetting(key, value) {
    return fetch('/os/api/settings', {
      method: 'POST',
      credentials: 'same-origin',
      headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': csrf() },
      body: JSON.stringify({ key: key, value: value })
    }).then(function (r) { if (!r.ok) throw new Error('HTTP ' + r.status); });
  }
  function say(msg, kind) { if (window.vpToast) window.vpToast(msg, kind); }

  /* ── Popovers (<details class="sa-pop">) ──────────────────────────────
     A details element opens and closes itself; what it lacks is closing when
     the pointer or focus goes elsewhere, Escape, and one-at-a-time. */
  var pops = Array.prototype.slice.call(document.querySelectorAll('details.sa-pop'));
  function closeAll(except) {
    pops.forEach(function (d) { if (d !== except) d.open = false; });
  }
  function items(d) {
    return Array.prototype.slice.call(d.querySelectorAll('[role="menuitem"]:not([hidden]), .sa-seg__opt'));
  }
  pops.forEach(function (d) {
    d.addEventListener('toggle', function () {
      if (!d.open) return;
      closeAll(d);
      // Opened from the keyboard: move into the menu so arrows work at once.
      if (d.dataset.kbd === '1') {
        var first = items(d)[0];
        if (first) first.focus();
      }
      d.dataset.kbd = '';
    });
    var sum = d.querySelector('summary');
    if (sum) {
      sum.addEventListener('keydown', function (e) {
        if (e.key === 'Enter' || e.key === ' ' || e.key === 'ArrowDown') {
          d.dataset.kbd = '1';
          if (e.key === 'ArrowDown') { e.preventDefault(); d.open = true; }
        }
      });
    }
    d.addEventListener('keydown', function (e) {
      if (e.key === 'Escape' && d.open) {
        e.preventDefault();
        d.open = false;
        if (sum) sum.focus();
        return;
      }
      if (e.key !== 'ArrowDown' && e.key !== 'ArrowUp') return;
      var list = items(d);
      if (!list.length || !d.open) return;
      var i = list.indexOf(document.activeElement);
      if (i < 0 && document.activeElement === sum) return;
      e.preventDefault();
      i = e.key === 'ArrowDown' ? (i + 1) % list.length : (i - 1 + list.length) % list.length;
      list[i].focus();
    });
  });
  document.addEventListener('click', function (e) {
    var inside = e.target.closest ? e.target.closest('details.sa-pop') : null;
    closeAll(inside);
  });
  document.addEventListener('focusin', function (e) {
    var inside = e.target.closest ? e.target.closest('details.sa-pop') : null;
    closeAll(inside);
  });

  /* ── Colour scheme ─────────────────────────────────────────────────────
     Applied at once, saved for every device, mirrored for the sign-in pages
     (os-theme.js reads the same key). A failed save is said out loud: the page
     already shows the new scheme, so silence would read as saved. */
  var seg = Array.prototype.slice.call(document.querySelectorAll('[data-sa-theme]'));
  seg.forEach(function (b) {
    b.addEventListener('click', function () {
      var next = b.getAttribute('data-sa-theme');
      document.body.dataset.theme = next;
      seg.forEach(function (o) { o.setAttribute('aria-pressed', o === b ? 'true' : 'false'); });
      try { localStorage.setItem('vp-os-theme', next); } catch (e) { /* private mode: page-only */ }
      saveSetting('admin.theme', next).catch(function () {
        say('Colour scheme changed for this page only — it could not be saved.', 'warn');
      });
    });
  });


  /* ── Search settings ───────────────────────────────────────────────────
     The index is a list of links the server drew (settings_app.go); this
     only filters it. Every word must appear, so "footer legal" narrows
     rather than widens. While a query stands the categories step aside. */
  var find = document.querySelector('[data-settings-search]');
  if (find) {
    var side = find.closest('.sa-appside');
    var index = document.querySelector('[data-settings-index]');
    var none = document.querySelector('[data-settings-none]');
    var items = Array.prototype.slice.call(index.querySelectorAll('.sa-find__item'));
    var shown = [];
    var active = -1;
    var mark = function (i) {
      shown.forEach(function (a, j) { a.classList.toggle('is-active', j === i); });
      active = i;
    };
    var filter = function () {
      var words = find.value.toLowerCase().split(/\s+/).filter(Boolean);
      side.classList.toggle('is-searching', words.length > 0);
      index.hidden = !words.length;
      shown = [];
      items.forEach(function (a) {
        var terms = a.getAttribute('data-terms');
        var hit = words.length > 0 && words.every(function (w) { return terms.indexOf(w) >= 0; });
        a.parentNode.hidden = !hit;
        if (hit) shown.push(a);
      });
      none.hidden = !words.length || shown.length > 0;
      mark(shown.length ? 0 : -1);
    };
    find.addEventListener('input', filter);
    find.addEventListener('keydown', function (e) {
      if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
        if (!shown.length) return;
        e.preventDefault();
        mark((active + (e.key === 'ArrowDown' ? 1 : shown.length - 1)) % shown.length);
      } else if (e.key === 'Enter' && active >= 0) {
        e.preventDefault();
        shown[active].click();
      } else if (e.key === 'Escape' && find.value) {
        e.preventDefault();
        find.value = '';
        filter();
      }
    });
  }


  /* ── Tips (ui.Tip) ──────────────────────────────────────────────────────
     A tip opens on hover or focus by CSS alone; Escape puts it away. */
  document.addEventListener('keydown', function (e) {
    if (e.key === 'Escape' && document.activeElement && document.activeElement.classList.contains('sa-tip')) {
      document.activeElement.blur();
    }
  });


  /* ── HTMX writes: say what happened ──────────────────────────────────────
     A write that swaps a fragment in place changes the page and says
     nothing; a refused one changes nothing and says nothing either. The
     page's status region announces the first to a screen reader, and a toast
     names the second, so no write fails silently. */
  document.addEventListener('htmx:afterRequest', function (e) {
    var d = e.detail || {};
    var verb = d.requestConfig && d.requestConfig.verb;
    if (!verb || verb === 'get') return;
    var live = document.querySelector('.page-actions [role="status"]');
    if (d.successful) {
      if (live) live.textContent = 'Done.';
      return;
    }
    var msg = 'That did not go through' + (d.xhr && d.xhr.status ? ' (' + d.xhr.status + ')' : '') + '.';
    if (live) live.textContent = msg;
    say(msg, 'error');
  });


  /* ── Control states: loading, then success or error ─────────────────────
     A write shows, on the control that started it, that it is working and
     then how it went (plan §7). The toast says it once, elsewhere, and goes;
     the control is where the operator is looking. One mechanism for every
     write, HTMX or fetch, so no page wires its own and none can forget.
     A fetch is credited to the button whose click handler started it: the
     click is noted, and forgotten once that task ends, so a fetch from a
     timer or a later callback is never pinned on a stale button. */
  var origin = null;
  document.addEventListener('click', function (e) {
    var b = e.target.closest ? e.target.closest('button, .btn') : null;
    if (!b) return;
    origin = b;
    setTimeout(function () { if (origin === b) origin = null; }, 0);
  }, true);

  function begin(el) {
    el.removeAttribute('data-state');
    el.setAttribute('aria-busy', 'true');
  }
  function settle(el, ok) {
    el.removeAttribute('aria-busy');
    var state = ok ? 'success' : 'error';
    el.setAttribute('data-state', state);
    setTimeout(function () {
      if (el.getAttribute('data-state') === state) el.removeAttribute('data-state');
    }, ok ? 1600 : 2600);
  }
  function isWrite(method) { return !/^(GET|HEAD|OPTIONS)$/i.test(method || 'GET'); }

  var nativeFetch = window.fetch;
  if (nativeFetch) {
    window.fetch = function (input, init) {
      var p = nativeFetch.apply(this, arguments);
      var method = (init && init.method) || (input && input.method);
      var el = origin;
      if (!el || !isWrite(method)) return p;
      begin(el);
      p.then(function (r) { settle(el, r.ok); }, function () { settle(el, false); });
      return p;
    };
  }

  var htmxOrigin = new WeakMap();
  document.addEventListener('htmx:beforeRequest', function (e) {
    var d = e.detail || {};
    if (!d.requestConfig || !isWrite(d.requestConfig.verb)) return;
    var el = d.elt;
    if (el && el.tagName === 'FORM') {
      var ev = d.requestConfig.triggeringEvent;
      el = (ev && ev.submitter) || el.querySelector('[type="submit"], button:not([type])');
    }
    if (!el || !el.matches('button, .btn, input, select, textarea')) return;
    htmxOrigin.set(d.xhr, el);
    begin(el);
  });
  document.addEventListener('htmx:afterRequest', function (e) {
    var d = e.detail || {};
    var el = d.xhr && htmxOrigin.get(d.xhr);
    if (el) settle(el, d.successful);
  });

})();
