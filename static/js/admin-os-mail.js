/* admin-os-mail.js — VayuMail panel interactions (compose, accounts, message
 * actions). CSRF: reads the vp_csrf cookie and sends it as X-CSRF-Token, matching
 * the double-submit middleware. No inline handlers (CSP-safe). */
(function () {
  'use strict';

  // Post-action navigation targets are built inline from encodeURIComponent-
  // escaped components with a literal path prefix (see the actions block below),
  // so there is no full-URL-from-DOM navigation helper — the previous
  // localPath/safeNav guards are no longer needed.

  // Split reading pane: highlight the row whose message is open in the pane.
  // Delegated on document so it keeps working after the list is swapped by HTMX.
  // Opening a message leaves the list where it is (Mail plan §8, item 5): the
  // row turns selected in place and its unread mark goes, and nothing else in
  // the list moves. A refresh of the list (a reader action, the poll) marks the
  // open message's row again.
  var openHref = '';
  function markOpen(row) {
    document.querySelectorAll('[data-vm-row].vm-active').forEach(function (r) { r.classList.remove('vm-active'); });
    if (row) { row.classList.add('vm-active'); row.classList.remove('is-unread'); }
  }
  document.addEventListener('click', function (e) {
    var link = e.target && e.target.closest ? e.target.closest('[data-vm-open]') : null;
    if (!link) return;
    openHref = link.getAttribute('href') || '';
    markOpen(link.closest('[data-vm-row]'));
  });
  // The newest message is open when Mail opens (Mail plan, decision 3), but
  // it is marked read only once it has been on screen for two seconds, the way
  // Apple Mail's delay works: glancing past is not reading. Its row is the
  // selected one meanwhile, and keeps its unread mark until then.
  var peekTimer = null;
  function csrfToken() { var m = document.cookie.match(/(?:^|; )vp_csrf=([^;]*)/); return m ? decodeURIComponent(m[1]) : ''; }
  function settle() {
    var pane = document.getElementById('vm-readpane');
    var reader = pane && pane.querySelector('[data-mx-reader]');
    if (peekTimer) { clearTimeout(peekTimer); peekTimer = null; }
    if (!reader) return;
    var href = reader.getAttribute('data-mx-href') || '';
    if (href && !openHref) {
      openHref = href;
      var link = document.querySelector('#vm-inbox-list [data-vm-open][href="' + CSS.escape(href) + '"]');
      if (link) { document.querySelectorAll('[data-vm-row].vm-active').forEach(function (r) { r.classList.remove('vm-active'); }); link.closest('[data-vm-row]').classList.add('vm-active'); }
    }
    var peek = reader.getAttribute('data-mx-peek');
    // Only a message on screen is being read: narrower than three columns
    // the list comes first and the newest message waits, unread, behind it.
    if (!peek || !reader.getClientRects().length) return;
    peekTimer = setTimeout(function () {
      // Still on screen when the time is up: the layout may have put the list
      // over it since (Reading set to full width is settled after this runs).
      if (!document.body.contains(reader) || !reader.getClientRects().length) return;
      var body = new URLSearchParams({ action: 'mark', mark: 'read', id: peek, user: reader.getAttribute('data-mx-user') || '', folder: reader.getAttribute('data-mx-folder') || '' });
      fetch('/os/vayumail/inbox/action', { method: 'POST', credentials: 'same-origin', headers: { 'X-CSRF-Token': csrfToken(), 'Content-Type': 'application/x-www-form-urlencoded' }, body: body })
        .then(function (r) {
          if (!r.ok) return;
          var link = document.querySelector('#vm-inbox-list [data-vm-open][href="' + CSS.escape(openHref) + '"]');
          if (link) link.closest('[data-vm-row]').classList.remove('is-unread');
        });
    }, 2000);
  }
  // The reader's words arrive from the direction of travel (Mail plan §8,
  // item 5): the older message comes up from below, the newer down from above.
  var travel = '';
  document.addEventListener('click', function (e) {
    var t = e.target && e.target.closest ? e.target.closest('[data-mx-travel]') : null;
    if (t) travel = t.getAttribute('data-mx-travel');
  }, true);
  document.body.addEventListener('htmx:afterSwap', function (e) {
    if (!e.target || e.target.id !== 'vm-readpane') return;
    var reader = e.target.querySelector('[data-mx-reader]');
    if (reader && travel) reader.setAttribute('data-mx-from', travel);
    travel = '';
    settle();
  });
  settle();

  // The mailbox switcher (Mail plan §4). Its search is focused as it opens,
  // so typing filters at once; the shell's popover code gives it the arrow
  // keys, Escape and the motion. Enter in the search opens the first match.
  var sw = document.querySelector('[data-mx-switch]');
  if (sw) {
    var find = sw.querySelector('[data-mx-switch-find]');
    var filter = function () {
      var q = find.value.trim().toLowerCase();
      var any = false;
      sw.querySelectorAll('.mx-switch__group').forEach(function (g) {
        var shown = 0;
        g.querySelectorAll('.mx-switch__row').forEach(function (r) {
          var hit = !q || (r.getAttribute('data-mx-find') || '').indexOf(q) >= 0;
          r.hidden = !hit;
          if (hit) shown++;
        });
        g.hidden = !shown;
        if (shown) any = true;
      });
      var none = sw.querySelector('.mx-switch__none');
      if (none) none.hidden = any || !sw.querySelector('.mx-switch__row');
    };
    sw.addEventListener('toggle', function () { if (sw.open) find.focus({ preventScroll: true }); });
    find.addEventListener('input', filter);
    // The list arrives after the first keys may have been typed.
    sw.addEventListener('htmx:afterSwap', filter);
    find.addEventListener('keydown', function (e) {
      if (e.key !== 'Enter') return;
      e.preventDefault();
      var first = sw.querySelector('.mx-switch__row:not([hidden])');
      if (first) first.click();
    });
    // Cmd+Shift+M (Ctrl on other systems) opens it from anywhere in Mail.
    document.addEventListener('keydown', function (e) {
      if (!(e.metaKey || e.ctrlKey) || !e.shiftKey || e.altKey || (e.key !== 'm' && e.key !== 'M')) return;
      e.preventDefault();
      sw.open = true;
    });
  }

  // Search in place (Mail plan §8, item 8). Focus lifts the field into the
  // header as the title folds away and brings the scope bar down; results
  // take the list's place as they arrive; Escape plays it back and puts the
  // list where it was. Delegated, because every list swap brings a new form.
  var searchScroll = 0;
  function searchList() { return document.getElementById('vm-inbox-list'); }
  document.addEventListener('focusin', function (e) {
    var input = e.target && e.target.closest ? e.target.closest('[data-mx-search] input[name="q"]') : null;
    var l = searchList();
    if (!input || !l || l.classList.contains('is-searching')) return;
    searchScroll = l.scrollTop;
    l.classList.add('is-searching');
  });
  function endSearch() {
    var l = searchList();
    if (!l || !l.classList.contains('is-searching')) return;
    var form = l.querySelector('[data-mx-search]');
    var input = form && form.querySelector('input[name="q"]');
    if (input) { input.value = ''; input.blur(); }
    var res = document.getElementById('vm-search-results');
    if (res) res.textContent = '';
    l.classList.remove('is-searching', 'has-results');
    l.scrollTop = searchScroll;
  }
  document.addEventListener('keydown', function (e) {
    if (e.key !== 'Escape' || !e.target || !e.target.closest || !e.target.closest('[data-mx-search]')) return;
    e.preventDefault();
    endSearch();
  });
  document.addEventListener('focusout', function (e) {
    var form = e.target && e.target.closest ? e.target.closest('[data-mx-search]') : null;
    if (!form) return;
    // Leaving an empty search lets go of it; leaving one with words keeps its
    // results, which is what the next click is usually for.
    setTimeout(function () {
      if (form.contains(document.activeElement)) return;
      var input = form.querySelector('input[name="q"]');
      if (input && !input.value.trim()) endSearch();
    }, 0);
  });
  document.body.addEventListener('htmx:afterSwap', function (e) {
    if (!e.target || e.target.id !== 'vm-search-results') return;
    var l = searchList();
    if (l) { l.classList.toggle('has-results', !!e.target.firstChild); l.scrollTop = 0; }
  });

  // An attachment's download fills a ring on its tile, then settles to a tick
  // (Mail plan §8, item 7). It is fetched to know how far it has come, then
  // saved under its own name; anything that goes wrong falls back to the plain
  // download the link already is.
  document.addEventListener('click', function (e) {
    var card = e.target && e.target.closest ? e.target.closest('a[data-mx-file]') : null;
    if (!card || e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return;
    if (!window.ReadableStream || card.classList.contains('is-loading')) { if (card && card.classList.contains('is-loading')) e.preventDefault(); return; }
    e.preventDefault();
    var arc = card.querySelector('.mx-file__ring circle');
    var name = card.querySelector('.mx-file__name');
    card.classList.remove('is-done');
    card.classList.add('is-loading');
    var set = function (part) { if (arc) arc.style.strokeDashoffset = String(100 - Math.round(part * 100)); };
    set(0);
    var fallBack = function () { card.classList.remove('is-loading'); window.location.href = card.href; };
    fetch(card.href, { credentials: 'same-origin' }).then(function (res) {
      if (!res.ok || !res.body) { fallBack(); return; }
      var total = parseInt(res.headers.get('Content-Length') || '0', 10);
      var got = 0, parts = [], reader = res.body.getReader();
      var pump = function () {
        return reader.read().then(function (step) {
          if (step.done) return;
          parts.push(step.value);
          got += step.value.length;
          if (total > 0) set(Math.min(got / total, 1));
          return pump();
        });
      };
      return pump().then(function () {
        set(1);
        var url = URL.createObjectURL(new Blob(parts, { type: res.headers.get('Content-Type') || 'application/octet-stream' }));
        var a = document.createElement('a');
        a.href = url;
        a.download = name ? name.textContent : '';
        document.body.appendChild(a);
        a.click();
        a.remove();
        setTimeout(function () { URL.revokeObjectURL(url); }, 4000);
        card.classList.remove('is-loading');
        card.classList.add('is-done');
      });
    }).catch(fallBack);
  });

  // ── Screens (Mail plan §7, render 05; pipeline 3y) ────────────────────────
  // The list comes first and a message is pushed over it; on a phone the
  // mailboxes are a screen above the list. <body data-mx-screen> says which
  // is showing; data-mx-move and data-mx-from let the stylesheet play the
  // push or the pop between them (§8, item 6). Opening a message is a history
  // entry, so a phone's own back gesture comes back to the list.
  // Messages are screens (<body data-mx-screens>) narrower than three
  // columns, for a person who reads full width (Mail's Reading), and while
  // one message is widened (Full width, w): one mechanism for all three.
  (function () {
    var split = document.querySelector('.vm-split');
    if (!split) return;
    var narrow = window.matchMedia('(max-width: 1099px)');
    var body = document.body, pushed = false, moveTimer = null;
    var full = split.getAttribute('data-mx-layout') === 'full', wide = false;
    function screensOn() { return narrow.matches || full || wide; }
    function sync() { body.toggleAttribute('data-mx-screens', screensOn()); }
    sync();
    if (narrow.addEventListener) narrow.addEventListener('change', sync);
    var list = document.getElementById('vm-inbox-list');
    function at() { return body.getAttribute('data-mx-screen'); }
    function move(to, dir) {
      var from = at();
      if (from === to) return;
      body.setAttribute('data-mx-from', from);
      body.setAttribute('data-mx-move', dir);
      body.setAttribute('data-mx-screen', to);
      clearTimeout(moveTimer);
      moveTimer = setTimeout(function () { body.removeAttribute('data-mx-move'); body.removeAttribute('data-mx-from'); }, 280);
      if (to === 'list' && from === 'message') {
        // Back on the list where it was, the row just read tinted a moment.
        var row = document.querySelector('#vm-inbox-list .mx-row.vm-active');
        if (row) { row.classList.add('is-just-read'); setTimeout(function () { row.classList.remove('is-just-read'); }, 900); }
      }
    }
    body.setAttribute('data-mx-screen', 'list');
    // Back from a widened message (always through history: widen pushes an
    // entry) is back to the columns, the message beside the list as it was
    // before, with no screen to pop.
    function settle() {
      if (!wide) return false;
      wide = false;
      body.setAttribute('data-mx-screen', 'list');
      sync();
      var w = document.querySelector('#vm-readpane [data-mx-widen]');
      if (w && document.activeElement && document.activeElement.closest && document.activeElement.closest('.mx-rtools')) w.focus();
      return true;
    }
    function toList() {
      if (at() !== 'message') return;
      if (pushed) { history.back(); return; }
      move('list', 'pop');
    }
    function widen() {
      if (screensOn() || !document.querySelector('#vm-readpane .mx-reader')) return;
      wide = true;
      sync();
      move('message', 'push');
      history.pushState({ mx: 'message' }, '');
      pushed = true;
      var back = document.querySelector('#vm-readpane .mx-back');
      if (back) back.focus();
    }
    // For the keys (below): w widens, and w or Escape comes back.
    window.vmScreens = {
      widen: widen, toList: toList,
      open: function () { return screensOn() && at() === 'message'; },
      wide: function () { return wide; }
    };
    // In the capture phase, so it runs before htmx's own listener: the entry
    // under a message is often one htmx pushed for a folder, and htmx would
    // restore it by fetching the page again, throwing away the list's scroll.
    // Popping a message is this page's own move, so htmx is not asked.
    window.addEventListener('popstate', function (e) {
      if (e.state && e.state.mx === 'message') { if (screensOn()) { pushed = true; move('message', 'push'); } return; }
      pushed = false;
      if (at() !== 'message') return;
      e.stopImmediatePropagation();
      if (!settle()) move('list', 'pop');
    }, true);
    document.body.addEventListener('htmx:afterSwap', function (e) {
      if (!e.target || e.target.id !== 'vm-readpane' || !screensOn()) return;
      if (e.target.querySelector('.vm-reader')) {
        if (at() !== 'message') { move('message', 'push'); history.pushState({ mx: 'message' }, ''); pushed = true; }
      } else {
        toList(); // an action took the message out of view
      }
    });
    document.addEventListener('click', function (e) {
      var t = e.target && e.target.closest ? e.target : null;
      if (!t) return;
      if (t.closest('[data-mx-widen]')) { widen(); return; }
      var choice = t.closest('[data-mx-layout-choice]');
      if (choice) {
        // Reading: kept on the person's account, applied at once.
        full = choice.getAttribute('data-mx-layout-choice') === 'full';
        document.querySelectorAll('[data-mx-layout-choice]').forEach(function (c) { c.setAttribute('aria-checked', String(c === choice)); });
        var pop = choice.closest('details');
        if (pop) pop.open = false;
        if (!screensOn()) body.setAttribute('data-mx-screen', 'list');
        sync();
        fetch('/os/vayumail/layout', { method: 'POST', credentials: 'same-origin', headers: { 'X-CSRF-Token': csrfToken(), 'Content-Type': 'application/x-www-form-urlencoded' }, body: new URLSearchParams({ layout: full ? 'full' : '' }) })
          .then(function (r) { if (!r.ok && window.vpToast) window.vpToast('Reading could not be saved.', 'error'); });
        return;
      }
      var go = t.closest('[data-mx-go]');
      if (go) {
        e.preventDefault();
        if (go.getAttribute('data-mx-go') === 'boxes') move('boxes', 'pop'); else toList();
        return;
      }
      // From the mailboxes screen, a folder or view goes on to its list.
      if (at() === 'boxes' && t.closest('#vm-folders a, .mx-switch__row')) move('list', 'push');
      var menu = t.closest('[data-mx-menu]');
      if (menu) {
        var label = menu.getAttribute('data-mx-menu');
        var sums = document.querySelectorAll('#vm-readpane .mx-rtools details.sa-pop > summary');
        for (var i = 0; i < sums.length; i++) if (sums[i].getAttribute('aria-label') === label) { sums[i].parentNode.open = true; break; }
      }
    });
    // The list's large title shrinks into the bar as the list scrolls.
    if (list) {
      list.addEventListener('scroll', function () { list.classList.toggle('is-scrolled', list.scrollTop > 24); }, { passive: true });
    }
  })();

  // Each folder keeps its scroll position for the session: leaving one
  // remembers where its list was, and coming back puts it there again.
  var scrolls = {}, switching = false;
  function listKey() {
    var f = document.querySelector('#vm-inbox-list input[name="folder"][data-vm-scope]');
    return (f ? f.value : '') + '|' + ((window.location.search.match(/[?&]view=([a-z]+)/) || [])[1] || '');
  }
  document.body.addEventListener('htmx:beforeRequest', function (e) {
    var list = document.getElementById('vm-inbox-list');
    if (!list || !e.target || !e.target.closest || !e.target.closest('#vm-folders')) return;
    scrolls[listKey()] = list.scrollTop;
    switching = true;
  });
  document.body.addEventListener('htmx:afterSwap', function (e) {
    var list = document.getElementById('vm-inbox-list');
    if (!list || e.target !== list) return;
    if (switching) { switching = false; list.scrollTop = scrolls[listKey()] || 0; }
    if (!openHref) return;
    var links = list.querySelectorAll('[data-vm-open]');
    for (var i = 0; i < links.length; i++) {
      if (links[i].getAttribute('href') === openHref) { links[i].closest('[data-vm-row]').classList.add('vm-active'); break; }
    }
  });

  // A message that lands in the pane starts at its top. The pane is its own
  // scroller; scrolling the reader into view would move the columns around it.
  document.addEventListener('htmx:afterSwap', function (e) {
    var pane = document.getElementById('vm-readpane');
    if (!pane || !e.target || e.target !== pane) return;
    pane.scrollTop = 0;
  });

  // Print prints just the reader (the @media print rules).
  document.addEventListener('click', function (e) {
    if (e.target && e.target.closest && e.target.closest('[data-vm-print]')) window.print();
  });

  function cookie(name) {
    var m = document.cookie.match(new RegExp('(?:^|; )' + name + '=([^;]*)'));
    return m ? decodeURIComponent(m[1]) : '';
  }

  function postJSON(url, data) {
    return fetch(url, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': cookie('vp_csrf') },
      body: JSON.stringify(data || {}),
    }).then(function (r) {
      return r.json().catch(function () { return {}; }).then(function (body) {
        return { ok: r.ok, status: r.status, body: body };
      });
    });
  }

  // errText turns a failed response into a readable message, with a clear hint
  // for the expired-CSRF (403) case so the operator knows to just reload.
  function errText(res) {
    if (res.status === 403) return 'session token expired — reload the page and try again';
    return (res.body && res.body.message) || res.status;
  }

  function val(root, sel) {
    var el = root.querySelector(sel);
    return el ? (el.value || '').trim() : '';
  }

  // ── Compose ────────────────────────────────────────────────────────────────
  // One composer, two homes: the compose page, and the sheet Mail opens over
  // the reader (Mail plan §5). initCompose wires either and returns what the
  // sheet needs to close it: save what is written, or let it go.
  function initCompose(compose) {
    var cStatus = compose.querySelector('[data-c-status]');
    var sendBtn = compose.querySelector('[data-c-send]');
    var EMAIL_RE = /^[^\s@,;]+@[^\s@,;]+\.[^\s@,;]+$/;
    // Set before anything paints: the chips draw as soon as they are wired.
    var recipients = {};
    var encLine = compose.querySelector('[data-c-enc]');
    var encToggle = compose.querySelector('[data-c-encrypt]');
    function localPart(addr) { var m = (addr || '').match(/[^<@\s]+(?=@)/); return m ? m[0] : ''; }

    // ── Formatting toolbar ─────────────────────────────────────────────────
    // Wraps or prefixes the selection with plain-text conventions. The body is
    // sent as text/plain, so what the composer shows is exactly what is
    // delivered — no hidden HTML alternative that only some clients honour.
    var bodyEl = compose.querySelector('[data-c-body]');
    var countEl = compose.querySelector('[data-c-count]');

    function updateCount() {
      if (!countEl || !bodyEl) { return; }
      var text = bodyEl.value;
      var words = text.trim() ? text.trim().split(/\s+/).length : 0;
      countEl.textContent = words + (words === 1 ? ' word' : ' words') + ' · ' + text.length + ' characters';
    }

    // Wrap the selection in a marker, or unwrap it when it is already wrapped so
    // the button toggles instead of stacking asterisks on every click.
    function wrapSelection(marker) {
      var start = bodyEl.selectionStart, end = bodyEl.selectionEnd;
      var value = bodyEl.value;
      var selected = value.slice(start, end);
      var before = value.slice(0, start), after = value.slice(end);
      if (before.endsWith(marker) && after.startsWith(marker)) {
        bodyEl.value = before.slice(0, -marker.length) + selected + after.slice(marker.length);
        bodyEl.setSelectionRange(start - marker.length, end - marker.length);
      } else {
        bodyEl.value = before + marker + (selected || 'text') + marker + after;
        var inner = start + marker.length;
        bodyEl.setSelectionRange(inner, inner + (selected ? selected.length : 4));
      }
    }

    // Prefix every line of the selection, numbering when asked. Re-running it on
    // an already-prefixed block removes the prefix.
    function prefixLines(prefix, numbered) {
      var value = bodyEl.value;
      var start = value.lastIndexOf('\n', bodyEl.selectionStart - 1) + 1;
      var end = bodyEl.selectionEnd;
      var lineEnd = value.indexOf('\n', end);
      if (lineEnd === -1) { lineEnd = value.length; }
      var block = value.slice(start, lineEnd);
      var lines = block.split('\n');
      var already = lines.every(function (l) {
        return !l.trim() || (numbered ? /^\d+\.\s/.test(l) : l.indexOf(prefix) === 0);
      });
      var out = lines.map(function (l, i) {
        if (!l.trim()) { return l; }
        if (already) { return l.replace(numbered ? /^\d+\.\s/ : new RegExp('^' + prefix.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')), ''); }
        return (numbered ? (i + 1) + '. ' : prefix) + l;
      }).join('\n');
      bodyEl.value = value.slice(0, start) + out + value.slice(lineEnd);
      bodyEl.setSelectionRange(start, start + out.length);
    }

    function insertBlock(text) {
      var start = bodyEl.selectionStart;
      var value = bodyEl.value;
      var lead = (start > 0 && value[start - 1] !== '\n') ? '\n' : '';
      bodyEl.value = value.slice(0, start) + lead + text + value.slice(bodyEl.selectionEnd);
      var pos = start + lead.length + text.length;
      bodyEl.setSelectionRange(pos, pos);
    }

    // openLinkModal asks for a URL in the console's own dialog and hands it back
    // through the callback (empty string when cancelled). The formatting toolbar
    // used window.prompt, which is unstyled, blocks the whole tab, and ignores the
    // console's Escape/focus conventions — the same reason the "set password"
    // prompt was replaced.
    function openLinkModal(done) {
      var back = el('div', 'modal-backdrop');
      var panel = el('div', 'modal-panel');
      var head = el('div', 'modal-header');
      head.appendChild(el('div', 'modal-title', 'Insert link'));
      var x = el('button', 'modal-close', '×'); x.type = 'button';
      head.appendChild(x);
      var body = el('div', 'modal-body');
      var f = el('div', 'field');
      var l = el('label', 'field-label', 'Web address'); l.setAttribute('for', 'mb-link-url');
      var i = document.createElement('input');
      i.id = 'mb-link-url'; i.className = 'input'; i.type = 'url';
      i.placeholder = 'https://'; i.value = 'https://';
      f.appendChild(l); f.appendChild(i); body.appendChild(f);
      body.appendChild(el('p', 'field-hint', 'The link is written into the message text so it reads the same in every mail client.'));
      var foot = el('div', 'modal-footer');
      var cancel = el('button', 'btn btn--ghost btn--sm', 'Cancel'); cancel.type = 'button';
      var ok = el('button', 'btn btn--primary btn--sm', 'Insert'); ok.type = 'button';
      foot.appendChild(cancel); foot.appendChild(ok);
      panel.appendChild(head); panel.appendChild(body); panel.appendChild(foot);
      back.appendChild(panel); document.body.appendChild(back);
      i.focus(); i.select();
      function close() {
        document.removeEventListener('keydown', onKey);
        if (back.parentNode) back.parentNode.removeChild(back);
        if (bodyEl && bodyEl.focus) bodyEl.focus();
      }
      function onKey(ev) { if (ev.key === 'Escape') { ev.preventDefault(); close(); done(''); } }
      document.addEventListener('keydown', onKey);
      x.addEventListener('click', function () { close(); done(''); });
      cancel.addEventListener('click', function () { close(); done(''); });
      back.addEventListener('click', function (ev) { if (ev.target === back) { close(); done(''); } });
      function submit() { var u = (i.value || '').trim(); close(); done(u); }
      ok.addEventListener('click', submit);
      i.addEventListener('keydown', function (ev) { if (ev.key === 'Enter') { ev.preventDefault(); submit(); } });
    }

    function applyFormat(kind) {
      if (!bodyEl) { return; }
      bodyEl.focus();
      switch (kind) {
        case 'bold':   wrapSelection('**'); break;
        case 'italic': wrapSelection('*'); break;
        case 'strike': wrapSelection('~~'); break;
        case 'h2':     prefixLines('## '); break;
        case 'ul':     prefixLines('- '); break;
        case 'ol':     prefixLines('', true); break;
        case 'quote':  prefixLines('> '); break;
        case 'code': {
          var s0 = bodyEl.selectionStart, e0 = bodyEl.selectionEnd;
          var sel = bodyEl.value.slice(s0, e0);
          insertBlock('```\n' + (sel || 'code') + '\n```\n');
          break;
        }
        case 'link': {
          var a = bodyEl.selectionStart, b = bodyEl.selectionEnd;
          var label = bodyEl.value.slice(a, b) || 'link text';
          // A real dialog rather than window.prompt: the prompt is unstyled, blocks
          // the tab, and cannot be reached by the console's keyboard handling.
          openLinkModal(function (url) {
            if (!url) { return; }
            bodyEl.value = bodyEl.value.slice(0, a) + label + ' <' + url + '>' + bodyEl.value.slice(b);
            updateCount();
          });
          break;
        }
        case 'rule': insertBlock('\n---\n\n'); break;
      }
      updateCount();
    }

    compose.querySelectorAll('[data-c-fmt]').forEach(function (btn) {
      btn.addEventListener('click', function () { applyFormat(btn.getAttribute('data-c-fmt')); });
    });

    if (bodyEl) {
      bodyEl.addEventListener('input', updateCount);
      // Familiar shortcuts. Ctrl/Cmd-Enter sends, which is what every mail client
      // trains people to expect.
      bodyEl.addEventListener('keydown', function (e) {
        var mod = e.ctrlKey || e.metaKey;
        if (!mod) { return; }
        var k = e.key.toLowerCase();
        if (k === 'b') { e.preventDefault(); applyFormat('bold'); }
        else if (k === 'i') { e.preventDefault(); applyFormat('italic'); }
        else if (k === 'k') { e.preventDefault(); applyFormat('link'); }
      });
      updateCount();
    }
    // Cmd/Ctrl+Enter sends from anywhere in the composer, which is what every
    // mail client trains people to expect.
    compose.addEventListener('keydown', function (e) {
      if ((e.ctrlKey || e.metaKey) && e.key === 'Enter' && sendBtn) { e.preventDefault(); sendBtn.click(); }
    });
    // Format shows the formatting bar; the message itself stays plain text.
    var fmtBtn = compose.querySelector('[data-c-format]');
    var fmtBar = compose.querySelector('[data-c-toolbar]');
    if (fmtBtn && fmtBar) {
      fmtBtn.addEventListener('click', function () {
        fmtBar.hidden = !fmtBar.hidden;
        fmtBtn.setAttribute('aria-pressed', fmtBar.hidden ? 'false' : 'true');
      });
    }
    // The sheet's title is the subject as it is typed.
    var titleEl = compose.querySelector('[data-c-title]');
    var subjEl = compose.querySelector('[data-c-subject]');
    if (titleEl && subjEl) {
      subjEl.addEventListener('input', function () { titleEl.textContent = subjEl.value.trim() || 'New message'; });
    }
    // A reply's quote is folded away under the reply, and goes with it unless
    // the sender leaves it out.
    var quoteEl = compose.querySelector('[data-c-quote]');
    var quoteDrop = compose.querySelector('[data-c-quote-drop]');
    if (quoteDrop) {
      quoteDrop.addEventListener('click', function () {
        var box = compose.querySelector('[data-c-quoted]');
        if (box) box.remove();
        quoteEl = null;
      });
    }

    // Preview: renders the plain-text conventions the way a reader's eye groups
    // them. Built with textContent per node, never innerHTML, so a message can
    // never inject markup into the console.
    var previewBtn = compose.querySelector('[data-c-preview]');
    var previewPane = compose.querySelector('[data-c-preview-pane]');
    if (previewBtn && previewPane && bodyEl) {
      previewBtn.addEventListener('click', function () {
        var showing = previewPane.hasAttribute('hidden');
        if (!showing) {
          previewPane.setAttribute('hidden', '');
          bodyEl.removeAttribute('hidden');
          previewBtn.setAttribute('aria-pressed', 'false');
          previewBtn.textContent = 'Preview';
          return;
        }
        previewPane.textContent = '';
        bodyEl.value.split(/\n/).forEach(function (line) {
          var row = document.createElement('div');
          var t = line.trim();
          if (!t) { row.className = 'vm-pv-blank'; }
          else if (t.indexOf('## ') === 0) { row.className = 'vm-pv-h'; row.textContent = t.slice(3); }
          else if (t.indexOf('> ') === 0) { row.className = 'vm-pv-q'; row.textContent = t.slice(2); }
          else if (t === '---') { row.className = 'vm-pv-hr'; }
          else { row.textContent = line; }
          // A literal non-breaking space, not innerHTML: an empty div collapses,
          // and the claim above about never using innerHTML has to hold here too.
          if (!row.textContent && row.className !== 'vm-pv-hr') { row.textContent = '\u00a0'; }
          previewPane.appendChild(row);
        });
        previewPane.removeAttribute('hidden');
        bodyEl.setAttribute('hidden', '');
        previewBtn.setAttribute('aria-pressed', 'true');
        previewBtn.textContent = 'Edit';
      });
    }

    // Reveal Cc/Bcc and Reply-To on demand (kept out of the way by default).
    var toggle = function (btnSel, fieldSels) {
      var btn = compose.querySelector(btnSel);
      if (!btn) return;
      btn.addEventListener('click', function () {
        fieldSels.forEach(function (s) { var el = compose.querySelector(s); if (el) el.hidden = !el.hidden; });
      });
    };
    toggle('[data-c-toggle-cc]', ['[data-c-cc-field]', '[data-c-bcc-field]']);
    toggle('[data-c-toggle-reply]', ['[data-c-reply-field]']);

    // Recipient chips: type an address + Enter/comma → a chip; invalid ones are
    // flagged; the comma-joined value is mirrored into the hidden data-c-<field>
    // input so the send/draft payloads are unchanged.
    function setupChips(field) {
      var container = compose.querySelector('[data-c-chips="' + field + '"]');
      var hidden = compose.querySelector('[data-c-' + field + ']');
      if (!container || !hidden) return;
      var input = container.querySelector('[data-c-chip-input]');
      var list = [];
      function render() {
        container.querySelectorAll('.vm-chip').forEach(function (c) { c.remove(); });
        list.forEach(function (addr, i) {
          var chip = document.createElement('span');
          var ok = EMAIL_RE.test(addrOf(addr));
          chip.className = 'vm-chip' + (ok ? '' : ' vm-chip--bad');
          if (!ok) chip.title = 'This does not look like a valid email address';
          var info = ok ? recipientInfo(addr, render) : null;
          if (info) {
            var av = document.createElement('span');
            av.className = 'vm-av vm-av--' + info.tone; av.setAttribute('aria-hidden', 'true'); av.textContent = info.initials;
            chip.appendChild(av);
          }
          var label = document.createElement('span'); label.textContent = nameOf(addr, true); label.title = addrOf(addr);
          chip.appendChild(label);
          if (info && info.key) {
            var lock = document.createElement('span');
            lock.className = 'vm-chip__lock'; lock.setAttribute('role', 'img'); lock.setAttribute('aria-label', 'has a key');
            lock.appendChild(window.vpIcon('lock'));
            chip.appendChild(lock);
          }
          var x = document.createElement('button');
          x.type = 'button'; x.className = 'vm-chip-x'; x.setAttribute('aria-label', 'Remove ' + addr); x.textContent = '×';
          x.addEventListener('click', function () { list.splice(i, 1); render(); sync(); });
          chip.appendChild(x);
          container.insertBefore(chip, input);
        });
      }
      function sync() { hidden.value = list.join(', '); paintEnc(); }
      function addFromText(text) {
        (text || '').split(/[,;\n]/).forEach(function (t) { t = t.trim(); if (t && list.indexOf(t) === -1) list.push(t); });
      }
      input.addEventListener('keydown', function (e) {
        if (e.key === 'Enter' || e.key === ',' || e.key === ';') {
          e.preventDefault();
          if (input.value.trim()) { addFromText(input.value); input.value = ''; render(); sync(); }
        } else if (e.key === 'Backspace' && !input.value && list.length) {
          list.pop(); render(); sync();
        }
      });
      input.addEventListener('blur', function () {
        if (input.value.trim()) { addFromText(input.value); input.value = ''; render(); sync(); }
      });
      if (hidden.value.trim()) {
        addFromText(hidden.value); render(); sync(); // prefill (reply/forward/draft)
        // A prefilled Cc/Bcc means the draft HAS those recipients, so reveal the
        // field instead of leaving them hidden behind the Cc/Bcc toggle.
        var fieldWrap = compose.querySelector('[data-c-' + field + '-field]');
        if (fieldWrap) fieldWrap.hidden = false;
      }
    }
    setupChips('to'); setupChips('cc'); setupChips('bcc');

    // Attachment tray: array-backed so files can be removed and drag-dropped
    // (an <input type=file> FileList is immutable). The send handler builds the
    // multipart body from this array.
    var filesEl = compose.querySelector('[data-c-files]');
    // The whole composer takes dropped files, lit while they are over it.
    var dropzone = compose;
    var tray = compose.querySelector('[data-c-attach-list]');
    var browseBtn = compose.querySelector('[data-c-attach-btn]');
    var composeFiles = [];
    function humanSize(n) {
      if (n < 1024) return n + ' B';
      if (n < 1048576) return (n / 1024).toFixed(1) + ' KB';
      return (n / 1048576).toFixed(1) + ' MB';
    }
    function renderFiles() {
      if (!tray) return;
      tray.textContent = '';
      // Say where the files go: they are stored with the draft, not autosaved.
      if (composeFiles.length > 0 && cStatus && !cStatus.textContent) {
        cStatus.textContent = 'Files are kept with the draft when it is closed.';
      }
      composeFiles.forEach(function (f, i) {
        var chip = document.createElement('span');
        chip.className = 'vm-attach-chip';
        var ico = document.createElement('span'); ico.className = 'vm-attach-ico'; ico.appendChild(window.vpIcon('doc'));
        var name = document.createElement('span'); name.className = 'vm-attach-name'; name.textContent = f.name;
        var size = document.createElement('span'); size.className = 'vm-attach-size'; size.textContent = humanSize(f.size);
        var x = document.createElement('button');
        x.type = 'button'; x.className = 'vm-chip-x'; x.setAttribute('aria-label', 'Remove ' + f.name); x.textContent = '×';
        x.addEventListener('click', function () { composeFiles.splice(i, 1); renderFiles(); });
        chip.appendChild(ico); chip.appendChild(name); chip.appendChild(size); chip.appendChild(x);
        tray.appendChild(chip);
      });
    }
    // Each added file slides into the row (Mail plan §8, item 7); the ones
    // already there stay still.
    var shownFiles = 0;
    function addFiles(fileList) {
      shownFiles = composeFiles.length;
      for (var i = 0; i < fileList.length; i++) composeFiles.push(fileList[i]);
      renderFiles();
      Array.prototype.slice.call(tray ? tray.children : []).forEach(function (chip, n) { if (n >= shownFiles) chip.classList.add('is-new'); });
    }
    if (browseBtn && filesEl) browseBtn.addEventListener('click', function () { filesEl.click(); });
    if (filesEl) filesEl.addEventListener('change', function () { addFiles(filesEl.files); filesEl.value = ''; });
    if (dropzone) {
      ['dragenter', 'dragover'].forEach(function (ev) {
        dropzone.addEventListener(ev, function (e) { e.preventDefault(); dropzone.classList.add('is-drop'); });
      });
      ['dragleave', 'drop'].forEach(function (ev) {
        dropzone.addEventListener(ev, function (e) { e.preventDefault(); dropzone.classList.remove('is-drop'); });
      });
      dropzone.addEventListener('drop', function (e) { if (e.dataTransfer && e.dataTransfer.files) addFiles(e.dataTransfer.files); });
    }

    // Who each recipient is, and whether a message to them can be encrypted,
    // asked once per address of the server (handleVayuOSComposeRecipient),
    // which resolves the key as the send will. again is called when it lands.
    function addrOf(a) { var m = (a || '').match(/<([^>]+)>/); return (m ? m[1] : a || '').trim(); }
    function nameOf(a, whole) {
      var m = (a || '').match(/^\s*"?([^"<]+?)"?\s*</);
      if (m) return whole ? m[1] : m[1].split(/\s+/)[0];
      return whole ? addrOf(a) : addrOf(a).split('@')[0];
    }
    function recipientInfo(addr, again) {
      var k = addrOf(addr).toLowerCase();
      var got = recipients[k];
      if (got && got !== 'asking') return got;
      if (!got) {
        recipients[k] = 'asking';
        fetch('/os/vayumail/compose/recipient?addr=' + encodeURIComponent(addr) + '&user=' + encodeURIComponent(compose.getAttribute('data-c-user') || ''), { credentials: 'same-origin' })
          .then(function (r) { return r.ok ? r.json() : { key: false, initials: '', tone: 0 }; })
          .catch(function () { return { key: false, initials: '', tone: 0 }; })
          .then(function (j) { recipients[k] = j; if (again) again(); paintEnc(); });
      }
      return null;
    }
    function allRecipients() {
      return ['to', 'cc', 'bcc'].reduce(function (out, f) {
        return out.concat(val(compose, '[data-c-' + f + ']').split(',').map(function (t) { return t.trim(); }).filter(Boolean));
      }, []);
    }
    // The line beside Send says what the send will do: the send encrypts only
    // when encryption is on and every recipient has a key (engine
    // ComposeRich), and those are exactly what this line reads.
    function names(list) {
      return list.length <= 2 ? list.join(' and ') : list.slice(0, 2).join(', ') + ' and ' + (list.length - 2) + ' more';
    }
    function paintEnc() {
      if (!encLine) return;
      var rs = allRecipients().filter(function (a) { return EMAIL_RE.test(addrOf(a)); });
      encLine.hidden = rs.length === 0;
      if (!rs.length) return;
      var text, tone = '';
      if (!pgpEncrypt()) {
        text = 'Sent as readable text: encryption is off';
        encLine.title = 'Turn encryption on';
      } else {
        var asking = false, missing = [];
        rs.forEach(function (a) {
          var info = recipientInfo(a);
          if (!info) asking = true; else if (!info.key) missing.push(nameOf(a));
        });
        encLine.title = 'Turn encryption off';
        if (asking) text = 'Checking keys…';
        else if (missing.length) text = 'Sent as readable text: no key for ' + names(missing);
        else { text = 'Encrypted for ' + (rs.length === 1 ? nameOf(rs[0]) : rs.length === 2 ? 'both' : 'all ' + rs.length); tone = 'ok'; }
      }
      encLine.textContent = '';
      if (tone === 'ok') encLine.appendChild(window.vpIcon('lock'));
      encLine.appendChild(document.createTextNode(text));
      encLine.className = 'mx-compose__enc' + (tone ? ' mx-compose__enc--' + tone : '');
    }

    // ── Signature ────────────────────────────────────────────────────────────
    // Each From <option> carries its account's signature in data-sig. The editor
    // saves it (self-service for your own mailbox; admins for the selected one),
    // and the "Append signature" toggle controls whether it is added on send.
    var fromSel = compose.querySelector('[data-c-from]');
    var sigToggle = compose.querySelector('[data-c-sig-toggle]');
    var sigText = compose.querySelector('[data-c-sig-text]');
    var sigPreview = compose.querySelector('[data-c-sig-preview]');
    var sigSave = compose.querySelector('[data-c-sig-save]');
    var sigStatus = compose.querySelector('[data-c-sig-status]');
    function currentSig() {
      if (!fromSel || fromSel.selectedIndex < 0) return '';
      var opt = fromSel.options[fromSel.selectedIndex];
      return opt ? (opt.getAttribute('data-sig') || '') : '';
    }
    function paintSig() {
      var s = currentSig();
      if (sigText && document.activeElement !== sigText) sigText.value = s;
      if (sigPreview) sigPreview.textContent = s; // empty, and so hidden, without one
    }
    function sigAppend() { return !(sigToggle && !sigToggle.checked); }
    function pgpEncrypt() { return !!(encToggle && encToggle.checked); }
    var richToggle = compose.querySelector('[data-c-rich]');
    function richHTML() { return !!(richToggle && richToggle.checked); }
    // Signing (OpenPGP): offered only for a sender with a key to sign with
    // (data-can-sign on its From option), ticked by that sender's default
    // (data-sign); the second box changes the default itself.
    var signBox = compose.querySelector('[data-c-sign]');
    var signDefault = compose.querySelector('[data-c-sign-default]');
    function fromOpt() { return fromSel && fromSel.selectedIndex >= 0 ? fromSel.options[fromSel.selectedIndex] : null; }
    function paintSign() {
      var opt = fromOpt();
      var can = !!(opt && opt.getAttribute('data-can-sign'));
      var dflt = !!(opt && opt.getAttribute('data-sign'));
      compose.querySelectorAll('[data-c-sign-opt]').forEach(function (l) { l.hidden = !can; });
      if (signBox) signBox.checked = can && dflt;
      if (signDefault) signDefault.checked = dflt;
    }
    function pgpSign() { return !!(signBox && signBox.checked && !signBox.closest('[hidden]')); }
    if (fromSel) fromSel.addEventListener('change', paintSign);
    paintSign();
    if (signDefault) {
      signDefault.addEventListener('change', function () {
        var opt = fromOpt();
        if (!opt) return;
        var on = signDefault.checked;
        postJSON('/os/vayumail/accounts/update', { email: opt.value, sign_by_default: on }).then(function (res) {
          if (!res.ok) {
            signDefault.checked = !on;
            if (cStatus) cStatus.textContent = 'Not changed: ' + errText(res);
            return;
          }
          if (on) opt.setAttribute('data-sign', '1'); else opt.removeAttribute('data-sign');
          if (signBox) signBox.checked = on;
          if (cStatus) cStatus.textContent = on ? 'Messages from ' + opt.value + ' are signed unless you untick it.' : 'Messages from ' + opt.value + ' are no longer signed unless you tick it.';
        });
      });
    }
    if (encLine && encToggle) encLine.addEventListener('click', function () { encToggle.checked = !encToggle.checked; paintEnc(); });
    if (fromSel) fromSel.addEventListener('change', paintSig);
    if (sigSave) {
      sigSave.addEventListener('click', function () {
        if (!fromSel) return;
        var email = fromSel.value, text = sigText ? sigText.value : '';
        sigSave.disabled = true;
        if (sigStatus) sigStatus.textContent = 'Saving…';
        postJSON('/os/vayumail/accounts/update', { email: email, signature: text }).then(function (res) {
          sigSave.disabled = false;
          if (res.ok) {
            var opt = fromSel.options[fromSel.selectedIndex];
            if (opt) opt.setAttribute('data-sig', text);
            paintSig();
            if (sigStatus) sigStatus.textContent = 'Saved ✓';
            acctToast('Signature saved');
          } else {
            if (sigStatus) sigStatus.textContent = 'Failed: ' + errText(res);
            acctToast('Signature save failed: ' + errText(res), true);
          }
        });
      });
    }
    paintSig();
    paintEnc();

    var composeFields = function () {
      return {
        from: val(compose, '[data-c-from]'),
        to: val(compose, '[data-c-to]'),
        cc: val(compose, '[data-c-cc]'),
        bcc: val(compose, '[data-c-bcc]'),
        replyTo: val(compose, '[data-c-reply]'),
        subject: val(compose, '[data-c-subject]'),
        // The folded quote goes back under the reply, where it always was in
        // the body (insertSignature puts the signature above it).
        body: val(compose, '[data-c-body]') + (quoteEl ? '\r\n\r\n' + quoteEl.textContent : ''),
        // Present only when this composer was opened from a saved draft. The send
        // path uses it to merge the files that draft is holding, so reopening a
        // draft and pressing Send does not ship a message without its attachments.
        draft_id: val(compose, '[data-c-draft-id]'),
      };
    };

    // Draft: manual save + a debounced autosave that REPLACES the previous
    // autosaved draft (delete-then-save) so Drafts never fills with copies.
    var draftId = '';
    var lastSavedSig = '';
    var autosaveTimer = null;
    function saveDraft(silent, done) {
      var f = composeFields();
      if (!f.to && !f.subject && !f.body) { if (done) done(false, true); return; }
      // The signature includes the file count, so attaching a file is itself a
      // reason to save.
      var sig = f.to + '|' + f.subject + '|' + f.body + '|' + composeFiles.length;
      if (silent && sig === lastSavedSig) { if (done) done(true); return; }
      lastSavedSig = sig;
      if (!silent && cStatus) cStatus.textContent = 'Saving draft…';
      var prev = draftId, user = localPart(f.from);
      // With files attached the draft goes up as multipart so the FILES are stored
      // too. A draft that silently dropped them lost work the sender could see on
      // screen but not get back.
      var req;
      if (composeFiles.length > 0) {
        var fd = new FormData();
        Object.keys(f).forEach(function (k) { fd.append(k, f[k] || ''); });
        composeFiles.forEach(function (file) { fd.append('attachments', file); });
        req = fetch('/os/vayumail/draft', {
          method: 'POST',
          headers: { 'X-CSRF-Token': cookie('vp_csrf') },
          body: fd,
        }).then(function (r) {
          return r.json().catch(function () { return {}; }).then(function (b) { return { ok: r.ok, status: r.status, body: b }; });
        });
      } else {
        req = postJSON('/os/vayumail/draft', f);
      }
      req.then(function (res) {
        if (res.ok) {
          draftId = (res.body && res.body.id) || '';
          if (prev && user && prev !== draftId) {
            postJSON('/os/vayumail/message/action', { user: user, folder: 'Drafts', id: prev, delete: true });
          }
          if (cStatus) { var t = new Date(); cStatus.textContent = 'Draft saved ' + ('0' + t.getHours()).slice(-2) + ':' + ('0' + t.getMinutes()).slice(-2); }
          if (done) done(true);
        } else {
          if (!silent && cStatus) cStatus.textContent = 'Draft failed: ' + errText(res);
          if (done) done(false);
        }
      });
    }
    function stopAutosave() { if (autosaveTimer) { clearInterval(autosaveTimer); autosaveTimer = null; } }
    // Autosave deliberately skips while files are attached: it would re-upload
    // them on every text change. "Save as draft" still stores them, and the
    // composer says so when a file is added.
    function armAutosave() {
      stopAutosave();
      autosaveTimer = setInterval(function () { if (composeFiles.length === 0) saveDraft(true); }, 20000);
    }
    armAutosave();

    // Discard: the draft goes, saved copy included, and the composer closes.
    // Closing keeps what is written instead (close below); the page has no
    // close of its own beyond its link back to the mailbox.
    var discarded = false;
    var discardBtn = compose.querySelector('[data-c-discard]');
    if (discardBtn) {
      discardBtn.addEventListener('click', function () {
        discarded = true;
        stopAutosave();
        var f = composeFields();
        if (draftId) postJSON('/os/vayumail/message/action', { user: localPart(f.from), folder: 'Drafts', id: draftId, delete: true });
        compose.dispatchEvent(new CustomEvent('mx-compose-done', { bubbles: true, detail: { why: 'discarded' } }));
        // The way back is built here, a fixed path and the mailbox encoded,
        // never read from the page: an address taken from markup and handed to
        // location is a script waiting for a javascript: URL (CodeQL #120).
        if (!compose.closest('[data-mx-compose-host]')) {
          var box = new URLSearchParams(window.location.search).get('user');
          window.location.href = '/os/vayumail/inbox' + (box ? '?user=' + encodeURIComponent(box) : '');
        }
      });
    }

    // ── Undo-send ──────────────────────────────────────────────────────────
    // Clicking Send holds the message behind an Undo control for a few seconds
    // rather than dispatching immediately. If the operator leaves during the
    // hold, a beforeunload handler fires the send (keepalive) so nothing is
    // lost. A "sending" guard prevents a double send.
    var HOLD_MS = 8000;
    var holdTimer = null, countdownTimer = null, holdSnapshot = null, sending = false;
    var undoBar = compose.querySelector('[data-c-undobar]');
    var undoText = compose.querySelector('[data-c-undo-text]');
    var undoBtn = compose.querySelector('[data-c-undo]');

    function clearHoldTimers() {
      if (holdTimer) { clearTimeout(holdTimer); holdTimer = null; }
      if (countdownTimer) { clearInterval(countdownTimer); countdownTimer = null; }
    }
    function sendDone(res) {
      if (sendBtn) sendBtn.disabled = false;
      if (res.ok) {
        stopAutosave();
        if (holdSnapshot && holdSnapshot.draftId) {
          postJSON('/os/vayumail/message/action', { user: localPart(holdSnapshot.from), folder: 'Drafts', id: holdSnapshot.draftId, delete: true });
        }
        var later = res.body && res.body.scheduled;
        var said = later ? 'Scheduled for ' + laterLabel(new Date(res.body.sendAt)) : 'Message sent';
        if (cStatus) cStatus.textContent = later ? 'Scheduled' : 'Sent';
        acctToast(said);
        // The sidebar gains Scheduled: redraw the list beside the sheet.
        if (later) document.body.dispatchEvent(new CustomEvent('vm-mail-changed'));
        if (later && !compose.closest('[data-mx-compose-host]')) {
          var box = new URLSearchParams(window.location.search).get('user');
          setTimeout(function () { window.location.href = '/os/vayumail/inbox?folder=Scheduled' + (box ? '&user=' + encodeURIComponent(box) : ''); }, 650);
          return;
        }
        // In the sheet the mailbox stays where it was; the page goes to Sent.
        if (compose.closest('[data-mx-compose-host]')) compose.dispatchEvent(new CustomEvent('mx-compose-done', { bubbles: true, detail: { why: 'sent' } }));
        else setTimeout(function () { window.location.href = '/os/vayumail/sent'; }, 650);
      } else {
        sending = false;
        holdSnapshot = null;
        if (cStatus) cStatus.textContent = 'Failed: ' + errText(res);
        acctToast('Send failed: ' + errText(res), true);
      }
    }
    function performSend(keepalive) {
      if (sending || !holdSnapshot) return;
      sending = true;
      clearHoldTimers();
      if (undoBar) undoBar.setAttribute('hidden', '');
      var snap = holdSnapshot;
      var opts = { method: 'POST', keepalive: !!keepalive, headers: { 'X-CSRF-Token': cookie('vp_csrf') } };
      if (snap.files.length > 0) {
        var fd = new FormData();
        Object.keys(snap.fields).forEach(function (k) { fd.append(k, snap.fields[k] || ''); });
        snap.files.forEach(function (file) { fd.append('attachments', file); });
        fd.append('appendSig', snap.appendSig ? '1' : '0');
        fd.append('encrypt', snap.encrypt ? '1' : '0');
        fd.append('richHTML', snap.richHTML ? '1' : '0');
        fd.append('sign', snap.sign ? '1' : '0');
        if (snap.sendAt) fd.append('sendAt', snap.sendAt);
        opts.body = fd;
      } else {
        var payload = {};
        Object.keys(snap.fields).forEach(function (k) { payload[k] = snap.fields[k]; });
        payload.appendSig = snap.appendSig;
        payload.encrypt = snap.encrypt;
        payload.richHTML = snap.richHTML;
        payload.sign = snap.sign;
        if (snap.sendAt) payload.sendAt = snap.sendAt;
        opts.headers['Content-Type'] = 'application/json';
        opts.body = JSON.stringify(payload);
      }
      fetch('/os/vayumail/send', opts)
        .then(function (r) { return r.json().catch(function () { return {}; }).then(function (b) { return { ok: r.ok, status: r.status, body: b }; }); })
        .then(sendDone)
        .catch(function () { sending = false; });
    }
    function cancelHold() {
      clearHoldTimers();
      holdSnapshot = null;
      if (undoBar) undoBar.setAttribute('hidden', '');
      if (sendBtn) sendBtn.disabled = false;
      if (cStatus) cStatus.textContent = 'Cancelled — back to your draft.';
      if (!autosaveTimer) armAutosave();
    }
    if (undoBtn) undoBtn.addEventListener('click', cancelHold);

    // ── Send later ─────────────────────────────────────────────────────────
    // The times are the sender's own: built and labelled on this clock, sent
    // as an instant (UTC), held by the server until then. No Undo hold: until
    // its time it waits under Scheduled, where it can be sent or cancelled.
    var later = compose.querySelector('[data-c-later]');
    function laterLabel(d) {
      return d.toLocaleString([], { weekday: 'short', day: 'numeric', month: 'short', hour: 'numeric', minute: '2-digit' });
    }
    function atHour(daysAhead, hour) {
      var d = new Date();
      d.setDate(d.getDate() + daysAhead);
      d.setHours(hour, 0, 0, 0);
      return d;
    }
    function laterPresets() {
      var monday = (8 - new Date().getDay()) % 7 || 7;
      return { morning: ['Tomorrow morning', atHour(1, 8)], afternoon: ['Tomorrow afternoon', atHour(1, 13)], monday: ['Monday morning', atHour(monday, 8)] };
    }
    function scheduleAt(d) {
      if (sending || holdSnapshot) return;
      if (isNaN(d) || d.getTime() <= Date.now()) { if (cStatus) cStatus.textContent = 'Choose a time that has not passed.'; return; }
      var f = composeFields();
      if (!f.to && !f.cc && !f.bcc) { if (cStatus) cStatus.textContent = 'Add at least one recipient.'; return; }
      if (later) later.open = false;
      holdSnapshot = { fields: f, files: composeFiles.slice(), appendSig: sigAppend(), encrypt: pgpEncrypt(), richHTML: richHTML(), sign: pgpSign(), from: f.from, draftId: draftId, sendAt: d.toISOString() };
      stopAutosave();
      if (sendBtn) sendBtn.disabled = true;
      performSend(false);
    }
    if (later) {
      later.addEventListener('toggle', function () {
        if (!later.open) return;
        var p = laterPresets();
        later.querySelectorAll('[data-c-later-preset]').forEach(function (b) {
          var it = p[b.getAttribute('data-c-later-preset')];
          var at = document.createElement('span');
          at.className = 'mx-compose__at';
          at.textContent = laterLabel(it[1]);
          b.textContent = it[0];
          b.appendChild(at);
          // Monday is tomorrow on a Sunday: one button is enough.
          b.hidden = b.getAttribute('data-c-later-preset') === 'monday' && it[1].getTime() === p.morning[1].getTime();
        });
      });
      later.addEventListener('click', function (e) {
        var b = e.target.closest('[data-c-later-preset]');
        if (b) scheduleAt(laterPresets()[b.getAttribute('data-c-later-preset')][1]);
        if (e.target.closest('[data-c-later-go]')) {
          var v = later.querySelector('[data-c-later-at]').value;
          // datetime-local is the sender's wall time with no zone, which is
          // exactly what new Date reads it as.
          scheduleAt(v ? new Date(v) : new Date(NaN));
        }
      });
    }

    // ── Templates ──────────────────────────────────────────────────────────
    // Read as the menu opens, so one saved in another window is there.
    // Choosing one puts its text where the cursor is and its subject into an
    // empty Subject; saving keeps this message's subject and text under a
    // name, replacing a template of that name, which is how one is edited.
    var tpl = compose.querySelector('[data-c-tpl]');
    if (tpl) {
      var tplUser = compose.getAttribute('data-c-user') || '';
      var tplList = tpl.querySelector('[data-c-tpl-list]');
      var tplNote = tpl.querySelector('[data-c-tpl-note]');
      var tplName = tpl.querySelector('[data-c-tpl-name]');
      var tplItems = [];
      var paintTpl = function (ans, said) {
        tplItems = ans.templates || [];
        tplList.textContent = '';
        if (!tplItems.length) {
          var none = document.createElement('li');
          none.className = 'mx-compose__tpl-empty';
          none.textContent = 'None yet. Write a message, name it below and save it.';
          tplList.appendChild(none);
        }
        tplItems.forEach(function (t) {
          var li = document.createElement('li');
          li.className = 'mx-compose__tpl-row';
          var use = document.createElement('button');
          use.type = 'button';
          use.className = 'mx-compose__opt';
          // A menu item: the console's popovers close behind one
          // (vayuos.js), and a click during the close reopens the menu.
          use.setAttribute('role', 'menuitem');
          use.setAttribute('data-c-tpl-use', t.id);
          use.textContent = t.name;
          var del = document.createElement('button');
          del.type = 'button';
          del.className = 'mx-compose__tpl-del';
          del.setAttribute('data-c-tpl-del', t.id);
          del.setAttribute('aria-label', 'Delete the template ' + t.name);
          del.textContent = 'Delete';
          li.appendChild(use);
          li.appendChild(del);
          tplList.appendChild(li);
        });
        tplNote.textContent = ans.error ? 'Not done: ' + ans.error + '.' : (said || '');
        return !ans.error;
      };
      var tplAsk = function (req, said) {
        return req.then(function (r) { return r.json(); })
          .then(function (ans) { return paintTpl(ans, said); })
          .catch(function () { tplNote.textContent = 'Templates could not be reached.'; return false; });
      };
      var tplPost = function (form, said) {
        form.user = tplUser;
        return tplAsk(fetch('/os/vayumail/templates/action', { method: 'POST', credentials: 'same-origin', headers: { 'X-CSRF-Token': csrfToken(), 'Content-Type': 'application/x-www-form-urlencoded' }, body: new URLSearchParams(form) }), said);
      };
      // Enter in the name saves the template: left to the form, it would send
      // the message.
      tplName.addEventListener('keydown', function (e) {
        if (e.key !== 'Enter') return;
        e.preventDefault();
        tpl.querySelector('[data-c-tpl-save]').click();
      });
      tpl.addEventListener('toggle', function () {
        if (!tpl.open) return;
        tplNote.textContent = '';
        tplAsk(fetch('/os/vayumail/templates?user=' + encodeURIComponent(tplUser), { credentials: 'same-origin' }));
      });
      tpl.addEventListener('click', function (e) {
        var use = e.target.closest('[data-c-tpl-use]');
        var del = e.target.closest('[data-c-tpl-del]');
        if (use) {
          var t = tplItems.filter(function (x) { return String(x.id) === use.getAttribute('data-c-tpl-use'); })[0];
          if (!t) return;
          if (subjEl && !subjEl.value.trim() && t.subject) {
            subjEl.value = t.subject;
            subjEl.dispatchEvent(new Event('input', { bubbles: true }));
          }
          if (bodyEl) {
            bodyEl.setRangeText(t.body, bodyEl.selectionStart, bodyEl.selectionEnd, 'end');
            bodyEl.dispatchEvent(new Event('input', { bubbles: true }));
            bodyEl.focus();
          }
        } else if (del) {
          var name = del.parentNode.querySelector('[data-c-tpl-use]').textContent;
          vpConfirm({ title: 'Delete a template', message: 'Delete the template “' + name + '”? Messages written from it are not changed.', confirm: 'Delete' }, function () {
            tplPost({ action: 'delete', id: del.getAttribute('data-c-tpl-del') }, 'Deleted “' + name + '”.');
          });
        } else if (e.target.closest('[data-c-tpl-save]')) {
          var named = tplName.value.trim();
          tplPost({ action: 'save', name: named, subject: subjEl ? subjEl.value : '', body: bodyEl ? bodyEl.value : '' }, 'Saved as “' + named + '”.')
            .then(function (ok) { if (ok) tplName.value = ''; });
        }
      });
    }

    compose.addEventListener('submit', function (e) {
      e.preventDefault();
      if (sending || holdSnapshot) return; // a send is already in flight/held
      var f = composeFields();
      if (!f.to && !f.cc && !f.bcc) { if (cStatus) cStatus.textContent = 'Add at least one recipient.'; return; }
      // Snapshot at click time so edits during the hold don't leak into the send.
      holdSnapshot = { fields: f, files: composeFiles.slice(), appendSig: sigAppend(), encrypt: pgpEncrypt(), richHTML: richHTML(), sign: pgpSign(), from: f.from, draftId: draftId };
      stopAutosave();
      if (sendBtn) sendBtn.disabled = true;
      if (cStatus) cStatus.textContent = '';
      var remaining = Math.ceil(HOLD_MS / 1000);
      if (undoText) undoText.textContent = 'Sending in ' + remaining + 's…';
      if (undoBar) undoBar.removeAttribute('hidden');
      clearHoldTimers();
      countdownTimer = setInterval(function () {
        remaining -= 1;
        if (undoText) undoText.textContent = remaining > 0 ? ('Sending in ' + remaining + 's…') : 'Sending…';
      }, 1000);
      holdTimer = setTimeout(function () { performSend(false); }, HOLD_MS);
    });

    // If the operator navigates away while a send is held, fire it best-effort.
    //
    // keepalive is what lets that request outlive the page, and the Fetch spec
    // caps a keepalive body at 64 KiB: Firefox and Safari reject anything larger,
    // so a held message with an attachment was lost without a word. It is still
    // fired (Chromium carries it), and past the cap the browser is also asked to
    // confirm leaving, so no engine can drop it silently.
    var KEEPALIVE_MAX = 64 * 1024;
    function outlivesPage(snap) {
      if (snap.files.length > 0) return false;
      return new Blob([JSON.stringify(snap.fields)]).size < KEEPALIVE_MAX - 1024; // headroom for the flags
    }
    function onLeave(e) {
      if (!holdSnapshot || sending) return;
      var fits = outlivesPage(holdSnapshot);
      performSend(true);
      if (!fits) { e.preventDefault(); e.returnValue = ''; }
    }
    window.addEventListener('beforeunload', onLeave);

    return {
      // Is a send held behind Undo? The sheet then minimises instead of closing.
      holding: function () { return !!holdSnapshot && !sending; },
      // Close: keep what is written as a draft, then let go of the timers.
      // done(false) means the draft could not be kept, and the sheet stays.
      close: function (done) {
        if (discarded || sending) { window.removeEventListener('beforeunload', onLeave); done(true); return; }
        saveDraft(true, function (ok, nothing) {
          if (ok || nothing) { stopAutosave(); window.removeEventListener('beforeunload', onLeave); }
          else if (cStatus) cStatus.textContent = 'The draft could not be saved, so it stays open.';
          done(!!(ok || nothing));
        });
      },
    };
  }
  var pageCompose = document.querySelector('form[data-mail-compose]');
  if (pageCompose) initCompose(pageCompose);

  // ── The compose sheet in Mail (plan §5; motion §8 item 4) ─────────────────
  // Beside the list, every way into compose (the list's pencil, c, Reply,
  // Reply all, Forward, the reply field under a message) opens the sheet over
  // the reader instead of leaving the mailbox. It rises out of the control
  // that opened it, dims only the reader, minimises to a bar at the reader's
  // foot, expands over list and reader, and closes keeping a draft. One sheet
  // at a time: opening another keeps the first as a draft.
  var split = document.querySelector('.vm-split');
  if (split) {
    var host = document.createElement('div');
    host.className = 'mx-compose-host';
    host.setAttribute('data-mx-compose-host', '');
    host.hidden = true;
    split.appendChild(host);
    var sheet = null, opener = null;
    // carry: a field whose words become the start of the message (the reply
    // field under a message), read when the sheet arrives, so what was typed
    // while it loaded is kept too.
    var sheetOpen = function (href, trigger, carry) {
      var go = function () {
        opener = trigger || null;
        host.className = 'mx-compose-host';
        fetch(href.replace('/os/vayumail/compose', '/os/vayumail/compose/sheet'), { credentials: 'same-origin' })
          .then(function (r) { return r.text(); })
          .then(function (markup) {
            host.innerHTML = markup; // the server's own escaped fragment, as htmx would swap it
            host.hidden = false;
            var form = host.querySelector('form[data-mail-compose]');
            sheet = form ? initCompose(form) : null;
            if (window.vpWirePops) window.vpWirePops(host);
            var panel = host.querySelector('.mx-compose');
            if (panel && window.vpOpenFrom) window.vpOpenFrom(panel, opener);
            var first = form && (form.querySelector('[data-c-chips="to"] input') || null);
            var to = form && form.querySelector('[data-c-to]');
            if (form && to && to.value.trim()) first = form.querySelector('[data-c-body]');
            var body = form && form.querySelector('[data-c-body]');
            if (carry) carry.removeAttribute('data-opening');
            if (carry && body && carry.value) {
              var words = carry.value;
              carry.value = '';
              body.value = words + (body.value ? '\n\n' + body.value : '');
              body.dispatchEvent(new Event('input', { bubbles: true }));
              first = body;
              body.focus({ preventScroll: true });
              return;
            }
            if (first) first.focus({ preventScroll: true });
          });
      };
      if (sheet) sheetClose(go); else go();
    };
    var sheetClose = function (then) {
      var finish = function () {
        sheet = null;
        host.hidden = true;
        host.textContent = '';
        if (opener && document.contains(opener)) opener.focus({ preventScroll: true });
        if (then) then();
      };
      if (!sheet) { finish(); return; }
      if (sheet.holding()) { host.classList.add('is-min'); return; }
      sheet.close(function (ok) { if (ok) finish(); });
    };
    document.addEventListener('click', function (e) {
      if (e.defaultPrevented || e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return;
      var a = e.target.closest ? e.target.closest('a[href^="/os/vayumail/compose"]') : null;
      if (a && !host.contains(a)) { e.preventDefault(); sheetOpen(a.getAttribute('href'), a); return; }
      if (!e.target.closest) return;
      if (e.target.closest('[data-c-min]')) { host.classList.add('is-min'); host.classList.remove('is-max'); return; }
      var max = e.target.closest('[data-c-max]');
      if (max) {
        host.classList.remove('is-min');
        max.setAttribute('aria-pressed', host.classList.toggle('is-max') ? 'true' : 'false');
        return;
      }
      if (e.target.closest('[data-c-close]')) { sheetClose(); return; }
      // The minimised bar opens again from anywhere on it but its own tools.
      if (host.classList.contains('is-min') && e.target.closest('.mx-compose__head') && !e.target.closest('button')) host.classList.remove('is-min');
    });
    host.addEventListener('mx-compose-done', function () { sheet = null; sheetClose(); });
    // The reply field under a message (Mail plan §3.3): the first thing typed
    // opens the reply, and everything typed is carried into it.
    document.addEventListener('input', function (e) {
      var f = e.target && e.target.closest ? e.target.closest('[data-mx-quick-reply]') : null;
      if (!f || f.hasAttribute('data-opening')) return;
      f.setAttribute('data-opening', '');
      sheetOpen(f.getAttribute('data-href'), f, f);
    });
    // Escape minimises the sheet, unless a menu inside it is what is open.
    host.addEventListener('keydown', function (e) {
      if (e.key !== 'Escape' || host.querySelector('details.sa-pop[open]')) return;
      if (host.classList.contains('is-min')) return;
      e.preventDefault();
      host.classList.add('is-min');
      host.classList.remove('is-max');
      var bar = host.querySelector('[data-c-title]');
      if (bar) { bar.setAttribute('tabindex', '-1'); bar.focus({ preventScroll: true }); }
    });
    window.vpComposeSheet = sheetOpen;
  }

  // Enterprise feedback: a non-blocking toast (from the admin shell) instead of
  // a blocking alert() dialog. Falls back to alert() only if the shell toast is
  // somehow unavailable.
  //
  // The second argument is either true/false (error/ok) or an explicit kind
  // string, because vpToast only styles ok/error/info/warn — the 'success' kind
  // this used to pass matched no CSS rule, so those toasts rendered unstyled.
  function acctToast(msg, kindOrErr) {
    var kind = typeof kindOrErr === 'string' ? kindOrErr : (kindOrErr ? 'error' : 'ok');
    if (window.vpToast) { window.vpToast(msg, kind); }
    else { window.alert(msg); }
  }
  // A state change (create / 2FA / password) refreshes the collapsible account
  // list in place via HTMX — no full-page reload. Falls back to a hard reload
  // only if htmx somehow isn't present.
  function acctReload() {
    if (window.htmx && document.getElementById('vm-accounts-list')) {
      window.htmx.ajax('GET', '/os/vayumail/accounts/fragment', { target: '#vm-accounts-list', swap: 'innerHTML' });
    } else {
      setTimeout(function () { window.location.reload(); }, 550);
    }
  }

  // ── Create mail account ──────────────────────────────────────────────────────
  var acctForm = document.querySelector('form[data-acct-create]');
  if (acctForm) {
    var aStatus = acctForm.querySelector('[data-a-status]');
    acctForm.addEventListener('submit', function (e) {
      e.preventDefault();
      var local = val(acctForm, '[data-a-local]');
      var pass = val(acctForm, '[data-a-pass]');
      if (!local || pass.length < 8) { if (aStatus) aStatus.textContent = 'Address and an 8+ character password are required.'; return; }
      if (aStatus) aStatus.textContent = 'Creating…';
      postJSON('/os/vayumail/accounts/create', {
        local: local, name: val(acctForm, '[data-a-name]'), pass: pass,
        role: val(acctForm, '[data-a-role]'),
        domain: val(acctForm, '[data-a-domain]'),
        quota_mb: parseFloat(val(acctForm, '[data-a-quota]')) || 0,
      }).then(function (res) {
        if (res.ok) {
          acctToast('Mailbox ' + local + ' created');
          if (aStatus) aStatus.textContent = '';
          acctForm.querySelectorAll('[data-a-local],[data-a-name],[data-a-pass]').forEach(function (el) { el.value = ''; });
          var sheet = acctForm.closest('dialog');
          if (sheet && sheet.open) sheet.close();
          acctReload();
        } else if (aStatus) aStatus.textContent = 'Failed: ' + errText(res);
      });
    });
  }

  // el is a tiny CSP-safe element builder (no innerHTML with response data —
  // dynamic values go in via textContent / img.src / setAttribute).
  function el(tag, cls, txt) {
    var e = document.createElement(tag);
    if (cls) e.className = cls;
    if (txt != null) e.textContent = txt;
    return e;
  }

  // openMailboxTotpModal shows a premium 2FA-enrolment dialog for a mailbox: a
  // scannable QR (the otpauth label auto-fills the account name), the manual key
  // as a fallback, and a code field to confirm. Replaces the old window.prompt.
  function openMailboxTotpModal(email, data) {
    var back = el('div', 'modal-backdrop');
    var panel = el('div', 'modal-panel');
    var head = el('div', 'modal-header');
    head.appendChild(el('div', 'modal-title', 'Two-factor · ' + email));
    var x = el('button', 'modal-close', '×'); x.type = 'button';
    head.appendChild(x);
    var body = el('div', 'modal-body');
    body.appendChild(el('p', 'text-sm muted', 'Scan this QR in the account’s authenticator app (Google Authenticator, Aegis, 1Password…) — the address fills in automatically — then enter the 6-digit code to confirm. Can’t scan? Add the key by hand.'));
    if (data.qr) {
      var img = document.createElement('img');
      img.alt = '2FA setup QR code'; img.width = 188; img.height = 188; img.src = data.qr;
      img.style.background = 'var(--paper)'; img.style.padding = '8px'; img.style.borderRadius = '10px';
      img.style.display = 'block'; img.style.margin = '4px auto 14px';
      body.appendChild(img);
    }
    var keyWrap = el('p', 'text-xs muted');
    keyWrap.appendChild(document.createTextNode('Manual key: '));
    keyWrap.appendChild(el('code', null, data.secret || ''));
    body.appendChild(keyWrap);
    var field = el('div', 'field');
    var lab = el('label', 'field-label', 'Verification code'); lab.setAttribute('for', 'mb-totp-code');
    var inp = document.createElement('input');
    inp.id = 'mb-totp-code'; inp.className = 'input'; inp.type = 'text';
    inp.inputMode = 'numeric'; inp.maxLength = 6; inp.placeholder = '000000';
    inp.setAttribute('autocomplete', 'one-time-code');
    field.appendChild(lab); field.appendChild(inp); body.appendChild(field);
    var foot = el('div', 'modal-footer');
    var cancel = el('button', 'btn btn--ghost btn--sm', 'Cancel'); cancel.type = 'button';
    var verify = el('button', 'btn btn--primary btn--sm', 'Verify & enable'); verify.type = 'button';
    foot.appendChild(cancel); foot.appendChild(verify);
    panel.appendChild(head); panel.appendChild(body); panel.appendChild(foot);
    back.appendChild(panel);
    // Keyboard users get the same exits as the password dialog: Escape closes,
    // Enter in the code field verifies, and focus goes back to whatever opened it.
    var trigger = document.activeElement;
    document.body.appendChild(back);
    inp.focus();
    function close() {
      document.removeEventListener('keydown', onKey);
      if (back.parentNode) back.parentNode.removeChild(back);
      if (trigger && typeof trigger.focus === 'function') { try { trigger.focus(); } catch (e) {} }
    }
    function onKey(ev) { if (ev.key === 'Escape') { ev.preventDefault(); close(); } }
    document.addEventListener('keydown', onKey);
    x.addEventListener('click', close);
    cancel.addEventListener('click', close);
    back.addEventListener('click', function (ev) { if (ev.target === back) close(); });
    inp.addEventListener('keydown', function (ev) { if (ev.key === 'Enter') { ev.preventDefault(); verify.click(); } });
    verify.addEventListener('click', function () {
      var c = (inp.value || '').replace(/\D/g, '');
      if (c.length !== 6) { acctToast('Enter the 6-digit code.', true); return; }
      verify.disabled = true;
      postJSON('/os/vayumail/accounts/totp', { email: email, action: 'verify', code: c }).then(function (vr) {
        if (vr.ok) { close(); acctToast('Two-factor authentication is now ON for ' + email); acctReload(); }
        else { verify.disabled = false; acctToast('Verification failed: ' + errText(vr), true); }
      });
    });
  }

  // openMailboxPasswordModal replaces the old window.prompt for "Set password".
  // A prompt showed the password in cleartext with no confirmation and no way to
  // check what was typed; this is masked, confirmed, and closes on Escape with
  // focus handed back to the button that opened it.
  function openMailboxPasswordModal(email, trigger) {
    var back = el('div', 'modal-backdrop');
    var panel = el('div', 'modal-panel');
    var head = el('div', 'modal-header');
    head.appendChild(el('div', 'modal-title', 'Set password · ' + email));
    var x = el('button', 'modal-close', '×'); x.type = 'button';
    head.appendChild(x);

    var body = el('div', 'modal-body');
    body.appendChild(el('p', 'text-sm muted',
      'Choose a new password for this mailbox (at least 8 characters). Mail apps already signed in with the old password stop working until they use the new one.'));

    function pwField(label, id) {
      var f = el('div', 'field');
      var l = el('label', 'field-label', label); l.setAttribute('for', id);
      var i = document.createElement('input');
      i.id = id; i.className = 'input'; i.type = 'password';
      i.setAttribute('autocomplete', 'new-password');
      f.appendChild(l); f.appendChild(i);
      return { field: f, input: i };
    }
    var p1 = pwField('New password', 'mb-pass-new');
    var p2 = pwField('Confirm password', 'mb-pass-confirm');
    body.appendChild(p1.field); body.appendChild(p2.field);

    var showWrap = el('label', 'vm-filter-check');
    var show = document.createElement('input'); show.type = 'checkbox';
    showWrap.appendChild(show);
    showWrap.appendChild(el('span', null, 'Show passwords'));
    body.appendChild(showWrap);
    show.addEventListener('change', function () {
      var t = show.checked ? 'text' : 'password';
      p1.input.type = t; p2.input.type = t;
    });

    var foot = el('div', 'modal-footer');
    var cancel = el('button', 'btn btn--ghost btn--sm', 'Cancel'); cancel.type = 'button';
    var save = el('button', 'btn btn--primary btn--sm', 'Update password'); save.type = 'button';
    foot.appendChild(cancel); foot.appendChild(save);
    panel.appendChild(head); panel.appendChild(body); panel.appendChild(foot);
    back.appendChild(panel); document.body.appendChild(back);
    p1.input.focus();

    function close() {
      document.removeEventListener('keydown', onKey);
      if (back.parentNode) back.parentNode.removeChild(back);
      if (trigger && typeof trigger.focus === 'function') { try { trigger.focus(); } catch (e) {} }
    }
    function onKey(ev) { if (ev.key === 'Escape') { ev.preventDefault(); close(); } }
    document.addEventListener('keydown', onKey);
    x.addEventListener('click', close);
    cancel.addEventListener('click', close);
    back.addEventListener('click', function (ev) { if (ev.target === back) close(); });

    function submit() {
      var v1 = p1.input.value, v2 = p2.input.value;
      if (v1.length < 8) { acctToast('Password must be at least 8 characters.', true); p1.input.focus(); return; }
      if (v1 !== v2) { acctToast('The two passwords do not match.', true); p2.input.focus(); return; }
      save.disabled = true;
      postJSON('/os/vayumail/accounts/update', { email: email, pass: v1 }).then(function (res) {
        save.disabled = false;
        if (res.ok) { close(); acctToast('Password updated for ' + email); acctReload(); }
        else { acctToast('Update failed: ' + errText(res), true); }
      });
    }
    save.addEventListener('click', submit);
    [p1.input, p2.input].forEach(function (i) {
      i.addEventListener('keydown', function (ev) { if (ev.key === 'Enter') { ev.preventDefault(); submit(); } });
    });
  }

  // Inline mailbox actions (enable/disable, role, quota, retention, delete) are
  // now HTMX: each posts /os/vayumail/accounts/action and the server swaps the
  // #vm-accounts-list fragment in place — no per-element JS, no page reload.
  //
  // The prompt-driven controls (set password, enable/disable 2FA) live INSIDE that
  // swappable list, so binding them per-element would break after the first swap.
  // A single delegated listener on document survives every swap.
  document.addEventListener('click', function (e) {
    if (!e.target || !e.target.closest) return;

    var pw = e.target.closest('[data-acct-pass]');
    if (pw) {
      openMailboxPasswordModal(pw.getAttribute('data-acct-pass'), pw);
      return;
    }

    var en = e.target.closest('[data-acct-2fa-enable]');
    if (en) {
      var eemail = en.getAttribute('data-acct-2fa-enable');
      postJSON('/os/vayumail/accounts/totp', { email: eemail, action: 'begin' }).then(function (res) {
        if (!res.ok || !res.body || !res.body.secret) { acctToast('Could not start 2FA setup: ' + errText(res), true); return; }
        openMailboxTotpModal(eemail, res.body);
      });
      return;
    }

    var dis = e.target.closest('[data-acct-2fa-disable]');
    if (dis) {
      var demail = dis.getAttribute('data-acct-2fa-disable');
      // The house modal, not window.confirm: it can say what turning 2FA off
      // actually costs, and it keeps the rest of the console's look.
      vpConfirm({
        title: 'Turn off two-factor authentication',
        message: 'Turn OFF two-factor authentication for ' + demail + '? After this, the password alone is enough to sign in to this mailbox.',
        confirm: 'Turn off'
      }, function () {
        postJSON('/os/vayumail/accounts/totp', { email: demail, action: 'disable' }).then(function (res) {
          if (res.ok) { acctToast('Two-factor disabled for ' + demail); acctReload(); }
          else acctToast('Update failed: ' + errText(res), true);
        });
      });
      return;
    }
  });

  // ── Message actions (Junk / Trash / Restore / Delete) ────────────────────────
  var actions = document.querySelector('[data-mail-actions]');
  if (actions) {
    var user = actions.getAttribute('data-user');
    var folder = actions.getAttribute('data-folder');
    var id = actions.getAttribute('data-id');
    // Build navigation targets from individual components with a LITERAL path
    // prefix and encodeURIComponent-escaped values — never a full URL read from
    // a DOM attribute. encodeURIComponent is a recognised sanitiser, so no
    // DOM-derived text can reach location unescaped, and the resulting URLs are
    // identical to the ones the server used to hand over.
    var backURL = '/os/vayumail/inbox?user=' + encodeURIComponent(user) + '&folder=' + encodeURIComponent(folder);
    var nextId = actions.getAttribute('data-next-id') || '';
    var nextURL = nextId
      ? '/os/vayumail/message?user=' + encodeURIComponent(user) + '&folder=' + encodeURIComponent(folder) + '&id=' + encodeURIComponent(nextId)
      : '';
    // After a message leaves the folder (move/junk/trash/delete) continue to the
    // next message if there is one, otherwise fall back to the folder list.
    var advance = function () { window.location.assign(nextURL || backURL); };

    // Move / Junk / Trash / Restore buttons.
    actions.querySelectorAll('[data-mail-move]').forEach(function (btn) {
      btn.addEventListener('click', function () {
        var target = btn.getAttribute('data-mail-move');
        btn.disabled = true;
        postJSON('/os/vayumail/message/action', { user: user, id: id, folder: folder, to: target }).then(function (res) {
          if (res.ok) { acctToast('Moved to ' + target); advance(); }
          else { btn.disabled = false; acctToast('Move failed: ' + errText(res), true); }
        });
      });
    });

    // Not junk → back to the Inbox, and the sender into contacts, so their
    // next message is not filed as junk either.
    actions.querySelectorAll('[data-mail-notjunk]').forEach(function (btn) {
      btn.addEventListener('click', function () {
        btn.disabled = true;
        postJSON('/os/vayumail/message/action', { user: user, id: id, folder: folder, notjunk: true }).then(function (res) {
          if (res.ok) { acctToast('Moved to Inbox, and the sender will not be filed as junk again'); advance(); }
          else { btn.disabled = false; acctToast('Could not move it: ' + errText(res), true); }
        });
      });
    });

    // Mark unread → drop the Seen flag and return to the folder (Gmail-style).
    actions.querySelectorAll('[data-mail-mark]').forEach(function (btn) {
      btn.addEventListener('click', function () {
        var mark = btn.getAttribute('data-mail-mark');
        postJSON('/os/vayumail/message/action', { user: user, id: id, folder: folder, mark: mark }).then(function (res) {
          if (res.ok) { acctToast(mark === 'unread' ? 'Marked unread' : 'Marked read'); window.location.assign(backURL); }
          else acctToast('Mark failed: ' + errText(res), true);
        });
      });
    });

    // Pin / unpin — flips in place (stay on the message).
    actions.querySelectorAll('[data-mail-pin]').forEach(function (btn) {
      btn.addEventListener('click', function () {
        var pin = btn.getAttribute('data-mail-pin') === '1';
        postJSON('/os/vayumail/message/action', { user: user, id: id, folder: folder, pin: pin }).then(function (res) {
          if (res.ok) {
            acctToast(pin ? 'Pinned' : 'Unpinned');
            btn.setAttribute('data-mail-pin', pin ? '0' : '1');
            // Only the words change; the icon the server drew stays.
            btn.lastChild.textContent = pin ? 'Unpin' : 'Pin';
          } else acctToast('Pin failed: ' + errText(res), true);
        });
      });
    });

    // Move-to-folder picker.
    var moveSel = actions.querySelector('[data-mail-move-select]');
    if (moveSel) {
      moveSel.addEventListener('change', function () {
        var target = moveSel.value;
        if (!target) return;
        postJSON('/os/vayumail/message/action', { user: user, id: id, folder: folder, to: target }).then(function (res) {
          if (res.ok) { acctToast('Moved to ' + target); advance(); }
          else { moveSel.value = ''; acctToast('Move failed: ' + errText(res), true); }
        });
      });
    }

    // Delete permanently.
    var del = actions.querySelector('[data-mail-delete]');
    if (del) {
      del.addEventListener('click', function () {
        vpConfirm({
          title: 'Delete this message',
          message: 'Permanently delete this message? This cannot be undone.',
          confirm: 'Delete'
        }, function () {
          del.disabled = true;
          postJSON('/os/vayumail/message/action', { user: user, id: id, folder: folder, delete: true }).then(function (res) {
            if (res.ok) { acctToast('Deleted'); advance(); }
            else { del.disabled = false; acctToast('Delete failed: ' + errText(res), true); }
          });
        });
      });
    }

    // Print the message.
    var printBtn = actions.querySelector('[data-mail-print]');
    if (printBtn) { printBtn.addEventListener('click', function () { window.print(); }); }
  }
  // ── Mailbox list: selection mode (Mail plan §7, render 04) ───────────────────
  // x, or a row's round check, starts selecting. The header becomes "N
  // selected · Select all · Done", a click on a row picks it instead of opening
  // it, and the reader column shows the picked messages with what can be done
  // to them: the folder's own #vm-selpanel template, placed beside the list.
  // Its actions are HTMX posts that include the checked rows, as the bulk bar's
  // were; each one swaps the list, which ends the selection.
  (function () {
    var split = document.querySelector('.vm-split');
    var host = null, picking = false;
    function list() { return document.getElementById('vm-inbox-list'); }
    function boxes() { return Array.prototype.slice.call(document.querySelectorAll('#vm-inbox-list [data-vm-check]')); }
    function picked() { return boxes().filter(function (c) { return c.checked; }); }
    function who(row) { var w = row.querySelector('.mx-row__who'); return w ? w.textContent.trim() : ''; }
    function sentence(names) {
      if (names.length <= 1) return names.join('');
      if (names.length <= 3) return names.slice(0, -1).join(', ') + ' and ' + names[names.length - 1];
      return names.slice(0, 2).join(', ') + ' and ' + (names.length - 2) + ' others';
    }
    function card(row) {
      var c = document.createElement('div');
      c.className = 'mx-selpanel__card';
      var a = document.createElement('strong'); a.textContent = who(row);
      var b = document.createElement('span');
      var subj = row.querySelector('.mx-row__subj-text'); b.textContent = subj ? subj.textContent : '';
      c.appendChild(a); c.appendChild(b);
      return c;
    }
    function sync() {
      var l = list();
      if (!l) return;
      var cs = picked(), n = cs.length;
      var on = n > 0 || picking;
      l.classList.toggle('is-selecting', on);
      var mainLayer = l.querySelector('.mx-head__main'), selLayer = l.querySelector('.mx-head__sel');
      if (mainLayer) mainLayer.inert = on;
      if (selLayer) selLayer.inert = !on;
      document.querySelectorAll('#vm-inbox-list .mx-row').forEach(function (r) {
        var c = r.querySelector('[data-vm-check]');
        r.classList.toggle('is-picked', !!(c && c.checked));
      });
      var count = document.querySelector('[data-vm-bulkcount]');
      if (count) count.textContent = n + ' selected';
      var all = document.querySelector('[data-vm-select-all]');
      if (all) all.textContent = n > 0 && n === boxes().length ? 'Select none' : 'Select all';
      if (!split) return;
      if (!host) { host = document.createElement('div'); host.className = 'mx-select-host'; host.hidden = true; split.appendChild(host); }
      if (n === 0) { host.hidden = true; host.textContent = ''; return; }
      if (!host.firstChild) {
        var t = document.getElementById('vm-selpanel');
        if (!t) return;
        host.appendChild(t.content.cloneNode(true));
        if (window.htmx) window.htmx.process(host);
        if (window.vpWirePops) window.vpWirePops(host);
      }
      host.hidden = false;
      var stack = host.querySelector('[data-vm-sel-stack]');
      if (stack) { stack.textContent = ''; cs.slice(0, 3).forEach(function (c) { stack.appendChild(card(c.closest('.mx-row'))); }); }
      var title = host.querySelector('[data-vm-sel-title]');
      if (title) title.textContent = n === 1 ? '1 message selected' : n + ' messages selected';
      var from = host.querySelector('[data-vm-sel-from]');
      if (from) {
        var names = [];
        cs.forEach(function (c) { var w = who(c.closest('.mx-row')); if (w && names.indexOf(w) < 0) names.push(w); });
        from.textContent = names.length ? 'From ' + sentence(names) : '';
      }
    }
    function done() {
      picking = false;
      boxes().forEach(function (c) { c.checked = false; });
      sync();
    }
    // Starting from the keyboard: x picks the row that is open or focused.
    window.vmPick = function (row) {
      var c = row && row.querySelector('[data-vm-check]');
      if (!c) return;
      picking = true;
      c.checked = !c.checked;
      sync();
    };
    window.vmSelecting = function () { var l = list(); return !!(l && l.classList.contains('is-selecting')); };
    window.vmSelectionDone = done;
    document.addEventListener('change', function (e) {
      if (e.target && e.target.matches && e.target.matches('[data-vm-check]')) sync();
    });
    document.addEventListener('click', function (e) {
      var t = e.target;
      if (!t || !t.closest) return;
      if (t.closest('[data-vm-select-all]')) {
        var on = !(picked().length && picked().length === boxes().length);
        boxes().forEach(function (c) { c.checked = on; });
        sync();
        return;
      }
      if (t.closest('[data-vm-select-done]')) { done(); return; }
    });
    // While selecting, a click on a row picks it rather than opening it. In
    // the capture phase, so HTMX never sees the click that would open it.
    document.addEventListener('click', function (e) {
      if (!window.vmSelecting()) return;
      var open = e.target && e.target.closest ? e.target.closest('#vm-inbox-list .mx-row__open') : null;
      if (!open) return;
      e.preventDefault();
      e.stopPropagation();
      var c = open.closest('.mx-row').querySelector('[data-vm-check]');
      if (c) { c.checked = !c.checked; sync(); }
    }, true);
    document.addEventListener('keydown', function (e) {
      if (e.key !== 'Escape' || !window.vmSelecting()) return;
      if (document.querySelector('[data-mx-compose-host]:not([hidden]), details.sa-pop[open]')) return;
      done();
    });
    // A swap of the list (an action, a folder, the poll) ends the selection.
    document.body.addEventListener('htmx:afterSwap', function (e) {
      if (!e.target || e.target.id !== 'vm-inbox-list') return;
      picking = false;
      if (host) host.textContent = '';
      sync();
    });
    sync();
  })();

  // A bulk action that only partly applied must not read as success. The server
  // counts per-message failures and fires this event (via HX-Trigger); the shell
  // shows one toast. Before this, every per-message error was swallowed
  // (`_ = apply(id)`), so a half-done batch looked exactly like a clean one.
  // A write that answers with a sentence rather than a redraw (Block).
  document.body.addEventListener('vm-said', function (e) {
    var d = (e && e.detail) || {};
    if (d.text) acctToast(d.text, d.warn ? 'warn' : undefined);
  });

  document.body.addEventListener('vm-inbox-result', function (e) {
    var d = (e && e.detail) || {};
    if (!d.failed) return;
    if (d.readonly) {
      acctToast('This mailbox is read-only: messages can be read here but not deleted or moved.', 'warn');
      return;
    }
    var done = d.done || 0;
    acctToast(done + ' applied, ' + d.failed + ' failed — those messages may already have been moved or deleted.', 'warn');
  });

  // One-time app-password reveal: copy and save. The value is already on screen,
  // so this only saves retyping a 20-character secret onto another device — and
  // it reports a failed clipboard write honestly (plain-http Tor consoles have no
  // navigator.clipboard at all).
  document.addEventListener('click', function (e) {
    if (!e.target || !e.target.closest) return;
    var cp = e.target.closest('[data-apppw-copy]');
    if (cp) {
      var v = cp.getAttribute('data-apppw-copy') || '';
      var was = cp.textContent;
      var done = function () { cp.textContent = 'Copied'; setTimeout(function () { cp.textContent = was; }, 1400); };
      var fail = function () { cp.textContent = 'Copy failed — select it manually'; setTimeout(function () { cp.textContent = was; }, 2600); };
      if (navigator.clipboard && navigator.clipboard.writeText) {
        navigator.clipboard.writeText(v).then(done, function () { fail(); });
      } else { fail(); }
      return;
    }
    var sv = e.target.closest('[data-apppw-save]');
    if (sv) {
      var text = 'VayuMail app password\r\n' +
        'Mailbox:  ' + (sv.getAttribute('data-apppw-email') || '') + '\r\n' +
        'Label:    ' + (sv.getAttribute('data-apppw-label') || '') + '\r\n' +
        'Password: ' + (sv.getAttribute('data-apppw-save') || '') + '\r\n\r\n' +
        'The dashes are optional when signing in.\r\n' +
        'This password is shown only once — store it somewhere safe and revoke it if it leaks.\r\n';
      var blob = new Blob([text], { type: 'text/plain' });
      var link = document.createElement('a');
      link.href = URL.createObjectURL(blob);
      link.download = 'vayumail-app-password.txt';
      document.body.appendChild(link);
      link.click();
      document.body.removeChild(link);
      setTimeout(function () { URL.revokeObjectURL(link.href); }, 4000);
      return;
    }
  });

  // A new app password is made in its sheet; the answer swaps the list beside
  // it, which shows the password once, so the sheet steps out of the way.
  document.addEventListener('htmx:afterRequest', function (e) {
    var f = e.target && e.target.closest ? e.target.closest('form[data-apppw-create]') : null;
    if (!f || !e.detail || !e.detail.successful) return;
    f.reset();
    var sheet = f.closest('dialog');
    if (sheet && sheet.open) sheet.close();
  });

  // ── Message raw-source toggle ────────────────────────────────────────────────
  var rawBtn = document.querySelector('[data-mail-raw-toggle]');
  var rawPre = document.querySelector('[data-mail-raw]');
  if (rawBtn && rawPre) {
    rawBtn.addEventListener('click', function () {
      if (rawPre.hasAttribute('hidden')) {
        rawPre.removeAttribute('hidden');
        rawBtn.textContent = 'Hide raw source';
      } else {
        rawPre.setAttribute('hidden', '');
        rawBtn.textContent = 'View raw source';
      }
    });
  }

  // ── Keyboard shortcuts + help overlay ────────────────────────────────────────
  // Gmail/Superhuman-style single-key navigation. Shortcuts are ignored while
  // typing in a field. "?" opens a help overlay (native popover when available,
  // a positioned fallback otherwise). All CSP-safe — no inline handlers/styles.
  (function () {
    function typing() {
      var el = document.activeElement;
      return el && (el.tagName === 'INPUT' || el.tagName === 'TEXTAREA' || el.tagName === 'SELECT' || el.isContentEditable);
    }
    var help = null;
    function buildHelp() {
      if (help) return help;
      help = document.createElement('div');
      help.className = 'vm-help';
      help.setAttribute('role', 'dialog');
      help.setAttribute('aria-label', 'Keyboard shortcuts');
      try { help.setAttribute('popover', 'auto'); } catch (e) { /* older browser */ }
      // The keys of Mail plan §6.
      var pairs = [
        ['Older · newer message', 'j · k'], ['Open', 'Enter · o'],
        ['Reply · reply all · forward', 'r · a · f'], ['Compose', 'c'], ['Search', '/'],
        ['Select the row', 'x'], ['Archive', 'e'], ['Delete', '#'], ['Snooze', 's'], ['Pin', 'p'],
        ['Mark unread', 'u'], ['Move to…', 'v'], ['Junk · in Junk, not junk', '!'],
        ['Full width · the list again', 'w'], ['Go to Inbox · Sent · Drafts', 'g then i · s · d'], ['Switch mailbox', '⌘⇧M · Ctrl+Shift+M'], ['These keys', '?']
      ];
      var h = '<div class="vm-help-title">Keyboard shortcuts</div><div class="vm-help-grid">';
      pairs.forEach(function (p) { h += '<span></span><span class="vm-kbd"></span>'; });
      help.innerHTML = h;
      var spans = help.querySelectorAll('.vm-help-grid > span');
      pairs.forEach(function (p, i) { spans[i * 2].textContent = p[0]; spans[i * 2 + 1].textContent = p[1]; });
      var foot = document.createElement('div');
      foot.className = 'vm-help-foot muted text-xs';
      foot.textContent = 'Press Esc or ? to close';
      help.appendChild(foot);
      (document.querySelector('.vp-os') || document.body).appendChild(help);
      return help;
    }
    function helpOpen() { return help && ((help.matches && help.matches(':popover-open')) || help.classList.contains('vm-help--open')); }
    function toggleHelp() {
      var el = buildHelp();
      if (helpOpen()) { if (el.hidePopover) { try { el.hidePopover(); return; } catch (e) {} } el.classList.remove('vm-help--open'); return; }
      if (typeof el.showPopover === 'function') { try { el.showPopover(); return; } catch (e) {} }
      el.classList.add('vm-help--open');
    }

    var focusIdx = -1;
    // The rows on screen: while a search shows, its results, not the list.
    function rows() { return Array.prototype.slice.call(document.querySelectorAll('#vm-inbox-list .mx-row')).filter(function (r) { return r.offsetParent !== null; }); }
    function paint() {
      var rs = rows();
      rs.forEach(function (r, i) { if (i === focusIdx) r.classList.add('vm-focus'); else r.classList.remove('vm-focus'); });
      if (focusIdx >= 0 && rs[focusIdx]) rs[focusIdx].scrollIntoView({ block: 'nearest' });
    }
    // Where j and k start: the open message while its row is on screen, else
    // the first row on screen, so the first key never scrolls the list away
    // from what is being looked at (the newest message opens on its own, and
    // the list may since have been scrolled far past it).
    function startIdx(rs) {
      var list = document.getElementById('vm-inbox-list');
      var box = list ? list.getBoundingClientRect() : { top: 0, bottom: window.innerHeight };
      var top = box.top + 110;
      for (var i = 0; i < rs.length; i++) {
        if (!rs[i].classList.contains('vm-active')) continue;
        var r = rs[i].getBoundingClientRect();
        if (r.top >= top && r.bottom <= box.bottom) return i;
        break;
      }
      for (var k = 0; k < rs.length; k++) if (rs[k].getBoundingClientRect().top >= top) return k;
      return 0;
    }
    function move(delta) {
      var rs = rows(); if (!rs.length) return;
      if (focusIdx < 0) { focusIdx = startIdx(rs); if (rs[focusIdx].classList.contains('vm-active')) focusIdx += delta; }
      else focusIdx += delta;
      if (focusIdx < 0) focusIdx = 0;
      if (focusIdx > rs.length - 1) focusIdx = rs.length - 1;
      paint();
    }
    function focusedRow() { var rs = rows(); return focusIdx >= 0 ? rs[focusIdx] : null; }
    function clickIn(root, sel) { if (!root) return; var el = root.querySelector(sel); if (el) el.click(); }
    // x picks the focused row, or the open one, and starts selecting.
    function pickRow() {
      var row = focusedRow() || document.querySelector('#vm-inbox-list .mx-row.vm-active');
      if (row && window.vmPick) window.vmPick(row);
    }
    // While selecting, the keys act on what is picked, through the selection
    // panel's own controls (the ones a pointer uses).
    function inJunk() {
      var f = document.querySelector('#vm-inbox-list input[name="folder"][data-vm-scope]');
      return !!f && f.value.toLowerCase() === 'junk';
    }
    function selVals(fragment) {
      var btns = document.querySelectorAll('.mx-select-host button[hx-vals]');
      for (var i = 0; i < btns.length; i++) {
        if ((btns[i].getAttribute('hx-vals') || '').indexOf(fragment) >= 0) { btns[i].click(); return; }
      }
    }
    function openMenu(root, label) {
      var menus = root ? root.querySelectorAll('details.sa-pop') : [];
      for (var i = 0; i < menus.length; i++) {
        var sm = menus[i].querySelector('summary');
        if (sm && ((sm.getAttribute('aria-label') || sm.textContent).indexOf(label) === 0)) { menus[i].dataset.kbd = '1'; menus[i].open = true; return; }
      }
    }
    function goFolder(name) {
      var links = document.querySelectorAll('#vm-folders a');
      for (var i = 0; i < links.length; i++) {
        var lab = links[i].querySelector('.sa-appside__label');
        if (lab && lab.textContent.trim() === name) { links[i].click(); return; }
      }
    }
    var gAt = 0;

    document.addEventListener('keydown', function (e) {
      if (e.altKey || e.ctrlKey || e.metaKey) return;
      if (e.key === 'Escape' && helpOpen()) { toggleHelp(); return; }
      if (typing()) return;
      // The composer's buttons hold focus too; a key pressed there is not
      // meant for the list behind it.
      var at = document.activeElement;
      if (at && at.closest && at.closest('form[data-mail-compose]')) return;
      if (e.key === '?') { e.preventDefault(); toggleHelp(); return; }
      // w widens the open message, and w or Escape comes back to the list.
      var sc = window.vmScreens;
      if (sc && (e.key === 'w' || e.key === 'Escape') && document.querySelector('#vm-readpane .mx-reader')) {
        if (e.key === 'Escape' && (!sc.open() || document.querySelector('details.sa-pop[open]'))) return;
        if (e.key === 'w' && !sc.open()) { e.preventDefault(); sc.widen(); return; }
        if (sc.open()) { e.preventDefault(); sc.toList(); return; }
      }
      if (e.key === 'c') {
        // Keep the mailbox context: an admin reading someone's mailbox must not
        // silently start composing as postmaster instead.
        var mu = window.location.search.match(/[?&]user=([^&]*)/);
        var who = '';
        if (mu && mu[1]) {
          try { who = encodeURIComponent(decodeURIComponent(mu[1])); } catch (err) { who = encodeURIComponent(mu[1]); }
        }
        var to = '/os/vayumail/compose' + (who ? '?user=' + who : '');
        if (window.vpComposeSheet) window.vpComposeSheet(to, null); else window.location.href = to;
        return;
      }
      if (e.key === '/') {
        var s = document.querySelector('input[type="search"][name="q"]');
        if (s) { e.preventDefault(); s.focus(); } else { window.location.href = '/os/vayumail/search'; }
        return;
      }
      // g then i, s or d goes to the Inbox, Sent or Drafts.
      if (gAt && Date.now() - gAt < 1500) {
        gAt = 0;
        var to = { i: 'Inbox', s: 'Sent', d: 'Drafts' }[e.key];
        if (to) { e.preventDefault(); goFolder(to); return; }
      }
      if (e.key === 'g' && document.getElementById('vm-folders')) { gAt = Date.now(); return; }
      if (e.key === 'x' && document.getElementById('vm-inbox-list')) { e.preventDefault(); pickRow(); return; }
      if (window.vmSelecting && window.vmSelecting()) {
        var host = document.querySelector('.mx-select-host');
        switch (e.key) {
          case 'e': selVals('"to":"Archive"'); return;
          case '!': selVals(inJunk() ? '"action":"notjunk"' : '"to":"Junk"'); return;
          case '#': selVals('"delete"'); return;
          case 'u': selVals('"mark":"unread"'); return;
          case 'p': selVals('"pin"'); return;
          case 's': e.preventDefault(); openMenu(host, 'Snooze'); return;
          case 'v': e.preventDefault(); openMenu(host, 'Move to'); return;
        }
      }
      var reader = document.querySelector('.mx-rtools');
      // Beside the list, j and k walk the list (and open what they reach);
      // the reader's own older/newer links are for the message page alone.
      var beside = !!document.getElementById('vm-inbox-list');
      if (reader && !(beside && (e.key === 'j' || e.key === 'k'))) {
        switch (e.key) {
          case 'r': clickIn(reader, '[aria-label="Reply"]'); return;
          case 'a': clickIn(reader, '[aria-label="Reply all"]'); return;
          case 'f': clickIn(reader, '[aria-label="Forward"]'); return;
          case 'u': clickIn(reader, '[data-mail-mark="unread"], button[hx-vals*="unread"]'); return;
          case '#': clickIn(reader, '[aria-label="Move to Trash"], [aria-label="Delete for good"]'); return;
          case 'e': clickIn(reader, '[aria-label="Archive"]'); return;
          case '!': clickIn(reader, '[aria-label="Junk"], [aria-label="Not junk"]'); return;
          case 'p': clickIn(reader, '[aria-label="Pin"], [aria-label="Unpin"]'); return;
          case 's': e.preventDefault(); openMenu(reader, 'Snooze'); return;
          case 'v': e.preventDefault(); openMenu(reader, 'Move to'); return;
          case 'j': clickIn(reader, '[aria-label="Older message"]'); return;
          case 'k': clickIn(reader, '[aria-label="Newer message"]'); return;
        }
        return;
      }
      if (document.getElementById('vm-inbox-list')) {
        switch (e.key) {
          case 'j': e.preventDefault(); travel = 'older'; move(1); clickIn(focusedRow(), '.mx-row__open'); return;
          case 'k': e.preventDefault(); travel = 'newer'; move(-1); clickIn(focusedRow(), '.mx-row__open'); return;
          case 'o': case 'Enter': clickIn(focusedRow(), '.mx-row__open'); return;
        }
      }
    });
    document.body.addEventListener('htmx:afterSwap', function (e) {
      if (e.target && e.target.id === 'vm-inbox-list') focusIdx = -1;
    });
  })();

  // ── PGP public key: copy to clipboard ──────────────────────────────────────
  // Delegated from document so it keeps working after an HTMX swap replaces the
  // whole accounts list (every account action re-renders #vm-accounts-list, so a
  // handler bound to the button itself would be discarded on the first save).
  //
  // Only ever touches the PUBLIC key: the textarea it reads is rendered from
  // GetPublicKey, and no private-key material exists anywhere on this page.
  (function () {
    document.addEventListener('click', function (e) {
      var btn = e.target && e.target.closest ? e.target.closest('[data-pgp-copy]') : null;
      if (!btn) return;
      var wrap = btn.closest('.vm-pgp');
      var ta = wrap ? wrap.querySelector('[data-pgp-armor]') : null;
      if (!ta) return;
      var text = ta.value || '';
      var done = function (msg) {
        var was = btn.textContent;
        btn.textContent = msg;
        setTimeout(function () { btn.textContent = was; }, 2000);
      };
      if (navigator.clipboard && navigator.clipboard.writeText) {
        navigator.clipboard.writeText(text).then(function () {
          done('Copied');
        }).catch(function () {
          // The clipboard API needs a secure context and a permission some
          // browsers refuse. Select the text so Ctrl-C still works rather than
          // leaving a button that silently did nothing.
          ta.focus(); ta.select();
          done('Press Ctrl-C');
        });
      } else {
        ta.focus(); ta.select();
        done('Press Ctrl-C');
      }
    });
  })();
})();
