/* VayuPress — Trending & pinned posts widget.
 *
 * Hydrates every [data-vayu-trending] section (homepage + bottom of each post)
 * from the public /api/trending endpoint. Sovereign · zero-CDN · strict-CSP:
 * no inline styles, no eval, all DOM built with createElement/textContent so a
 * malicious title can never become markup. Lists are served as JSON (the pages
 * themselves are cached), so the widget is always fresh without cache churn.
 */
(function () {
  'use strict';

  var sections = Array.prototype.slice.call(
    document.querySelectorAll('[data-vayu-trending]')
  );
  if (!sections.length) return;

  fetch('/api/trending', { headers: { Accept: 'application/json' } })
    .then(function (r) { return r.ok ? r.json() : null; })
    .then(function (data) {
      if (!data || !data.enabled) return;
      var windows = data.windows || {};
      var pinned = data.pinned || [];
      var has7 = (windows['7'] || []).length > 0;
      var has30 = (windows['30'] || []).length > 0;
      if (!pinned.length && !has7 && !has30) return; // nothing to show
      sections.forEach(function (section) {
        renderInto(section, pinned, windows);
        section.removeAttribute('hidden');
      });
    })
    .catch(function () { /* network/parse error — leave the section hidden */ });

  // Build a single post card. rank > 0 renders a position number; isPin renders
  // a pin glyph instead.
  function card(item, rank, isPin) {
    var a = document.createElement('a');
    a.className = 'vayu-trending-card';
    a.href = '/' + item.slug;

    var badge = document.createElement('span');
    if (isPin) {
      badge.className = 'vayu-trending-pin';
      badge.textContent = '\uD83D\uDCCC'; // 📌
      badge.setAttribute('aria-hidden', 'true');
    } else {
      badge.className = 'vayu-trending-rank';
      badge.textContent = String(rank);
      badge.setAttribute('aria-hidden', 'true');
    }
    a.appendChild(badge);

    if (item.image) {
      var img = document.createElement('img');
      img.className = 'vayu-trending-thumb';
      img.src = item.image;
      img.alt = '';
      img.loading = 'lazy';
      img.decoding = 'async';
      a.appendChild(img);
    }

    var title = document.createElement('span');
    title.className = 'vayu-trending-title';
    title.textContent = item.title || item.slug;
    a.appendChild(title);
    return a;
  }

  function listEl(items, isPin) {
    var list = document.createElement('div');
    list.className = 'vayu-trending-list';
    items.forEach(function (it, i) { list.appendChild(card(it, i + 1, isPin)); });
    return list;
  }

  function group(labelText, icon) {
    var g = document.createElement('div');
    g.className = 'vayu-trending-group';
    var head = document.createElement('div');
    head.className = 'vayu-trending-head';
    var label = document.createElement('span');
    label.className = 'vayu-trending-label';
    label.textContent = icon + ' ' + labelText;
    head.appendChild(label);
    g.appendChild(head);
    return { group: g, head: head };
  }

  function renderInto(section, pinned, windows) {
    section.textContent = ''; // idempotent: clear any prior render

    // ── Pinned ──
    if (pinned.length) {
      var p = group('Pinned', '\uD83D\uDCCC');
      p.group.appendChild(listEl(pinned, true));
      section.appendChild(p.group);
    }

    // ── Trending (7 / 30 day tabs) ──
    var win7 = windows['7'] || [];
    var win30 = windows['30'] || [];
    if (!win7.length && !win30.length) return;

    var t = group('Trending', '\uD83D\uDD25'); // 🔥
    var tabs = document.createElement('div');
    tabs.className = 'vayu-trending-tabs';
    tabs.setAttribute('role', 'tablist');

    var listWrap = document.createElement('div');

    function show(days) {
      var items = days === 30 ? win30 : win7;
      listWrap.textContent = '';
      listWrap.appendChild(listEl(items, false));
      Array.prototype.forEach.call(tabs.children, function (b) {
        b.setAttribute('aria-selected', b.getAttribute('data-w') === String(days) ? 'true' : 'false');
      });
    }

    function tab(days, text, enabled) {
      var b = document.createElement('button');
      b.type = 'button';
      b.className = 'vayu-trending-tab';
      b.setAttribute('role', 'tab');
      b.setAttribute('data-w', String(days));
      b.textContent = text;
      if (!enabled) { b.disabled = true; }
      b.addEventListener('click', function () { if (!b.disabled) show(days); });
      return b;
    }

    tabs.appendChild(tab(7, 'Last 7 days', win7.length > 0));
    tabs.appendChild(tab(30, 'Last 30 days', win30.length > 0));
    t.head.appendChild(tabs);

    t.group.appendChild(listWrap);
    section.appendChild(t.group);

    // Default to the window that actually has data (prefer 7 days).
    show(win7.length ? 7 : 30);
  }
})();
