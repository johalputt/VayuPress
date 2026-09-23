/* admin-os-site-editor.js — the site editor (ADR-0161).
 *
 * Edits a site document: pages, each an ordered list of typed sections. Every
 * change is saved as a draft after a short pause; Publish makes it the live
 * site; History reads, compares and restores any earlier publish. The server
 * validates everything and answers a refusal with the path of the field that
 * failed (pages[1].sections[0].images[2].alt) — every input here carries that
 * path in data-path, so the refusal lands on the field it is about.
 *
 * DOM is built with createElement/textContent only: no document text is ever
 * parsed as markup, in the console any more than on the site. */
(function () {
  'use strict';
  var root = document.getElementById('site-editor');
  if (!root) return;
  var BASE = root.getAttribute('data-base');

  function csrf() {
    var m = document.cookie.match(/(?:^|;\s*)vp_csrf=([^;]+)/);
    return m ? decodeURIComponent(m[1]) : '';
  }
  function api(method, path, body) {
    var opts = { method: method, credentials: 'same-origin', headers: { 'X-CSRF-Token': csrf() } };
    if (body !== undefined) {
      opts.headers['Content-Type'] = 'application/json';
      opts.body = JSON.stringify(body);
    }
    return fetch(BASE + path, opts).then(function (r) {
      return r.json().catch(function () { return null; }).then(function (j) {
        return { ok: r.ok, status: r.status, j: j };
      });
    });
  }
  function el(tag, cls, text) {
    var e = document.createElement(tag);
    if (cls) e.className = cls;
    if (text !== undefined) e.textContent = text;
    return e;
  }
  function btn(label, cls, onClick) {
    var b = el('button', 'btn btn--sm ' + (cls || 'btn--ghost'), label);
    b.type = 'button';
    b.addEventListener('click', onClick);
    return b;
  }

  var state = { doc: null, page: 0, dirty: false, saving: false, revisions: [], source: '', choices: {} };
  var statusEl = document.getElementById('se-status');
  var errorEl = document.getElementById('se-error');
  var pagesEl = document.getElementById('se-pages');
  var siteEl = document.getElementById('se-site');
  var sectionsEl = document.getElementById('se-sections');
  var frame = document.getElementById('se-preview');
  var historyEl = document.getElementById('se-history');

  function say(msg) { if (statusEl) statusEl.textContent = msg; }
  function showError(msg, path) {
    document.querySelectorAll('[data-path].se-bad').forEach(function (n) { n.classList.remove('se-bad'); });
    if (!errorEl) return;
    errorEl.textContent = msg || '';
    errorEl.hidden = !msg;
    if (!path) return;
    // A refusal names a field on some page; show that page and mark the field.
    var m = /^pages\[(\d+)\]/.exec(path);
    if (m && +m[1] !== state.page) { state.page = +m[1]; render(); }
    var field = document.querySelector('[data-path="' + path.replace(/"/g, '') + '"]');
    if (field) { field.classList.add('se-bad'); field.focus(); }
  }

  // ── Model helpers ─────────────────────────────────────────────────────────
  var KINDS = {
    hero: 'Header (title, tagline, button, picture)',
    text: 'Text',
    items: 'List of offerings (menu, services, prices)',
    gallery: 'Gallery',
    contact: 'Contact details and form'
  };
  function newSection(kind, page) {
    var n = 1, id;
    do { id = kind + (n > 1 ? '-' + n : ''); n++; } while (page.sections.some(function (s) { return s.id === id; }));
    var s = { id: id, kind: kind };
    if (kind === 'items') s.items = [{ title: '' }];
    if (kind === 'gallery') s.images = [];
    if (kind === 'text' || kind === 'items' || kind === 'gallery' || kind === 'contact') s.heading = KINDS[kind].split(' ')[0];
    return s;
  }
  // Empty optional values are dropped before sending, so a cleared field is
  // "not set" rather than an empty string the validator then reads as present.
  function clean(v) {
    if (Array.isArray(v)) return v.map(clean);
    if (v && typeof v === 'object') {
      var o = {};
      Object.keys(v).forEach(function (k) {
        var c = clean(v[k]);
        if (c === '' || c === false || c === null || c === undefined) return;
        if (k === 'image' && (!c.src)) return;
        o[k] = c;
      });
      return o;
    }
    return v;
  }
  function payload() {
    var d = clean(state.doc);
    d.v = 1;
    d.name = state.doc.name || '';
    d.pages = state.doc.pages.map(function (p) {
      var cp = clean(p);
      cp.slug = p.slug || '';
      cp.sections = (p.sections || []).map(clean);
      return cp;
    });
    return d;
  }

  // ── Saving ────────────────────────────────────────────────────────────────
  var timer = null;
  function changed() {
    state.dirty = true;
    say('Unsaved changes…');
    clearTimeout(timer);
    timer = setTimeout(saveDraft, 1200);
  }
  function saveDraft() {
    if (state.saving) { timer = setTimeout(saveDraft, 600); return Promise.resolve(false); }
    state.saving = true;
    return api('POST', '/draft', { doc: payload() }).then(function (r) {
      state.saving = false;
      if (!r.ok) {
        var e = (r.j && r.j.error) || {};
        say('Draft not saved');
        showError(e.message || ('The server answered ' + r.status), e.path);
        return false;
      }
      state.dirty = false;
      showError('');
      say('Draft saved ' + new Date().toLocaleTimeString());
      reloadPreview();
      return true;
    }, function () { state.saving = false; say('Draft not saved — connection lost; retrying'); timer = setTimeout(saveDraft, 3000); return false; });
  }
  function publish() {
    clearTimeout(timer);
    say('Publishing…');
    api('POST', '/publish', { doc: payload() }).then(function (r) {
      if (!r.ok) {
        var e = (r.j && r.j.error) || {};
        say('Not published — the live site is unchanged');
        showError(e.message || ('The server answered ' + r.status), e.path);
        return;
      }
      state.dirty = false;
      showError('');
      if (window.vpToast) window.vpToast('Site published', 'ok');
      load(true, 'Published ✓ — revision ' + r.j.revision + ' is live');
    });
  }

  // ── Preview ───────────────────────────────────────────────────────────────
  function reloadPreview() {
    if (!frame || !state.doc) return;
    var p = state.doc.pages[state.page];
    frame.src = BASE + '/preview?page=' + encodeURIComponent(p ? p.slug : '') + '&t=' + Date.now();
  }
  document.querySelectorAll('[data-se-device]').forEach(function (b) {
    b.addEventListener('click', function () {
      var wrap = document.getElementById('se-preview-wrap');
      if (wrap) wrap.setAttribute('data-device', b.getAttribute('data-se-device'));
    });
  });

  // ── Fields ────────────────────────────────────────────────────────────────
  function field(label, obj, key, path, opts) {
    opts = opts || {};
    var wrap = el('label', 'se-field');
    wrap.appendChild(el('span', 'se-label', label));
    var input;
    if (opts.checkbox) {
      input = el('input');
      input.type = 'checkbox';
      input.checked = !!obj[key];
      input.addEventListener('change', function () { obj[key] = input.checked; changed(); });
      wrap.classList.add('se-check');
    } else {
      input = el(opts.multi ? 'textarea' : 'input', 'input');
      if (!opts.multi) input.type = 'text';
      if (opts.multi) input.rows = opts.rows || 3;
      input.value = obj[key] || '';
      if (opts.placeholder) input.placeholder = opts.placeholder;
      if (opts.max) input.maxLength = opts.max;
      input.addEventListener('input', function () { obj[key] = input.value; changed(); });
    }
    input.setAttribute('data-path', path ? path + '.' + key : key);
    wrap.appendChild(input);
    if (opts.hint) wrap.appendChild(el('span', 'se-hint', opts.hint));
    return wrap;
  }

  // choice is a select over a closed list the server sent; '' keeps the
  // design's own.
  function choice(label, obj, key, path, options, emptyLabel) {
    var wrap = el('label', 'se-field');
    wrap.appendChild(el('span', 'se-label', label));
    var sel = el('select', 'input');
    if (emptyLabel) { var o0 = el('option', '', emptyLabel); o0.value = ''; sel.appendChild(o0); }
    (options || []).forEach(function (v) { var o = el('option', '', v.charAt(0).toUpperCase() + v.slice(1)); o.value = v; sel.appendChild(o); });
    sel.value = obj[key] || '';
    sel.setAttribute('data-path', path ? path + '.' + key : key);
    sel.addEventListener('change', function () { obj[key] = sel.value; changed(); });
    wrap.appendChild(sel);
    return wrap;
  }

  // The brand colour: a picker and its #rrggbb, with a way back to the
  // design's own colour (a colour input cannot be empty).
  function accentField(st) {
    var wrap = el('div', 'se-field');
    wrap.appendChild(el('span', 'se-label', 'Brand colour'));
    var row = el('div', 'se-row');
    var pick = el('input');
    pick.type = 'color';
    pick.value = st.accent || '#0f766e';
    var hex = el('input', 'input');
    hex.type = 'text';
    hex.placeholder = 'The design\'s own';
    hex.value = st.accent || '';
    hex.maxLength = 7;
    hex.setAttribute('data-path', 'style.accent');
    pick.addEventListener('input', function () { st.accent = pick.value; hex.value = pick.value; changed(); });
    hex.addEventListener('input', function () { st.accent = hex.value.trim(); if (/^#[0-9a-fA-F]{6}$/.test(st.accent)) pick.value = st.accent; changed(); });
    row.appendChild(pick);
    row.appendChild(hex);
    row.appendChild(btn('Use the design\'s', 'btn--ghost', function () { st.accent = ''; hex.value = ''; changed(); }));
    wrap.appendChild(row);
    wrap.appendChild(el('span', 'se-hint', 'Dark enough for white text on a button; a lighter shade of it is used in dark mode automatically.'));
    return wrap;
  }

  function imageFields(img, path, needAlt, onRemove) {
    var box = el('div', 'se-image');
    var thumb = el('img', 'se-thumb');
    thumb.alt = '';
    function refreshThumb() { thumb.hidden = !img.src; if (img.src) thumb.src = img.src; }
    refreshThumb();
    box.appendChild(thumb);
    var fields = el('div', 'se-image-fields');
    var src = field('Picture address', img, 'src', path, { placeholder: '/media/… or https://…' });
    src.querySelector('input').addEventListener('input', refreshThumb);
    fields.appendChild(src);
    fields.appendChild(field(needAlt ? 'Describe the picture (required)' : 'Describe the picture (leave empty if decorative)',
      img, 'alt', path, { max: 300 }));
    var row = el('div', 'se-row');
    row.appendChild(btn('Choose from Media…', 'btn--ghost', function () {
      pickMedia(function (item) {
        img.src = item.url;
        // The library already knows what the picture shows; reuse it rather
        // than asking twice.
        if (!img.alt && item.alt) img.alt = item.alt;
        changed();
        render();
      });
    }));
    if (onRemove) row.appendChild(btn('Remove picture', 'btn--ghost', onRemove));
    fields.appendChild(row);
    box.appendChild(fields);
    return box;
  }

  function sectionBody(s, path) {
    var b = el('div', 'se-section-body');
    b.appendChild(field('In the menu as', s, 'nav', path, { placeholder: 'Leave empty to keep it out of the menu', max: 40 }));
    b.appendChild(field('Anchor', s, 'id', path, { hint: 'The part after # in a link to this section. Lowercase letters, digits and hyphens.' }));
    var layouts = (state.choices.variants || {})[s.kind] || [];
    if (layouts.length > 1) b.appendChild(choice('Layout', s, 'variant', path, layouts));
    switch (s.kind) {
      case 'hero':
        b.appendChild(field('Title', s, 'heading', path, { placeholder: 'Defaults to the site name' }));
        b.appendChild(field('Small label above the title', s, 'eyebrow', path, { placeholder: 'Defaults to the design\'s label', max: 80 }));
        b.appendChild(field('Tagline', s, 'body', path, { max: 300 }));
        b.appendChild(field('Button label', s, 'cta', path, { max: 80 }));
        b.appendChild(field('Button link', s, 'cta_link', path, { placeholder: '#contact, /menu, https://…, mailto:, tel:' }));
        if (s.image) {
          b.appendChild(imageFields(s.image, path + '.image', false, function () { delete s.image; changed(); render(); }));
        } else {
          b.appendChild(btn('Add a picture', 'btn--ghost', function () { s.image = { src: '' }; render(); }));
        }
        break;
      case 'text':
        b.appendChild(field('Heading', s, 'heading', path));
        b.appendChild(field('Text', s, 'body', path, { multi: true, rows: 6, hint: 'Each line becomes a paragraph.' }));
        break;
      case 'items':
        b.appendChild(field('Heading', s, 'heading', path));
        s.items = s.items || [];
        s.items.forEach(function (it, k) {
          var ip = path + '.items[' + k + ']';
          var card = el('div', 'se-item');
          card.appendChild(field('Name', it, 'title', ip));
          card.appendChild(field('Description', it, 'desc', ip, { multi: true, rows: 2 }));
          card.appendChild(field('Price', it, 'price', ip, { max: 40 }));
          card.appendChild(moveControls(s.items, k));
          card.appendChild(btn('Remove', 'btn--ghost', function () { s.items.splice(k, 1); changed(); render(); }));
          b.appendChild(card);
        });
        b.appendChild(btn('Add an offering', 'btn--ghost', function () { s.items.push({ title: '' }); render(); }));
        break;
      case 'gallery':
        b.appendChild(field('Heading', s, 'heading', path));
        s.images = s.images || [];
        s.images.forEach(function (img, k) {
          b.appendChild(imageFields(img, path + '.images[' + k + ']', true, function () { s.images.splice(k, 1); changed(); render(); }));
        });
        b.appendChild(btn('Add a picture', 'btn--ghost', function () { s.images.push({ src: '', alt: '' }); render(); }));
        break;
      case 'contact':
        b.appendChild(field('Heading', s, 'heading', path));
        b.appendChild(field('Phone', s, 'phone', path));
        b.appendChild(field('Email', s, 'email', path, { hint: 'Messages sent through the form go here.' }));
        b.appendChild(field('Address', s, 'address', path, { multi: true, rows: 3 }));
        b.appendChild(field('Opening hours', s, 'hours', path, { multi: true, rows: 3 }));
        b.appendChild(field('Show a message form', s, 'form', path, { checkbox: true }));
        break;
    }
    return b;
  }

  function moveControls(list, i) {
    var row = el('span', 'se-move');
    var up = btn('↑', 'btn--ghost', function () { var t = list[i - 1]; list[i - 1] = list[i]; list[i] = t; changed(); render(); });
    up.disabled = i === 0;
    up.setAttribute('aria-label', 'Move up');
    var down = btn('↓', 'btn--ghost', function () { var t = list[i + 1]; list[i + 1] = list[i]; list[i] = t; changed(); render(); });
    down.disabled = i === list.length - 1;
    down.setAttribute('aria-label', 'Move down');
    row.appendChild(up);
    row.appendChild(down);
    return row;
  }

  // ── Rendering the editor ──────────────────────────────────────────────────
  var open = {};
  function render() {
    var d = state.doc;
    if (!d) return;
    if (state.page >= d.pages.length) state.page = 0;

    siteEl.textContent = '';
    siteEl.appendChild(field('Business name', d, 'name', '', { max: 120 }));
    siteEl.appendChild(field('Link the blog from the menu and footer', d, 'show_blog', '', { checkbox: true }));
    d.style = d.style || {};
    siteEl.appendChild(accentField(d.style));
    siteEl.appendChild(choice('Typeface', d.style, 'font', 'style', state.choices.fonts, 'The design\'s own'));
    siteEl.appendChild(choice('Corners', d.style, 'corners', 'style', state.choices.corners, 'The design\'s own'));

    pagesEl.textContent = '';
    d.pages.forEach(function (p, i) {
      var tab = btn(p.slug === '' ? 'Home' : (p.title || '/' + p.slug), i === state.page ? 'btn--primary' : 'btn--ghost', function () {
        state.page = i; render(); reloadPreview();
      });
      tab.setAttribute('aria-pressed', i === state.page ? 'true' : 'false');
      pagesEl.appendChild(tab);
    });
    pagesEl.appendChild(btn('+ Page', 'btn--ghost', function () {
      if (d.pages.length >= 20) { showError('A site can have at most 20 pages.'); return; }
      var n = d.pages.length, slug;
      do { slug = 'page-' + n; n++; } while (d.pages.some(function (p) { return p.slug === slug; }));
      d.pages.push({ slug: slug, title: 'New page', in_nav: true, sections: [newSection('text', { sections: [] })] });
      state.page = d.pages.length - 1;
      changed(); render();
    }));

    var p = d.pages[state.page];
    var pp = 'pages[' + state.page + ']';
    sectionsEl.textContent = '';
    var pageBox = el('div', 'card se-page');
    pageBox.appendChild(el('div', 'settings-block-title', p.slug === '' ? 'Home page' : 'Page'));
    if (p.slug !== '') {
      pageBox.appendChild(field('Address', p, 'slug', pp, { hint: 'The page is served at /' + p.slug + '. Lowercase letters, digits and hyphens.' }));
      pageBox.appendChild(field('In the menu', p, 'in_nav', pp, { checkbox: true }));
    }
    pageBox.appendChild(field('Title', p, 'title', pp, { max: 120, placeholder: p.slug === '' ? 'Defaults to the business name and tagline' : '' }));
    pageBox.appendChild(field('Description for search engines and link previews', p, 'description', pp, { multi: true, rows: 2, max: 300 }));
    if (p.slug !== '') {
      pageBox.appendChild(btn('Delete this page', 'btn--ghost', function () {
        var go = function () { d.pages.splice(state.page, 1); state.page = 0; changed(); render(); reloadPreview(); };
        if (window.vpConfirm) window.vpConfirm({ title: 'Delete page', message: 'Delete “' + (p.title || p.slug) + '”? Versions you already published stay in History.', confirm: 'Delete' }, go);
        else go();
      }));
    }
    sectionsEl.appendChild(pageBox);

    p.sections.forEach(function (s, j) {
      var sp = pp + '.sections[' + j + ']';
      var card = el('div', 'card se-section');
      var head = el('div', 'se-section-head');
      var key = state.page + ':' + j;
      var toggle = btn((open[key] ? '▾ ' : '▸ ') + KINDS[s.kind].split(' (')[0] + (s.heading ? ' — ' + s.heading : ''), 'btn--ghost se-toggle', function () {
        open[key] = !open[key]; render();
      });
      toggle.setAttribute('aria-expanded', open[key] ? 'true' : 'false');
      head.appendChild(toggle);
      if (s.kind !== 'hero') head.appendChild(moveControls(p.sections, j));
      head.appendChild(btn('Remove', 'btn--ghost', function () { p.sections.splice(j, 1); changed(); render(); }));
      card.appendChild(head);
      if (open[key]) card.appendChild(sectionBody(s, sp));
      sectionsEl.appendChild(card);
    });

    var add = el('div', 'card se-add');
    var pick = el('select', 'input');
    Object.keys(KINDS).forEach(function (k) {
      if (k === 'hero' && p.sections.length && p.sections[0].kind === 'hero') return;
      var o = el('option', '', KINDS[k]);
      o.value = k;
      pick.appendChild(o);
    });
    add.appendChild(pick);
    add.appendChild(btn('Add section', 'btn--primary', function () {
      var s = newSection(pick.value, p);
      if (s.kind === 'hero') p.sections.unshift(s); else p.sections.push(s);
      open[state.page + ':' + (s.kind === 'hero' ? 0 : p.sections.length - 1)] = true;
      changed(); render();
    }));
    sectionsEl.appendChild(add);
  }

  // ── History ───────────────────────────────────────────────────────────────
  // flatten turns a document into path → value, so two revisions can be
  // compared field by field and the difference shown in the words the
  // validator uses.
  function flatten(v, path, out) {
    if (Array.isArray(v)) { v.forEach(function (x, i) { flatten(x, path + '[' + i + ']', out); }); }
    else if (v && typeof v === 'object') { Object.keys(v).forEach(function (k) { flatten(v[k], path ? path + '.' + k : k, out); }); }
    else if (v !== '' && v !== false && v !== null && v !== undefined) { out[path] = String(v); }
    return out;
  }
  function diff(a, b) {
    var fa = flatten(a, '', {}), fb = flatten(b, '', {}), rows = [];
    Object.keys(Object.assign({}, fa, fb)).sort().forEach(function (k) {
      if (fa[k] !== fb[k]) rows.push([k, fa[k], fb[k]]);
    });
    return rows;
  }
  function short(s) { return s === undefined ? '—' : (s.length > 80 ? s.slice(0, 79) + '…' : s); }

  function renderHistory() {
    if (!historyEl) return;
    historyEl.textContent = '';
    if (!state.revisions.length) {
      historyEl.appendChild(el('p', 'text-sm muted', 'Nothing published from the editor yet. The site shows its earlier content until you publish.'));
      return;
    }
    state.revisions.forEach(function (rv) {
      var row = el('div', 'se-rev');
      row.appendChild(el('span', 'text-sm', 'Revision ' + rv.id + ' · ' + new Date(rv.published_at).toLocaleString() + ' · ' + rv.author));
      var out = el('div', 'se-diff');
      row.appendChild(btn('Compare with the editor', 'btn--ghost', function () {
        api('GET', '/revisions/' + rv.id).then(function (r) {
          out.textContent = '';
          if (!r.ok) { out.appendChild(el('p', 'text-sm upd-bad', 'Could not load this revision.')); return; }
          var rows = diff(r.j.doc, payload());
          if (!rows.length) { out.appendChild(el('p', 'text-sm muted', 'Identical to what is in the editor.')); return; }
          out.appendChild(el('p', 'text-sm muted', 'Field · in revision ' + rv.id + ' · in the editor'));
          rows.slice(0, 200).forEach(function (d) {
            var line = el('div', 'se-diff-row');
            line.appendChild(el('code', '', d[0]));
            line.appendChild(el('span', 'se-old', short(d[1])));
            line.appendChild(el('span', 'se-new', short(d[2])));
            out.appendChild(line);
          });
          if (rows.length > 200) out.appendChild(el('p', 'text-sm muted', (rows.length - 200) + ' more differences'));
        });
      }));
      row.appendChild(btn('Restore', 'btn--ghost', function () {
        var go = function () {
          api('POST', '/revisions/' + rv.id + '/restore').then(function (r) {
            if (!r.ok) { showError(((r.j && r.j.error) || {}).message || 'Could not restore.'); return; }
            load(true, 'Restored revision ' + rv.id + ' — it is live as revision ' + r.j.revision);
          });
        };
        if (window.vpConfirm) window.vpConfirm({ title: 'Restore revision', message: 'Publish revision ' + rv.id + ' again? It becomes the live site, and what is live now stays in History.', confirm: 'Restore' }, go);
        else go();
      }));
      row.appendChild(out);
      historyEl.appendChild(row);
    });
  }

  // ── Media picker ──────────────────────────────────────────────────────────
  var picker = document.getElementById('se-media');
  document.addEventListener('keydown', function (e) { if (e.key === 'Escape' && picker && !picker.hidden) picker.hidden = true; });
  function pickMedia(onPick) {
    if (!picker) return;
    picker.textContent = '';
    picker.hidden = false;
    var head = el('div', 'se-media-head');
    head.appendChild(el('strong', '', 'Choose a picture'));
    var up = el('input');
    up.type = 'file';
    up.accept = 'image/*';
    up.addEventListener('change', function () {
      if (!up.files.length) return;
      var fd = new FormData();
      fd.append('file', up.files[0]);
      fetch('/os/api/media/upload', { method: 'POST', credentials: 'same-origin', headers: { 'X-CSRF-Token': csrf() }, body: fd })
        .then(function (r) { return r.json().then(function (j) { return { ok: r.ok, j: j }; }); })
        .then(function (res) {
          if (!res.ok) { showError((res.j && (res.j.error && res.j.error.message || res.j.error)) || 'Upload failed'); return; }
          picker.hidden = true;
          onPick({ url: res.j.url, alt: '' });
        });
    });
    head.appendChild(up);
    head.appendChild(btn('Close', 'btn--ghost', function () { picker.hidden = true; }));
    picker.appendChild(head);
    var grid = el('div', 'se-media-grid');
    picker.appendChild(grid);
    fetch('/os/api/media', { credentials: 'same-origin' }).then(function (r) { return r.json(); }).then(function (j) {
      (j.items || []).filter(function (it) { return !it.isPdf; }).forEach(function (it) {
        var b = el('button', 'se-media-item');
        b.type = 'button';
        var im = el('img');
        im.src = it.url;
        im.alt = it.alt || it.name;
        im.loading = 'lazy';
        b.appendChild(im);
        b.addEventListener('click', function () { picker.hidden = true; onPick(it); });
        grid.appendChild(b);
      });
      if (!grid.children.length) grid.appendChild(el('p', 'text-sm muted', 'The media library is empty — upload a picture above.'));
    });
  }

  // ── Load ──────────────────────────────────────────────────────────────────
  // load reads the site back from the server. outcome, when given, is what
  // just happened (published, restored) and is shown instead of where the
  // editor's content came from — otherwise the reload would overwrite the one
  // message the operator is waiting for.
  function load(keepPage, outcome) {
    return api('GET', '').then(function (r) {
      if (!r.ok) { say('Could not open the site'); showError(((r.j && r.j.error) || {}).message); return; }
      state.doc = r.j.doc;
      state.doc.pages = state.doc.pages || [];
      state.doc.pages.forEach(function (p) { p.sections = p.sections || []; });
      state.revisions = r.j.revisions || [];
      state.choices = r.j.choices || {};
      state.source = r.j.source;
      if (!keepPage) state.page = 0;
      var from = { draft: 'Editing your saved draft', published: 'Editing the live site', legacy: 'Editing the site as it is today' }[r.j.source] || '';
      say(outcome || from);
      render();
      renderHistory();
      reloadPreview();
    });
  }

  var pub = document.getElementById('se-publish');
  if (pub) pub.addEventListener('click', publish);
  window.addEventListener('beforeunload', function (e) {
    if (state.dirty) { e.preventDefault(); e.returnValue = ''; }
  });
  load(false);
})();
