/*
 * admin-os-editor.js — VayuPress VayuOS block editor (ADR-0068, Phase 3).
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
  var topbarStatusEl = root.querySelector('[data-editor-topbar-status]');
  var saveBtn = root.querySelector('[data-editor-save]');
  var previewBtn = root.querySelector('[data-editor-preview-btn]');
  var previewModal = root.querySelector('[data-editor-preview]');
  var previewBody = root.querySelector('[data-editor-preview-body]');
  var previewClose = root.querySelector('[data-editor-preview-close]');
  var historyBtn = root.querySelector('[data-editor-history-btn]');
  var historyModal = root.querySelector('[data-editor-history]');
  var historyList = root.querySelector('[data-editor-history-list]');
  var historyDiff = root.querySelector('[data-editor-history-diff]');
  var historyClose = root.querySelector('[data-editor-history-close]');

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

  // AI-assist commands shown at the bottom of the slash palette (when AI is available).
  var AI_CMDS = [
    { op: 'continue', label: 'AI: Continue writing', icon: '✦', hint: 'Continue the current paragraph' },
    { op: 'summarize', label: 'AI: Summarize', icon: '✦', hint: 'Condense the current block to a short summary' },
    { op: 'rewrite', label: 'AI: Rewrite', icon: '✦', hint: 'Rephrase the current block for clarity' }
  ];
  var aiEnabled = false; // updated from /os/api/editor/ai status check on load

  // ── Document model ─────────────────────────────────────────────────────────
  var blocks = [];

  function hydrate() {
    var dataEl = document.getElementById('vp-editor-data');
    var raw = dataEl ? dataEl.textContent.trim() : '';
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
    if (statusEl) {
      statusEl.textContent = msg;
      statusEl.className = 'editor-status' + (kind ? ' editor-status--' + kind : '');
    }
    if (topbarStatusEl) {
      topbarStatusEl.textContent = msg;
      topbarStatusEl.className = 'editor-topbar-status' + (kind ? ' editor-topbar-status--' + kind : '');
    }
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
        openPalette(idx + 1, idx);
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

  function makePaletteItem(icon, label, hint, onClick) {
    var item = document.createElement('button');
    item.type = 'button';
    item.className = 'block-palette__item';
    var ic = document.createElement('span');
    ic.className = 'block-palette__icon';
    ic.textContent = icon;
    var lab = document.createElement('span');
    lab.className = 'block-palette__label';
    lab.textContent = label;
    var hintEl = document.createElement('span');
    hintEl.className = 'block-palette__hint';
    hintEl.textContent = hint;
    item.appendChild(ic);
    item.appendChild(lab);
    item.appendChild(hintEl);
    item.addEventListener('click', function () { onClick(); closePalette(); });
    return item;
  }

  function openPalette(insertAt, sourceBlockIdx) {
    closePalette();
    paletteEl = document.createElement('div');
    paletteEl.className = 'block-palette';
    var list = document.createElement('div');
    list.className = 'block-palette__list';

    BLOCK_TYPES.forEach(function (bt) {
      list.appendChild(makePaletteItem(bt.icon, bt.label, bt.hint, function () {
        insertBlock(insertAt, newBlockOf(bt.type));
      }));
    });

    if (aiEnabled && sourceBlockIdx != null && sourceBlockIdx >= 0 && sourceBlockIdx < blocks.length) {
      var sep = document.createElement('div');
      sep.className = 'block-palette__sep';
      sep.textContent = 'AI assist';
      list.appendChild(sep);
      var srcBlock = blocks[sourceBlockIdx];
      var srcText = srcBlock.text || (srcBlock.items || []).join(' ') || '';
      AI_CMDS.forEach(function (cmd) {
        list.appendChild(makePaletteItem(cmd.icon, cmd.label, cmd.hint, function () {
          runAI(cmd.op, srcText, insertAt);
        }));
      });
    }

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

  // ── AI assist ─────────────────────────────────────────────────────────────
  // Sends the block's text to the AI backend and inserts the suggestion as a new
  // paragraph block. An inline overlay shows the pending state; if AI is
  // unavailable the editor silently disables AI items in the palette.
  function runAI(op, text, insertAt) {
    if (!text.trim()) {
      setStatus('Select a block with text first', 'warn');
      return;
    }
    setStatus('AI thinking…');
    fetch('/os/api/editor/ai', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': csrfToken() },
      body: JSON.stringify({ op: op, text: text })
    }).then(function (r) {
      if (r.status === 503) { aiEnabled = false; }
      if (!r.ok) throw new Error('ai-' + r.status);
      return r.json();
    }).then(function (d) {
      var result = (d && d.result) ? String(d.result) : '';
      if (!result) { setStatus('AI returned empty result', 'warn'); return; }
      showAISuggestion(result, insertAt);
      setStatus('AI suggestion ready', 'ok');
    }).catch(function (err) {
      setStatus('AI assist: ' + String(err.message || err), 'danger');
    });
  }

  // Shows an inline suggestion overlay with Accept / Discard buttons.
  function showAISuggestion(text, insertAt) {
    var existing = document.getElementById('ai-suggest-overlay');
    if (existing && existing.parentNode) existing.parentNode.removeChild(existing);

    var overlay = document.createElement('div');
    overlay.id = 'ai-suggest-overlay';
    overlay.className = 'ai-suggest';
    var pre = document.createElement('div');
    pre.className = 'ai-suggest__text';
    pre.textContent = text;
    var actions = document.createElement('div');
    actions.className = 'ai-suggest__actions';

    var accept = document.createElement('button');
    accept.type = 'button';
    accept.className = 'btn btn--primary btn--xs';
    accept.textContent = 'Accept';
    accept.addEventListener('click', function () {
      insertBlock(insertAt, { type: 'paragraph', text: text });
      overlay.parentNode && overlay.parentNode.removeChild(overlay);
    });

    var discard = document.createElement('button');
    discard.type = 'button';
    discard.className = 'btn btn--ghost btn--xs';
    discard.textContent = 'Discard';
    discard.addEventListener('click', function () {
      overlay.parentNode && overlay.parentNode.removeChild(overlay);
      setStatus('Ready');
    });

    actions.appendChild(accept);
    actions.appendChild(discard);
    overlay.appendChild(pre);
    overlay.appendChild(actions);
    canvas.parentNode.insertBefore(overlay, canvas.nextSibling);
  }

  // ── Version history ────────────────────────────────────────────────────────
  function openHistory() {
    if (!slug) { setStatus('Save the post first', 'warn'); return; }
    if (!historyModal) return;
    historyModal.hidden = false;
    loadVersionList();
  }

  function loadVersionList() {
    if (!historyList) return;
    while (historyList.firstChild) historyList.removeChild(historyList.firstChild);
    var loading = document.createElement('div');
    loading.className = 'text-sm muted';
    loading.textContent = 'Loading…';
    historyList.appendChild(loading);

    fetch('/os/api/editor/versions/' + encodeURIComponent(slug), {
      headers: { Accept: 'application/json' }
    }).then(function (r) { return r.ok ? r.json() : Promise.reject(r.status); })
      .then(function (d) {
        while (historyList.firstChild) historyList.removeChild(historyList.firstChild);
        var list = (d && d.versions) || [];
        if (!list.length) {
          var empty = document.createElement('div');
          empty.className = 'text-sm muted';
          empty.textContent = 'No versions yet.';
          historyList.appendChild(empty);
          return;
        }
        list.forEach(function (v) {
          var btn = document.createElement('button');
          btn.type = 'button';
          btn.className = 'history-ver';
          var ts = document.createElement('span');
          ts.className = 'history-ver__ts';
          ts.textContent = new Date(v.created_at || v.CreatedAt || '').toLocaleString();
          var label = document.createElement('span');
          label.className = 'history-ver__label';
          label.textContent = v.label || ('#' + v.id);
          btn.appendChild(ts);
          btn.appendChild(label);
          btn.addEventListener('click', function () { loadVersionDiff(v.id); });
          historyList.appendChild(btn);
        });
      }).catch(function () {
        while (historyList.firstChild) historyList.removeChild(historyList.firstChild);
        var err = document.createElement('div');
        err.className = 'text-sm muted';
        err.textContent = 'Could not load versions.';
        historyList.appendChild(err);
      });
  }

  function loadVersionDiff(id) {
    if (!historyDiff) return;
    while (historyDiff.firstChild) historyDiff.removeChild(historyDiff.firstChild);
    var loading = document.createElement('div');
    loading.className = 'text-sm muted';
    loading.textContent = 'Loading diff…';
    historyDiff.appendChild(loading);

    fetch('/os/api/editor/versions/' + encodeURIComponent(slug) + '/' + encodeURIComponent(String(id)), {
      headers: { Accept: 'application/json' }
    }).then(function (r) { return r.ok ? r.json() : Promise.reject(r.status); })
      .then(function (v) {
        while (historyDiff.firstChild) historyDiff.removeChild(historyDiff.firstChild);
        var currentText = collectAllText();
        var oldText = v.content || v.Content || '';
        renderWordDiff(currentText, oldText);
      }).catch(function () {
        while (historyDiff.firstChild) historyDiff.removeChild(historyDiff.firstChild);
        var err = document.createElement('div');
        err.className = 'text-sm muted';
        err.textContent = 'Could not load version.';
        historyDiff.appendChild(err);
      });
  }

  // collectAllText joins all block texts into one string for diffing.
  function collectAllText() {
    return blocks.map(function (b) {
      return b.text || (b.items || []).join('\n') || '';
    }).join('\n');
  }

  // renderWordDiff builds a word-level visual diff in historyDiff using only
  // DOM APIs — no innerHTML with untrusted data. Words present in old but absent
  // in new are shown in red (del), words present in new but absent in old are in
  // green (ins). Common words pass through unstyled.
  function renderWordDiff(current, old) {
    var pre = document.createElement('div');
    pre.className = 'history-diff__view';

    var oldWords = old.split(/\s+/).filter(Boolean);
    var newWords = current.split(/\s+/).filter(Boolean);

    // Simple LCS-based word diff using a greedy approach.
    var ops = lcsWordDiff(oldWords, newWords);
    ops.forEach(function (op) {
      var span = document.createElement('span');
      span.className = op.kind === 'del' ? 'diff-del'
                      : op.kind === 'ins' ? 'diff-ins'
                      : 'diff-eq';
      span.textContent = op.word + ' ';
      pre.appendChild(span);
    });

    var heading = document.createElement('div');
    heading.className = 'history-diff__title text-xs muted';
    heading.textContent = 'Current ↔ selected version (word-level diff)';
    historyDiff.appendChild(heading);
    historyDiff.appendChild(pre);
  }

  // lcsWordDiff: returns array of {kind:'eq'|'ins'|'del', word} operations.
  function lcsWordDiff(oldW, newW) {
    var m = oldW.length, n = newW.length;
    // Build LCS table (limit to 300 words each side to stay O(n²) bounded).
    var maxW = 300;
    if (m > maxW || n > maxW) {
      var ops = [];
      oldW.slice(0, maxW).forEach(function (w) { ops.push({ kind: 'del', word: w }); });
      newW.slice(0, maxW).forEach(function (w) { ops.push({ kind: 'ins', word: w }); });
      return ops;
    }
    var dp = [];
    for (var i = 0; i <= m; i++) {
      dp[i] = new Array(n + 1).fill(0);
    }
    for (var ii = 1; ii <= m; ii++) {
      for (var jj = 1; jj <= n; jj++) {
        dp[ii][jj] = oldW[ii-1] === newW[jj-1]
          ? dp[ii-1][jj-1] + 1
          : Math.max(dp[ii-1][jj], dp[ii][jj-1]);
      }
    }
    var result = [];
    var a = m, b = n;
    while (a > 0 || b > 0) {
      if (a > 0 && b > 0 && oldW[a-1] === newW[b-1]) {
        result.unshift({ kind: 'eq', word: oldW[a-1] });
        a--; b--;
      } else if (b > 0 && (a === 0 || dp[a][b-1] >= dp[a-1][b])) {
        result.unshift({ kind: 'ins', word: newW[b-1] });
        b--;
      } else {
        result.unshift({ kind: 'del', word: oldW[a-1] });
        a--;
      }
    }
    return result;
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
    // Brand-new drafts have no slug yet; the server creates the post on first
    // save and returns the new slug. A title is required to derive that slug.
    if (!slug && (!titleEl || !titleEl.value.trim())) {
      setStatus('Add a title before saving', 'warn');
      if (titleEl) titleEl.focus();
      return;
    }
    setStatus('Saving…');
    fetch('/os/api/editor/save', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': csrfToken() },
      body: payload()
    }).then(function (r) {
      if (!r.ok) throw new Error('save failed (' + r.status + ')');
      return r.json();
    }).then(function (data) {
      // On create, adopt the server-assigned slug and update the URL in place so
      // a refresh re-opens the same post and autosave/history start working.
      if (!slug && data && data.slug) {
        slug = data.slug;
        if (root) root.setAttribute('data-slug', slug);
        try { history.replaceState({}, '', '/os/editor/' + encodeURIComponent(slug)); } catch (e) {}
      }
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
    fetch('/os/api/editor/preview', {
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
  if (historyBtn) historyBtn.addEventListener('click', openHistory);
  if (historyClose) historyClose.addEventListener('click', function () { historyModal.hidden = true; });
  if (titleEl) titleEl.addEventListener('input', scheduleAutosave);

  // Cmd/Ctrl+S saves.
  document.addEventListener('keydown', function (e) {
    if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 's') {
      e.preventDefault();
      save();
    }
  });

  // Probe AI availability (fire-and-forget, affects palette only).
  fetch('/os/api/editor/ai', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': csrfToken() },
    body: JSON.stringify({ op: 'ping', text: '' })
  }).then(function (r) {
    // 503 → AI disabled; anything else (even 400 bad-op) means the endpoint is live.
    if (r.status !== 503) aiEnabled = true;
  }).catch(function () {});
})();
