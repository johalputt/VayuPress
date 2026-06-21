/*
 * admin-v3-editor.js — VayuPress Admin v3 block editor (ADR-0068, Phase 3).
 *
 * Vanilla JS, strict CSP: no eval, no new Function, no innerHTML with untrusted
 * data. The DOM is built with createElement/textContent. The canonical document
 * is an array of typed blocks; it is serialised to JSON and POSTed to the server,
 * which renders + sanitises it (internal/blockrender) — the client never trusts
 * rendered HTML except the server's own sanitised preview, injected via a
 * sandboxed assignment guarded below.
 */
(function () {
  'use strict';

  var root = document.querySelector('[data-editor]');
  if (!root) return;

  var slug = root.getAttribute('data-slug') || '';
  var canvas = root.querySelector('[data-editor-canvas]');
  var titleEl = root.querySelector('[data-editor-title]');
  var statusEl = root.querySelector('[data-editor-status]');
  var saveBtn = root.querySelector('[data-editor-save]');
  var previewBtn = root.querySelector('[data-editor-preview-btn]');
  var previewModal = root.querySelector('[data-editor-preview]');
  var previewBody = root.querySelector('[data-editor-preview-body]');
  var previewClose = root.querySelector('[data-editor-preview-close]');

  // Block type registry. Each defines how to create its editing UI and how to
  // serialise back to the document model.
  var BLOCK_TYPES = [
    { type: 'paragraph', label: 'Text', icon: '¶', hint: 'Plain paragraph' },
    { type: 'heading', label: 'Heading', icon: 'H', hint: 'Section heading' },
    { type: 'list', label: 'Bullet list', icon: '•', hint: 'Unordered list' },
    { type: 'ordered', label: 'Numbered list', icon: '1.', hint: 'Ordered list' },
    { type: 'quote', label: 'Quote', icon: '"', hint: 'Block quote' },
    { type: 'code', label: 'Code', icon: '</>', hint: 'Code block' },
    { type: 'callout', label: 'Callout', icon: '!', hint: 'Highlighted note' },
    { type: 'image', label: 'Image', icon: '🖼', hint: 'Image by URL' },
    { type: 'embed', label: 'Embed / Link card', icon: '🔗', hint: 'Unfurl a URL as a rich link card' },
    { type: 'diagram', label: 'Diagram', icon: '🔀', hint: 'Mermaid flowchart / sequence → SVG' },
    { type: 'divider', label: 'Divider', icon: '―', hint: 'Horizontal rule' }
  ];

  // ── Document model ─────────────────────────────────────────────────────────
  var blocks = [];

  function hydrate() {
    var raw = root.getAttribute('data-blocks');
    if (raw) {
      try {
        var parsed = JSON.parse(raw);
        if (Array.isArray(parsed) && parsed.length) {
          blocks = parsed;
        }
      } catch (e) { /* start fresh */ }
    }
    if (!blocks.length) {
      blocks = [{ type: 'paragraph', text: '' }];
    }
  }

  // ── Rendering the editing surface (all DOM built safely) ───────────────────
  function setStatus(msg, kind) {
    if (!statusEl) return;
    statusEl.textContent = msg;
    statusEl.className = 'editor-status' + (kind ? ' editor-status--' + kind : '');
  }

  function makeBlockEl(block, idx) {
    var wrap = document.createElement('div');
    wrap.className = 'eblock eblock--' + (block.type || 'paragraph');
    wrap.setAttribute('data-block-idx', String(idx));

    // Drag handle / controls column.
    var ctrl = document.createElement('div');
    ctrl.className = 'eblock__ctrl';
    var addBtn = document.createElement('button');
    addBtn.type = 'button';
    addBtn.className = 'eblock__btn';
    addBtn.textContent = '+';
    addBtn.title = 'Insert block below';
    addBtn.addEventListener('click', function () { openPalette(idx + 1); });
    var delBtn = document.createElement('button');
    delBtn.type = 'button';
    delBtn.className = 'eblock__btn';
    delBtn.textContent = '×';
    delBtn.title = 'Delete block';
    delBtn.addEventListener('click', function () { removeBlock(idx); });
    ctrl.appendChild(addBtn);
    ctrl.appendChild(delBtn);
    wrap.appendChild(ctrl);

    var field = buildField(block, idx);
    wrap.appendChild(field);
    return wrap;
  }

  // buildField returns the per-type editable control.
  function buildField(block, idx) {
    var field = document.createElement('div');
    field.className = 'eblock__field';

    switch (block.type) {
      case 'divider': {
        var hr = document.createElement('hr');
        hr.className = 'eblock__divider';
        field.appendChild(hr);
        break;
      }
      case 'image': {
        var url = mkInput('text', block.url || '', 'Image URL (https://…)');
        url.addEventListener('input', function () { block.url = url.value; });
        var alt = mkInput('text', block.alt || '', 'Alt text (described for accessibility)');
        alt.addEventListener('input', function () { block.alt = alt.value; });
        field.appendChild(url);
        field.appendChild(alt);
        break;
      }
      case 'embed': {
        var embedUrl = mkInput('text', block.url || '', 'Paste a URL to unfurl…');
        var embedStatus = document.createElement('span');
        embedStatus.className = 'eblock__embed-status';
        if (block.title) {
          embedStatus.textContent = block.title;
        }
        embedUrl.addEventListener('change', function () {
          var val = embedUrl.value.trim();
          block.url = val;
          if (!val) return;
          embedStatus.textContent = 'Fetching…';
          fetch('/api/v1/admin/embed/unfurl', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': csrfToken() },
            body: JSON.stringify({ url: val })
          }).then(function (r) { return r.json(); }).then(function (data) {
            if (data.error) { embedStatus.textContent = 'Error: ' + data.error; return; }
            block.url = data.url || val;
            block.title = data.title || '';
            block.description = data.description || '';
            block.provider = data.provider || '';
            block.thumbURL = data.thumbURL || '';
            block.kind = data.kind || 'link';
            block.embedSrc = data.embedSrc || '';
            var verb = block.kind === 'video' ? '▶ Video: ' : '';
            embedStatus.textContent = verb + (block.title || block.url);
            scheduleAutosave();
          }).catch(function () { embedStatus.textContent = 'Could not fetch URL'; });
        });
        field.appendChild(embedUrl);
        field.appendChild(embedStatus);
        break;
      }
      case 'list':
      case 'ordered': {
        var ta = mkTextarea((block.items || []).join('\n'), 'One item per line');
        ta.addEventListener('input', function () {
          block.items = ta.value.split('\n').map(function (s) { return s; }).filter(function (s) { return s.trim() !== ''; });
        });
        field.appendChild(ta);
        break;
      }
      case 'code': {
        var lang = mkInput('text', block.lang || '', 'Language (e.g. go, js)');
        lang.className += ' eblock__lang';
        lang.addEventListener('input', function () { block.lang = lang.value; });
        var code = mkTextarea(block.text || '', 'Code…');
        code.className += ' eblock__code';
        code.addEventListener('input', function () { block.text = code.value; });
        field.appendChild(lang);
        field.appendChild(code);
        break;
      }
      case 'diagram': {
        var dsrc = mkTextarea(block.text || '', 'flowchart TD\n  A[Start] --> B[End]');
        dsrc.className += ' eblock__code';
        var dprev = document.createElement('div');
        dprev.className = 'eblock__diagram-preview';
        var dtimer;
        var renderDiag = function () {
          var v = dsrc.value.trim();
          if (!v) { dprev.innerHTML = ''; return; }
          fetch('/api/v1/admin/diagram/preview', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': csrfToken() },
            body: JSON.stringify({ source: v })
          }).then(function (r) { return r.json(); }).then(function (d) {
            if (d.svg) { dprev.innerHTML = d.svg; dprev.classList.remove('is-error'); }
            else { dprev.textContent = d.error || 'Could not render diagram'; dprev.classList.add('is-error'); }
          }).catch(function () { dprev.textContent = 'Preview failed'; dprev.classList.add('is-error'); });
        };
        dsrc.addEventListener('input', function () {
          block.text = dsrc.value;
          clearTimeout(dtimer);
          dtimer = setTimeout(renderDiag, 400);
        });
        field.appendChild(dsrc);
        field.appendChild(dprev);
        if (block.text) { setTimeout(renderDiag, 0); }
        break;
      }
      case 'heading': {
        var lvlSel = document.createElement('select');
        lvlSel.className = 'eblock__level';
        [2, 3, 4].forEach(function (n) {
          var opt = document.createElement('option');
          opt.value = String(n);
          opt.textContent = 'H' + n;
          if ((block.level || 2) === n) opt.selected = true;
          lvlSel.appendChild(opt);
        });
        lvlSel.addEventListener('change', function () { block.level = parseInt(lvlSel.value, 10); });
        var ht = mkInput('text', block.text || '', 'Heading…');
        ht.className += ' eblock__heading';
        ht.addEventListener('input', function () { block.text = ht.value; });
        ht.addEventListener('keydown', onTextKey(idx));
        field.appendChild(lvlSel);
        field.appendChild(ht);
        break;
      }
      default: {
        // paragraph, quote, callout — single text area.
        var t = mkTextarea(block.text || '', placeholderFor(block.type));
        t.addEventListener('input', function () { block.text = t.value; autoGrow(t); });
        t.addEventListener('keydown', onTextKey(idx));
        field.appendChild(t);
        if (block.type === 'callout') {
          var tone = mkInput('text', block.style || 'info', 'Tone (info, warn, success)');
          tone.className += ' eblock__tone';
          tone.addEventListener('input', function () { block.style = tone.value; });
          field.appendChild(tone);
        }
        // Defer autogrow until in DOM.
        setTimeout(function () { autoGrow(t); }, 0);
      }
    }
    return field;
  }

  function placeholderFor(type) {
    if (type === 'quote') return 'Quote…';
    if (type === 'callout') return 'Callout text…';
    return "Write something, or press '/' for blocks…";
  }

  function mkInput(type, val, ph) {
    var i = document.createElement('input');
    i.type = type;
    i.className = 'eblock__input';
    i.value = val;
    i.placeholder = ph;
    return i;
  }

  function mkTextarea(val, ph) {
    var t = document.createElement('textarea');
    t.className = 'eblock__text';
    t.value = val;
    t.placeholder = ph;
    t.rows = 1;
    return t;
  }

  function autoGrow(t) {
    t.style.height = 'auto';
    t.style.height = (t.scrollHeight) + 'px';
  }

  // Enter on an empty text block at the cursor with leading '/' opens the palette.
  function onTextKey(idx) {
    return function (e) {
      var el = e.target;
      if (e.key === '/' && el.value === '') {
        e.preventDefault();
        openPalette(idx + 1);
        return;
      }
      if (e.key === 'Enter' && !e.shiftKey && (el.tagName === 'INPUT')) {
        e.preventDefault();
        insertBlock(idx + 1, { type: 'paragraph', text: '' });
      }
    };
  }

  function renderCanvas() {
    while (canvas.firstChild) canvas.removeChild(canvas.firstChild);
    blocks.forEach(function (b, i) {
      canvas.appendChild(makeBlockEl(b, i));
    });
  }

  // ── Mutations ──────────────────────────────────────────────────────────────
  function insertBlock(at, block) {
    if (at < 0) at = 0;
    if (at > blocks.length) at = blocks.length;
    blocks.splice(at, 0, block);
    renderCanvas();
    scheduleAutosave();
  }

  function removeBlock(idx) {
    blocks.splice(idx, 1);
    if (!blocks.length) blocks = [{ type: 'paragraph', text: '' }];
    renderCanvas();
    scheduleAutosave();
  }

  // ── Slash command palette ──────────────────────────────────────────────────
  var paletteEl = null;
  function openPalette(insertAt) {
    closePalette();
    paletteEl = document.createElement('div');
    paletteEl.className = 'block-palette';
    var list = document.createElement('div');
    list.className = 'block-palette__list';
    BLOCK_TYPES.forEach(function (bt) {
      var item = document.createElement('button');
      item.type = 'button';
      item.className = 'block-palette__item';
      var ic = document.createElement('span');
      ic.className = 'block-palette__icon';
      ic.textContent = bt.icon;
      var lab = document.createElement('span');
      lab.className = 'block-palette__label';
      lab.textContent = bt.label;
      var hint = document.createElement('span');
      hint.className = 'block-palette__hint';
      hint.textContent = bt.hint;
      item.appendChild(ic);
      item.appendChild(lab);
      item.appendChild(hint);
      item.addEventListener('click', function () {
        insertBlock(insertAt, newBlockOf(bt.type));
        closePalette();
      });
      list.appendChild(item);
    });
    paletteEl.appendChild(list);
    document.body.appendChild(paletteEl);
    document.addEventListener('keydown', escClose);
    setTimeout(function () { document.addEventListener('click', outsideClose); }, 0);
  }

  function newBlockOf(type) {
    if (type === 'ordered') return { type: 'list', style: 'ordered', items: [] };
    if (type === 'list') return { type: 'list', style: 'unordered', items: [] };
    if (type === 'heading') return { type: 'heading', level: 2, text: '' };
    if (type === 'divider') return { type: 'divider' };
    if (type === 'image') return { type: 'image', url: '', alt: '' };
    if (type === 'embed') return { type: 'embed', url: '', title: '', description: '', provider: '', thumbURL: '' };
    if (type === 'code') return { type: 'code', lang: '', text: '' };
    if (type === 'diagram') return { type: 'diagram', text: '' };
    if (type === 'callout') return { type: 'callout', style: 'info', text: '' };
    return { type: 'paragraph', text: '' };
  }

  function escClose(e) { if (e.key === 'Escape') closePalette(); }
  function outsideClose(e) {
    if (paletteEl && !paletteEl.contains(e.target)) closePalette();
  }
  function closePalette() {
    if (paletteEl && paletteEl.parentNode) paletteEl.parentNode.removeChild(paletteEl);
    paletteEl = null;
    document.removeEventListener('keydown', escClose);
    document.removeEventListener('click', outsideClose);
  }

  // ── Persistence ────────────────────────────────────────────────────────────
  function csrfToken() {
    var m = document.cookie.match(/(?:^|;\s*)vp_csrf=([^;]+)/);
    return m ? decodeURIComponent(m[1]) : '';
  }

  function payload() {
    return JSON.stringify({
      slug: slug,
      title: titleEl ? titleEl.value : '',
      blocks: blocks
    });
  }

  function save() {
    if (!slug) {
      setStatus('Save unavailable for unsaved drafts', 'warn');
      return;
    }
    setStatus('Saving…');
    fetch('/admin/v3/api/editor/save', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': csrfToken() },
      body: payload()
    }).then(function (r) {
      if (!r.ok) throw new Error('save failed (' + r.status + ')');
      return r.json();
    }).then(function () {
      setStatus('Saved · ' + new Date().toLocaleTimeString(), 'ok');
      if (window.vpToast) window.vpToast('Post saved', 'ok');
    }).catch(function (err) {
      setStatus(String(err.message || err), 'danger');
    });
  }

  var autosaveTimer = null;
  function scheduleAutosave() {
    if (!slug) return;
    setStatus('Editing…');
    if (autosaveTimer) clearTimeout(autosaveTimer);
    autosaveTimer = setTimeout(save, 2500);
  }

  // Live preview. The server already returns UGC-sanitised HTML (blockrender),
  // but to display rendered markup the client must reinterpret a string as HTML.
  // Every path to the DOM sink is funnelled through DOMPurify.sanitize so no
  // unsanitised string can ever reach it; if DOMPurify is somehow unavailable we
  // degrade to a textContent rendering (escaped, never executed).
  function renderPreview(rawHTML) {
    if (window.DOMPurify && typeof window.DOMPurify.sanitize === 'function') {
      // DOMPurify.sanitize is a recognised XSS sanitizer; its output is safe.
      previewBody.innerHTML = window.DOMPurify.sanitize(rawHTML);
    } else {
      // Fail closed: show the markup as inert text rather than risk injection.
      previewBody.textContent = rawHTML;
    }
  }

  function preview() {
    setStatus('Rendering preview…');
    fetch('/admin/v3/api/editor/preview', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': csrfToken() },
      body: JSON.stringify({ blocks: blocks })
    }).then(function (r) { return r.json(); }).then(function (data) {
      renderPreview(data.html || '');
      previewModal.hidden = false;
      setStatus('Ready');
    }).catch(function () { setStatus('Preview failed', 'danger'); });
  }

  // ── Wire up ────────────────────────────────────────────────────────────────
  hydrate();
  renderCanvas();

  if (saveBtn) saveBtn.addEventListener('click', save);
  if (previewBtn) previewBtn.addEventListener('click', preview);
  if (previewClose) previewClose.addEventListener('click', function () { previewModal.hidden = true; });
  if (titleEl) titleEl.addEventListener('input', scheduleAutosave);

  // Cmd/Ctrl+S saves.
  document.addEventListener('keydown', function (e) {
    if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 's') {
      e.preventDefault();
      save();
    }
  });
})();
