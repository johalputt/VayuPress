/*
 * admin-os-editor.js — VayuPress VayuOS block editor (ADR-0068; v1.14.0 upgrade).
 *
 * Vanilla JS, strict CSP: no eval, no new Function, no innerHTML with untrusted
 * data. The DOM is built with createElement/textContent. The canonical document
 * is an array of typed blocks; it is serialised to JSON and POSTed to the server,
 * which renders + sanitises it (internal/blockrender) — the client never trusts
 * rendered HTML except the server's own sanitised preview, injected only through
 * the DOMPurify-guarded renderSanitized() sink below.
 *
 * v1.14.0 adds: table / toggle / task-list / math / audio blocks, drag-and-drop
 * + keyboard block reordering, an undo/redo stack, live word/character count and
 * reading time, a distraction-free focus mode, a split-screen live preview, a
 * global Cmd/Ctrl+K command menu, and a categorised, keyboard-navigable slash
 * palette. Element.style mutations from JS are CSP-safe (the policy governs HTML
 * style attributes / <style>, not scripted style writes).
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
  // v1.14.0 chrome (optional — guarded everywhere so older shells still work).
  var focusBtn = root.querySelector('[data-editor-focus-btn]');
  var splitBtn = root.querySelector('[data-editor-split-btn]');
  var htmlBtn = root.querySelector('[data-editor-html-btn]');
  var htmlPanel = root.querySelector('[data-editor-html-panel]');
  var htmlArea = root.querySelector('[data-editor-html-area]');
  var wordCountEl = root.querySelector('[data-editor-wordcount]');
  var liveEl = root.querySelector('[data-editor-live]');
  var liveBody = root.querySelector('[data-editor-live-body]');
  var statsWordsEl = root.querySelector('[data-editor-stats-words]');
  var statsCharsEl = root.querySelector('[data-editor-stats-chars]');
  var statsReadEl = root.querySelector('[data-editor-stats-read]');
  var undoBtn = root.querySelector('[data-editor-undo]');
  var redoBtn = root.querySelector('[data-editor-redo]');

  // Block type registry. `cat` groups blocks in the slash palette. Each defines
  // how to create its editing UI and how to serialise back to the document model.
  var BLOCK_TYPES = [
    { type: 'paragraph', label: 'Text', icon: '¶', hint: 'Plain paragraph', cat: 'Basic' },
    { type: 'heading', label: 'Heading', icon: 'H', hint: 'Section heading', cat: 'Basic' },
    { type: 'list', label: 'Bullet list', icon: '•', hint: 'Unordered list', cat: 'Basic' },
    { type: 'ordered', label: 'Numbered list', icon: '1.', hint: 'Ordered list', cat: 'Basic' },
    { type: 'tasklist', label: 'Task list', icon: '☑', hint: 'Checklist with done states', cat: 'Basic' },
    { type: 'quote', label: 'Quote', icon: '"', hint: 'Block quote', cat: 'Basic' },
    { type: 'divider', label: 'Divider', icon: '―', hint: 'Horizontal rule', cat: 'Basic' },
    { type: 'image', label: 'Image', icon: '🖼', hint: 'Image by URL or upload', cat: 'Media' },
    { type: 'gallery', label: 'Gallery', icon: '▦', hint: 'Responsive image gallery (up to 9)', cat: 'Media' },
    { type: 'audio', label: 'Audio', icon: '♪', hint: 'Self-hosted audio player', cat: 'Media' },
    { type: 'embed', label: 'Embed / Link card', icon: '🔗', hint: 'Unfurl a URL (privacy-first)', cat: 'Embeds' },
    { type: 'diagram', label: 'Diagram', icon: '🔀', hint: 'Mermaid flowchart / sequence → SVG', cat: 'Embeds' },
    { type: 'markdown', label: 'Markdown', icon: 'M↓', hint: 'Write a block in Markdown', cat: 'Advanced' },
    { type: 'html', label: 'HTML', icon: '<>', hint: 'Custom HTML (sanitised on save)', cat: 'Advanced' },
    { type: 'code', label: 'Code', icon: '</>', hint: 'Code block with language hint', cat: 'Advanced' },
    { type: 'callout', label: 'Callout', icon: '!', hint: 'Highlighted note', cat: 'Advanced' },
    { type: 'table', label: 'Table', icon: '⊞', hint: 'Rows & columns', cat: 'Advanced' },
    { type: 'toggle', label: 'Toggle', icon: '▸', hint: 'Collapsible details', cat: 'Advanced' },
    { type: 'math', label: 'Math', icon: '∑', hint: 'LaTeX / math expression', cat: 'Advanced' }
  ];
  var CATEGORIES = ['Basic', 'Media', 'Embeds', 'Advanced'];

  // AI-assist commands shown at the bottom of the slash palette (when AI is available).
  var AI_CMDS = [
    { op: 'continue', label: 'AI: Continue writing', icon: '✦', hint: 'Continue the current paragraph' },
    { op: 'summarize', label: 'AI: Summarize', icon: '✦', hint: 'Condense the current block to a short summary' },
    { op: 'rewrite', label: 'AI: Rewrite', icon: '✦', hint: 'Rephrase the current block for clarity' }
  ];
  var aiEnabled = false; // updated from /os/api/editor/ai status check on load

  // ── Document model ─────────────────────────────────────────────────────────
  var blocks = [];
  var dragSrcIdx = -1;

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

  // ── Status ──────────────────────────────────────────────────────────────────
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

  // ── Block-text extraction (stats, diff, AI source) ──────────────────────────
  function blockText(b) {
    if (!b) return '';
    switch (b.type) {
      case 'list':
      case 'ordered':
      case 'tasklist':
        return (b.items || []).join(' ');
      case 'table':
        return (b.header || []).join(' ') + ' ' +
          (b.rows || []).map(function (r) { return (r || []).join(' '); }).join(' ');
      case 'toggle':
        return (b.summary || '') + ' ' + (b.text || '');
      case 'gallery':
        return b.caption || '';
      default:
        return b.text || '';
    }
  }
  function collectAllText() {
    return blocks.map(blockText).join('\n');
  }

  // ── Live stats: word / character count + reading time ───────────────────────
  function countWords(s) {
    var t = (s || '').trim();
    if (!t) return 0;
    return t.split(/\s+/).length;
  }
  function updateStats() {
    var text = collectAllText();
    var words = countWords(text);
    var chars = text.replace(/\s+/g, ' ').trim().length;
    var mins = words ? Math.max(1, Math.round(words / 200)) : 0;
    if (wordCountEl) wordCountEl.textContent = words + (words === 1 ? ' word' : ' words');
    if (statsWordsEl) statsWordsEl.textContent = String(words);
    if (statsCharsEl) statsCharsEl.textContent = String(chars);
    if (statsReadEl) statsReadEl.textContent = mins ? (mins + ' min read') : '—';
  }

  // ── Undo / redo (block-level checkpoints) ───────────────────────────────────
  // History holds JSON snapshots of the block document. Structural mutations
  // checkpoint immediately; text edits checkpoint on a short debounce. Native
  // per-field text undo is preserved because Cmd/Ctrl+Z is only intercepted when
  // focus is NOT inside an editable field.
  var hist = [], histPos = -1, histTimer = null;
  function serialize() { return JSON.stringify(blocks); }
  function commitNow() {
    if (histTimer) { clearTimeout(histTimer); histTimer = null; }
    var s = serialize();
    if (histPos >= 0 && hist[histPos] === s) return;
    hist = hist.slice(0, histPos + 1);
    hist.push(s);
    if (hist.length > 120) hist.shift();
    histPos = hist.length - 1;
    refreshUndoButtons();
  }
  function scheduleCommit() {
    if (histTimer) clearTimeout(histTimer);
    histTimer = setTimeout(commitNow, 600);
  }
  function restore(state) {
    try { blocks = JSON.parse(state); } catch (e) { return; }
    if (!Array.isArray(blocks) || !blocks.length) blocks = [{ type: 'paragraph', text: '' }];
    renderCanvas();
    updateStats();
    scheduleLivePreview();
    scheduleAutosave();
  }
  function undo() {
    if (histTimer) commitNow();
    if (histPos > 0) { histPos--; restore(hist[histPos]); setStatus('Undo', 'ok'); refreshUndoButtons(); }
  }
  function redo() {
    if (histPos < hist.length - 1) { histPos++; restore(hist[histPos]); setStatus('Redo', 'ok'); refreshUndoButtons(); }
  }
  function refreshUndoButtons() {
    if (undoBtn) undoBtn.disabled = histPos <= 0;
    if (redoBtn) redoBtn.disabled = histPos >= hist.length - 1;
  }

  // touch() is called after any text-field edit; structural() after any block
  // add / remove / move / type change.
  function touch() { updateStats(); scheduleAutosave(); scheduleCommit(); scheduleLivePreview(); }
  function structural() { renderCanvas(); commitNow(); updateStats(); scheduleAutosave(); scheduleLivePreview(); }

  // ── Rendering the editing surface (all DOM built safely) ───────────────────
  function clearDropMarkers() {
    var els = canvas.querySelectorAll('.is-drop-before, .is-drop-after, .is-dragging');
    Array.prototype.forEach.call(els, function (el) {
      el.classList.remove('is-drop-before', 'is-drop-after', 'is-dragging');
    });
  }

  function makeBlockEl(block, idx) {
    var wrap = document.createElement('div');
    wrap.className = 'eblock eblock--' + (block.type || 'paragraph');
    wrap.setAttribute('data-block-idx', String(idx));

    // Control rail (drag handle, move up/down, insert, delete).
    var ctrl = document.createElement('div');
    ctrl.className = 'eblock__ctrl';

    var drag = document.createElement('button');
    drag.type = 'button';
    drag.className = 'eblock__btn eblock__drag';
    drag.textContent = '⋮⋮';
    drag.title = 'Drag to reorder';
    drag.setAttribute('draggable', 'true');
    drag.setAttribute('aria-label', 'Drag to reorder block');
    drag.addEventListener('dragstart', function (e) {
      dragSrcIdx = idx;
      if (e.dataTransfer) {
        e.dataTransfer.effectAllowed = 'move';
        try { e.dataTransfer.setData('text/plain', String(idx)); } catch (err) {}
        try { e.dataTransfer.setDragImage(wrap, 12, 12); } catch (err) {}
      }
      wrap.classList.add('is-dragging');
    });
    drag.addEventListener('dragend', function () { dragSrcIdx = -1; clearDropMarkers(); });

    var up = mkCtrlBtn('↑', 'Move up', function () { nudge(idx, -1); });
    var down = mkCtrlBtn('↓', 'Move down', function () { nudge(idx, 1); });
    var addBtn = mkCtrlBtn('+', 'Insert block below', function () { openPalette(idx + 1, idx); });
    var delBtn = mkCtrlBtn('×', 'Delete block', function () { removeBlock(idx); });

    ctrl.appendChild(drag);
    ctrl.appendChild(up);
    ctrl.appendChild(down);
    ctrl.appendChild(addBtn);
    ctrl.appendChild(delBtn);
    wrap.appendChild(ctrl);

    // Drop-target behaviour for reordering.
    wrap.addEventListener('dragover', function (e) {
      if (dragSrcIdx < 0) return;
      e.preventDefault();
      if (e.dataTransfer) e.dataTransfer.dropEffect = 'move';
      var rect = wrap.getBoundingClientRect();
      var after = (e.clientY - rect.top) > rect.height / 2;
      wrap.classList.toggle('is-drop-after', after);
      wrap.classList.toggle('is-drop-before', !after);
    });
    wrap.addEventListener('dragleave', function () {
      wrap.classList.remove('is-drop-before', 'is-drop-after');
    });
    wrap.addEventListener('drop', function (e) {
      if (dragSrcIdx < 0) return;
      e.preventDefault();
      e.stopPropagation();
      var rect = wrap.getBoundingClientRect();
      var after = (e.clientY - rect.top) > rect.height / 2;
      moveBlock(dragSrcIdx, after ? idx + 1 : idx);
      clearDropMarkers();
    });

    wrap.appendChild(buildField(block, idx));
    return wrap;
  }

  function mkCtrlBtn(label, title, onClick) {
    var b = document.createElement('button');
    b.type = 'button';
    b.className = 'eblock__btn';
    b.textContent = label;
    b.title = title;
    b.addEventListener('click', onClick);
    return b;
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
        var url = mkInput('text', block.url || '', 'Image URL (https://…) or upload');
        url.addEventListener('input', function () { block.url = url.value; touch(); });
        var uprow = document.createElement('div');
        uprow.className = 'eblock__row';
        var upBtn = mkSmallBtn('⬆ Upload', function () { upFile.click(); });
        var upFile = document.createElement('input');
        upFile.type = 'file';
        upFile.accept = 'image/*';
        upFile.style.display = 'none';
        upFile.addEventListener('change', function () {
          if (upFile.files && upFile.files[0]) {
            uploadInto(upFile.files[0], function (u) { block.url = u; url.value = u; touch(); });
          }
          upFile.value = '';
        });
        uprow.appendChild(url);
        uprow.appendChild(upBtn);
        uprow.appendChild(upFile);
        var alt = mkInput('text', block.alt || '', 'Alt text (described for accessibility)');
        alt.addEventListener('input', function () { block.alt = alt.value; touch(); });
        var cap = mkInput('text', block.caption || '', 'Caption (optional)');
        cap.addEventListener('input', function () { block.caption = cap.value; touch(); });
        var width = document.createElement('select');
        width.className = 'eblock__level';
        [['', 'Regular width'], ['wide', 'Wide'], ['full', 'Full width']].forEach(function (o) {
          var opt = document.createElement('option');
          opt.value = o[0];
          opt.textContent = o[1];
          if ((block.width || '') === o[0]) opt.selected = true;
          width.appendChild(opt);
        });
        width.addEventListener('change', function () { block.width = width.value; touch(); });
        field.appendChild(uprow);
        field.appendChild(alt);
        field.appendChild(cap);
        field.appendChild(width);
        break;
      }
      case 'gallery': {
        field.appendChild(buildGalleryField(block, idx));
        break;
      }
      case 'html': {
        var htmlSrc = mkTextarea(block.text || '', '<div>Custom HTML…</div>');
        htmlSrc.className += ' eblock__code';
        htmlSrc.addEventListener('input', function () { block.text = htmlSrc.value; autoGrow(htmlSrc); touch(); });
        field.appendChild(htmlSrc);
        field.appendChild(mkHint('Custom HTML is sanitised on save (scripts, forms, and inline handlers are removed). Use it for rich markup like styled blocks or buttons-as-links.'));
        setTimeout(function () { autoGrow(htmlSrc); }, 0);
        break;
      }
      case 'markdown': {
        var mdSrc = mkTextarea(block.text || '', '## Write in Markdown\\n\\n- lists, **bold**, [links](url), tables…');
        mdSrc.className += ' eblock__code';
        mdSrc.addEventListener('input', function () { block.text = mdSrc.value; autoGrow(mdSrc); touch(); });
        field.appendChild(mdSrc);
        setTimeout(function () { autoGrow(mdSrc); }, 0);
        break;
      }
      case 'audio': {
        var au = mkInput('text', block.url || '', 'Local audio URL, e.g. /media/episode.mp3');
        au.addEventListener('input', function () { block.url = au.value; touch(); });
        var cap = mkInput('text', block.alt || '', 'Caption (optional)');
        cap.addEventListener('input', function () { block.alt = cap.value; touch(); });
        field.appendChild(au);
        field.appendChild(cap);
        field.appendChild(mkHint('Audio must be a local /media file (upload via the Media library). External URLs are ignored for privacy.'));
        break;
      }
      case 'embed': {
        var embedUrl = mkInput('text', block.url || '', 'Paste a URL to unfurl…');
        var embedStatus = document.createElement('span');
        embedStatus.className = 'eblock__embed-status';
        if (block.title) embedStatus.textContent = block.title;
        embedUrl.addEventListener('change', function () {
          var val = embedUrl.value.trim();
          block.url = val;
          if (!val) return;
          embedStatus.textContent = 'Fetching…';
          fetch('/os/api/embed/unfurl', {
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
            touch();
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
          block.items = ta.value.split('\n').filter(function (s) { return s.trim() !== ''; });
          autoGrow(ta);
          touch();
        });
        field.appendChild(ta);
        setTimeout(function () { autoGrow(ta); }, 0);
        break;
      }
      case 'tasklist': {
        field.appendChild(buildTaskList(block, idx));
        break;
      }
      case 'table': {
        field.appendChild(buildTableField(block, idx));
        break;
      }
      case 'toggle': {
        var summary = mkInput('text', block.summary || '', 'Toggle title…');
        summary.className += ' eblock__heading';
        summary.addEventListener('input', function () { block.summary = summary.value; touch(); });
        var body = mkTextarea(block.text || '', 'Hidden content…');
        body.addEventListener('input', function () { block.text = body.value; autoGrow(body); touch(); });
        var openLine = document.createElement('label');
        openLine.className = 'eblock__checkline';
        var openCb = document.createElement('input');
        openCb.type = 'checkbox';
        openCb.checked = !!block.open;
        openCb.addEventListener('change', function () { block.open = openCb.checked; touch(); });
        var openTxt = document.createElement('span');
        openTxt.textContent = 'Expanded by default';
        openLine.appendChild(openCb);
        openLine.appendChild(openTxt);
        field.appendChild(summary);
        field.appendChild(body);
        field.appendChild(openLine);
        setTimeout(function () { autoGrow(body); }, 0);
        break;
      }
      case 'math': {
        var mt = mkTextarea(block.text || '', 'LaTeX or math expression, e.g. E = mc^2');
        mt.className += ' eblock__code';
        mt.addEventListener('input', function () { block.text = mt.value; autoGrow(mt); touch(); });
        field.appendChild(mt);
        field.appendChild(mkHint('Stored verbatim and rendered in a styled block (privacy-first, no external renderer).'));
        setTimeout(function () { autoGrow(mt); }, 0);
        break;
      }
      case 'code': {
        var lang = mkInput('text', block.lang || '', 'Language (e.g. go, js)');
        lang.className += ' eblock__lang';
        lang.addEventListener('input', function () { block.lang = lang.value; touch(); });
        var code = mkTextarea(block.text || '', 'Code…');
        code.className += ' eblock__code';
        code.addEventListener('input', function () { block.text = code.value; autoGrow(code); touch(); });
        field.appendChild(lang);
        field.appendChild(code);
        setTimeout(function () { autoGrow(code); }, 0);
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
          if (!v) { while (dprev.firstChild) dprev.removeChild(dprev.firstChild); return; }
          fetch('/os/api/diagram/preview', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': csrfToken() },
            body: JSON.stringify({ source: v })
          }).then(function (r) { return r.json(); }).then(function (d) {
            if (d.svg) { renderSanitized(dprev, d.svg); dprev.classList.remove('is-error'); }
            else { dprev.textContent = d.error || 'Could not render diagram'; dprev.classList.add('is-error'); }
          }).catch(function () { dprev.textContent = 'Preview failed'; dprev.classList.add('is-error'); });
        };
        dsrc.addEventListener('input', function () {
          block.text = dsrc.value;
          autoGrow(dsrc);
          touch();
          clearTimeout(dtimer);
          dtimer = setTimeout(renderDiag, 400);
        });
        field.appendChild(dsrc);
        field.appendChild(dprev);
        setTimeout(function () { autoGrow(dsrc); }, 0);
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
        lvlSel.addEventListener('change', function () { block.level = parseInt(lvlSel.value, 10); touch(); });
        var ht = mkInput('text', block.text || '', 'Heading…');
        ht.className += ' eblock__heading';
        ht.addEventListener('input', function () { block.text = ht.value; touch(); });
        ht.addEventListener('keydown', onTextKey(idx));
        field.appendChild(lvlSel);
        field.appendChild(ht);
        break;
      }
      default: {
        // paragraph, quote, callout — single text area.
        var t = mkTextarea(block.text || '', placeholderFor(block.type));
        t.addEventListener('input', function () {
          block.text = t.value;
          autoGrow(t);
          if (block.type === 'paragraph' && tryParagraphShortcut(idx, t)) return;
          touch();
        });
        t.addEventListener('keydown', onTextKey(idx));
        if (block.type === 'paragraph') {
          t.addEventListener('paste', function (e) {
            // Pasting a lone URL onto an empty paragraph turns it into a
            // bookmark / embed card and unfurls it (matches the "paste any URL"
            // flow). Anything else pastes normally.
            if (t.value.trim() !== '') return;
            var clip = (e.clipboardData && e.clipboardData.getData('text/plain')) || '';
            clip = clip.trim();
            if (isLoneURL(clip)) {
              e.preventDefault();
              convertBlock(idx, { type: 'embed', url: clip, title: '', description: '', provider: '', thumbURL: '' });
              setTimeout(function () { unfurlEmbedAt(idx, clip); }, 0);
            }
          });
        }
        field.appendChild(t);
        if (block.type === 'callout') {
          var tone = mkInput('text', block.style || 'info', 'Tone (info, warn, success)');
          tone.className += ' eblock__tone';
          tone.addEventListener('input', function () { block.style = tone.value; touch(); });
          field.appendChild(tone);
        }
        setTimeout(function () { autoGrow(t); }, 0);
      }
    }
    return field;
  }

  // ── Task-list editor ────────────────────────────────────────────────────────
  function buildTaskList(block, idx) {
    block.items = block.items || [];
    block.checked = block.checked || [];
    if (!block.items.length) { block.items = ['']; block.checked = [false]; }
    var wrap = document.createElement('div');
    wrap.className = 'eblock__tasks';
    block.items.forEach(function (it, i) {
      var row = document.createElement('div');
      row.className = 'eblock__task' + (block.checked[i] ? ' is-done' : '');
      var cb = document.createElement('input');
      cb.type = 'checkbox';
      cb.className = 'eblock__task-check';
      cb.checked = !!block.checked[i];
      cb.addEventListener('change', function () {
        block.checked[i] = cb.checked;
        row.classList.toggle('is-done', cb.checked);
        touch();
      });
      var ti = mkInput('text', it, 'To-do…');
      ti.className += ' eblock__task-text';
      ti.addEventListener('input', function () { block.items[i] = ti.value; touch(); });
      ti.addEventListener('keydown', function (e) {
        if (e.key === 'Enter') {
          e.preventDefault();
          block.items.splice(i + 1, 0, '');
          block.checked.splice(i + 1, 0, false);
          structural();
          setTimeout(function () { focusTaskItem(idx, i + 1); }, 0);
        } else if (e.key === 'Backspace' && ti.value === '' && block.items.length > 1) {
          e.preventDefault();
          block.items.splice(i, 1);
          block.checked.splice(i, 1);
          structural();
          setTimeout(function () { focusTaskItem(idx, Math.max(0, i - 1)); }, 0);
        }
      });
      row.appendChild(cb);
      row.appendChild(ti);
      wrap.appendChild(row);
    });
    var add = document.createElement('button');
    add.type = 'button';
    add.className = 'eblock__addrow';
    add.textContent = '+ Add item';
    add.addEventListener('click', function () {
      block.items.push('');
      block.checked.push(false);
      structural();
      setTimeout(function () { focusTaskItem(idx, block.items.length - 1); }, 0);
    });
    wrap.appendChild(add);
    return wrap;
  }

  function focusTaskItem(idx, i) {
    var wrap = canvas.querySelector('[data-block-idx="' + idx + '"]');
    if (!wrap) return;
    var items = wrap.querySelectorAll('.eblock__task-text');
    if (items[i]) {
      items[i].focus();
      try { var n = items[i].value.length; items[i].setSelectionRange(n, n); } catch (e) {}
    }
  }

  // ── Table editor ──────────────────────────────────────────────────────────
  function buildTableField(block, idx) {
    block.header = block.header || [];
    block.rows = block.rows || [];
    if (!block.header.length && !block.rows.length) {
      block.header = ['Column 1', 'Column 2'];
      block.rows = [['', ''], ['', '']];
    }
    var wrap = document.createElement('div');
    wrap.className = 'eblock__table';
    var table = document.createElement('table');
    table.className = 'eblock__table-grid';

    if (block.header.length) {
      var thead = document.createElement('thead');
      var htr = document.createElement('tr');
      block.header.forEach(function (h, c) {
        var th = document.createElement('th');
        var inp = mkInput('text', h, 'Header');
        inp.addEventListener('input', function () { block.header[c] = inp.value; touch(); });
        th.appendChild(inp);
        htr.appendChild(th);
      });
      htr.appendChild(document.createElement('th')); // spacer for row controls
      thead.appendChild(htr);
      table.appendChild(thead);
    }

    var tbody = document.createElement('tbody');
    block.rows.forEach(function (row, r) {
      var tr = document.createElement('tr');
      row.forEach(function (cell, c) {
        var td = document.createElement('td');
        var inp = mkInput('text', cell, 'Cell');
        inp.addEventListener('input', function () { block.rows[r][c] = inp.value; touch(); });
        td.appendChild(inp);
        tr.appendChild(td);
      });
      var ctlTd = document.createElement('td');
      ctlTd.className = 'eblock__table-rowctl';
      var del = mkCtrlBtn('×', 'Delete row', function () { block.rows.splice(r, 1); structural(); });
      ctlTd.appendChild(del);
      tr.appendChild(ctlTd);
      tbody.appendChild(tr);
    });
    table.appendChild(tbody);
    wrap.appendChild(table);

    var ctl = document.createElement('div');
    ctl.className = 'eblock__table-ctl';
    var addRow = mkSmallBtn('+ Row', function () {
      var cols = block.header.length || (block.rows[0] ? block.rows[0].length : 2);
      var nr = [];
      for (var i = 0; i < cols; i++) nr.push('');
      block.rows.push(nr);
      structural();
    });
    var addCol = mkSmallBtn('+ Column', function () {
      if (!block.header.length) {
        var cols = block.rows[0] ? block.rows[0].length : 0;
        for (var i = 0; i < cols; i++) block.header.push('Column ' + (i + 1));
      }
      block.header.push('Column ' + (block.header.length + 1));
      block.rows.forEach(function (rw) { rw.push(''); });
      structural();
    });
    ctl.appendChild(addRow);
    ctl.appendChild(addCol);
    wrap.appendChild(ctl);
    return wrap;
  }

  function mkSmallBtn(label, onClick) {
    var b = document.createElement('button');
    b.type = 'button';
    b.className = 'btn btn--ghost btn--xs';
    b.textContent = label;
    b.addEventListener('click', onClick);
    return b;
  }

  // uploadInto uploads a file to the media library and calls cb(url) on success.
  // Shared by the image card's Upload button and the gallery editor.
  function uploadInto(file, cb) {
    var fd = new FormData();
    fd.append('file', file);
    setStatus('Uploading image…');
    fetch('/os/api/media/upload', { method: 'POST', headers: { 'X-CSRF-Token': csrfToken() }, body: fd })
      .then(function (r) { return r.ok ? r.json() : Promise.reject(r.status); })
      .then(function (d) {
        if (d && d.url) { cb(d.url); setStatus('Image uploaded', 'ok'); }
        else setStatus('Upload failed', 'danger');
      }).catch(function () { setStatus('Image upload failed', 'danger'); });
  }

  // isLoneURL reports whether s is a single bare http(s) URL (no surrounding text).
  function isLoneURL(s) {
    return /^https?:\/\/\S+$/.test(s) && !/\s/.test(s);
  }

  // unfurlEmbedAt resolves a URL's metadata server-side and fills the embed block
  // at idx (used by the slash "Embed" card and by auto-embed on URL paste).
  function unfurlEmbedAt(idx, url) {
    var b = blocks[idx];
    if (!b || b.type !== 'embed') return;
    setStatus('Fetching…');
    fetch('/os/api/embed/unfurl', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': csrfToken() },
      body: JSON.stringify({ url: url })
    }).then(function (r) { return r.json(); }).then(function (data) {
      if (data.error) { setStatus('Embed: ' + data.error, 'warn'); return; }
      b.url = data.url || url;
      b.title = data.title || '';
      b.description = data.description || '';
      b.provider = data.provider || '';
      b.thumbURL = data.thumbURL || '';
      b.kind = data.kind || 'link';
      b.embedSrc = data.embedSrc || '';
      structural();
      setStatus('Embed added', 'ok');
    }).catch(function () { setStatus('Could not fetch URL', 'danger'); });
  }

  // ── Gallery editor (up to 9 images) ─────────────────────────────────────────
  // safeMediaURL is a strict allowlist for any URL that will be assigned to an
  // <img> src or stored in the document: it permits http(s), protocol-relative
  // (//host), site-relative (/path), and scheme-less relative paths, and rejects
  // everything else — notably javascript:, data:, and vbscript: URLs — by
  // returning ''. Control characters (which can smuggle a scheme past the check,
  // e.g. "java\tscript:") are rejected outright. This is the client-side barrier;
  // the server (blockrender) independently escapes/sanitises on render.
  function safeMediaURL(u) {
    u = (u == null ? '' : String(u)).trim();
    if (!u) return '';
    if (/[\u0000-\u001F\u007F]/.test(u)) return '';      // control chars
    if (u.charAt(0) === '/') return u;                   // /path or //host
    if (/^\.{1,2}\//.test(u)) return u;                  // ./x or ../x
    if (/^[^:]+$/.test(u)) return u;                     // relative, no scheme
    return /^https?:\/\//i.test(u) ? u : '';             // explicit http(s) only
  }

  function buildGalleryField(block, idx) {
    block.images = block.images || [];
    var wrap = document.createElement('div');
    wrap.className = 'eblock__gallery';

    var grid = document.createElement('div');
    grid.className = 'eblock__gallery-grid';
    block.images.forEach(function (src, i) {
      var cell = document.createElement('div');
      cell.className = 'eblock__gallery-cell';
      var img = document.createElement('img');
      img.src = safeMediaURL(src);
      img.alt = '';
      var del = document.createElement('button');
      del.type = 'button';
      del.className = 'eblock__gallery-del';
      del.textContent = '×';
      del.title = 'Remove image';
      del.addEventListener('click', function () { block.images.splice(i, 1); structural(); });
      cell.appendChild(img);
      cell.appendChild(del);
      grid.appendChild(cell);
    });
    wrap.appendChild(grid);

    var ctl = document.createElement('div');
    ctl.className = 'eblock__row';
    var addUrl = mkInput('text', '', 'Image URL, then press Add');
    var addBtn = mkSmallBtn('+ Add URL', function () {
      var v = safeMediaURL(addUrl.value);
      if (!v) { setStatus('Enter a valid image URL (http(s) or a /media path)', 'warn'); return; }
      if (block.images.length >= 9) { setStatus('A gallery holds up to 9 images', 'warn'); return; }
      block.images.push(v);
      addUrl.value = '';
      structural();
    });
    var upBtn = mkSmallBtn('⬆ Upload', function () { gFile.click(); });
    var gFile = document.createElement('input');
    gFile.type = 'file';
    gFile.accept = 'image/*';
    gFile.multiple = true;
    gFile.style.display = 'none';
    gFile.addEventListener('change', function () {
      var files = gFile.files ? Array.prototype.slice.call(gFile.files) : [];
      files.forEach(function (f) {
        if (block.images.length >= 9) return;
        uploadInto(f, function (u) { var s = safeMediaURL(u); if (s && block.images.length < 9) { block.images.push(s); structural(); } });
      });
      gFile.value = '';
    });
    ctl.appendChild(addUrl);
    ctl.appendChild(addBtn);
    ctl.appendChild(upBtn);
    ctl.appendChild(gFile);
    wrap.appendChild(ctl);

    var cap = mkInput('text', block.caption || '', 'Gallery caption (optional)');
    cap.addEventListener('input', function () { block.caption = cap.value; touch(); });
    wrap.appendChild(cap);
    wrap.appendChild(mkHint('Up to 9 images, shown as a responsive grid. Drag images into the editor or use Upload.'));
    return wrap;
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

  function mkHint(text) {
    var h = document.createElement('div');
    h.className = 'eblock__hint text-xs muted';
    h.textContent = text;
    return h;
  }

  function autoGrow(t) {
    t.style.height = 'auto';
    t.style.height = (t.scrollHeight) + 'px';
  }

  function focusBlock(idx, atEnd) {
    var wrap = canvas.querySelector('[data-block-idx="' + idx + '"]');
    if (!wrap) return;
    var f = wrap.querySelector('.eblock__heading, .eblock__text, .eblock__input');
    if (!f) return;
    f.focus();
    if (atEnd && typeof f.setSelectionRange === 'function') {
      try { var n = f.value.length; f.setSelectionRange(n, n); } catch (e) {}
    }
  }

  function convertBlock(idx, newBlock) {
    blocks[idx] = newBlock;
    structural();
    setTimeout(function () { focusBlock(idx, true); }, 0);
  }

  // tryParagraphShortcut turns leading Markdown markers in a paragraph into the
  // matching block as soon as the trigger space is typed.
  function tryParagraphShortcut(idx, el) {
    var v = el.value;
    var m;
    if ((m = /^[-*]\s\[( |x|X)\]\s(.*)$/.exec(v))) {
      convertBlock(idx, { type: 'tasklist', items: [m[2] || ''], checked: [/x/i.test(m[1])] });
      return true;
    }
    if ((m = /^(#{1,4})\s(.*)$/.exec(v))) {
      var lvl = Math.min(4, Math.max(2, m[1].length));
      convertBlock(idx, { type: 'heading', level: lvl, text: m[2] });
      return true;
    }
    if ((m = /^[-*]\s(.*)$/.exec(v))) {
      convertBlock(idx, { type: 'list', style: 'unordered', items: m[1] ? [m[1]] : [] });
      return true;
    }
    if ((m = /^1\.\s(.*)$/.exec(v))) {
      convertBlock(idx, { type: 'list', style: 'ordered', items: m[1] ? [m[1]] : [] });
      return true;
    }
    if ((m = /^>\s(.*)$/.exec(v))) {
      convertBlock(idx, { type: 'quote', text: m[1] });
      return true;
    }
    if ((m = /^```(\w*)$/.exec(v))) {
      convertBlock(idx, { type: 'code', lang: m[1] || '', text: '' });
      return true;
    }
    if (/^---\s?$/.test(v)) {
      blocks[idx] = { type: 'divider' };
      blocks.splice(idx + 1, 0, { type: 'paragraph', text: '' });
      structural();
      setTimeout(function () { focusBlock(idx + 1, true); }, 0);
      return true;
    }
    return false;
  }

  // onTextKey gives text blocks a continuous, document-like writing flow.
  function onTextKey(idx) {
    return function (e) {
      var el = e.target;
      if (e.key === '/' && el.value === '') {
        e.preventDefault();
        openPalette(idx + 1, idx);
        return;
      }
      if (e.key === 'Enter' && !e.shiftKey) {
        e.preventDefault();
        insertBlock(idx + 1, { type: 'paragraph', text: '' });
        setTimeout(function () { focusBlock(idx + 1, true); }, 0);
        return;
      }
      if (e.key === 'Backspace' && el.selectionStart === 0 && el.selectionEnd === 0 &&
          el.value === '' && blocks.length > 1) {
        e.preventDefault();
        var prev = idx - 1;
        removeBlock(idx);
        if (prev >= 0) setTimeout(function () { focusBlock(prev, true); }, 0);
        return;
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
    structural();
  }

  function removeBlock(idx) {
    blocks.splice(idx, 1);
    if (!blocks.length) blocks = [{ type: 'paragraph', text: '' }];
    structural();
  }

  // moveBlock relocates the block at `from` to insertion index `to` (in the
  // pre-move array). Used by drag-and-drop.
  function moveBlock(from, to) {
    if (from < 0 || from >= blocks.length) return;
    var b = blocks.splice(from, 1)[0];
    if (from < to) to--;
    if (to < 0) to = 0;
    if (to > blocks.length) to = blocks.length;
    blocks.splice(to, 0, b);
    structural();
  }

  // nudge swaps a block with its neighbour (keyboard / button reorder).
  function nudge(idx, dir) {
    var j = idx + dir;
    if (j < 0 || j >= blocks.length) return;
    var tmp = blocks[idx];
    blocks[idx] = blocks[j];
    blocks[j] = tmp;
    structural();
    setTimeout(function () { focusBlock(j, true); }, 0);
  }

  function newBlockOf(type) {
    if (type === 'ordered') return { type: 'list', style: 'ordered', items: [] };
    if (type === 'list') return { type: 'list', style: 'unordered', items: [] };
    if (type === 'tasklist') return { type: 'tasklist', items: [''], checked: [false] };
    if (type === 'heading') return { type: 'heading', level: 2, text: '' };
    if (type === 'divider') return { type: 'divider' };
    if (type === 'image') return { type: 'image', url: '', alt: '', caption: '', width: '' };
    if (type === 'gallery') return { type: 'gallery', images: [], caption: '' };
    if (type === 'audio') return { type: 'audio', url: '', alt: '' };
    if (type === 'embed') return { type: 'embed', url: '', title: '', description: '', provider: '', thumbURL: '' };
    if (type === 'code') return { type: 'code', lang: '', text: '' };
    if (type === 'html') return { type: 'html', text: '' };
    if (type === 'markdown') return { type: 'markdown', text: '' };
    if (type === 'diagram') return { type: 'diagram', text: '' };
    if (type === 'callout') return { type: 'callout', style: 'info', text: '' };
    if (type === 'table') return { type: 'table', header: ['Column 1', 'Column 2'], rows: [['', ''], ['', '']] };
    if (type === 'toggle') return { type: 'toggle', summary: '', text: '', open: false };
    if (type === 'math') return { type: 'math', text: '' };
    return { type: 'paragraph', text: '' };
  }

  // ── Slash / command palette (categorised, keyboard-navigable) ───────────────
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

    var search = document.createElement('input');
    search.type = 'text';
    search.className = 'block-palette__search';
    search.placeholder = 'Search blocks…  (↑↓ navigate · Enter insert · Esc close)';
    paletteEl.appendChild(search);

    var list = document.createElement('div');
    list.className = 'block-palette__list';

    CATEGORIES.forEach(function (cat) {
      var inCat = BLOCK_TYPES.filter(function (b) { return b.cat === cat; });
      if (!inCat.length) return;
      var sep = document.createElement('div');
      sep.className = 'block-palette__sep';
      sep.textContent = cat;
      list.appendChild(sep);
      inCat.forEach(function (bt) {
        var item = makePaletteItem(bt.icon, bt.label, bt.hint, function () {
          insertBlock(insertAt, newBlockOf(bt.type));
          setTimeout(function () { focusBlock(insertAt, true); }, 0);
        });
        item.setAttribute('data-search', (bt.label + ' ' + bt.hint + ' ' + bt.type).toLowerCase());
        list.appendChild(item);
      });
    });

    if (aiEnabled && sourceBlockIdx != null && sourceBlockIdx >= 0 && sourceBlockIdx < blocks.length) {
      var aisep = document.createElement('div');
      aisep.className = 'block-palette__sep';
      aisep.textContent = 'AI assist';
      list.appendChild(aisep);
      var srcText = blockText(blocks[sourceBlockIdx]);
      AI_CMDS.forEach(function (cmd) {
        var item = makePaletteItem(cmd.icon, cmd.label, cmd.hint, function () {
          runAI(cmd.op, srcText, insertAt);
        });
        item.setAttribute('data-search', (cmd.label + ' ' + cmd.hint).toLowerCase());
        list.appendChild(item);
      });
    }

    paletteEl.appendChild(list);

    var selIdx = -1;
    function visibleItems() {
      return Array.prototype.filter.call(
        list.querySelectorAll('.block-palette__item'),
        function (it) { return it.style.display !== 'none'; });
    }
    function highlight() {
      var items = visibleItems();
      items.forEach(function (it, i) { it.classList.toggle('is-active', i === selIdx); });
      if (selIdx >= 0 && items[selIdx] && items[selIdx].scrollIntoView) {
        items[selIdx].scrollIntoView({ block: 'nearest' });
      }
    }
    function applyFilter() {
      var q = search.value.trim().toLowerCase();
      list.querySelectorAll('.block-palette__item').forEach(function (it) {
        var hay = it.getAttribute('data-search') || '';
        it.style.display = (!q || hay.indexOf(q) !== -1) ? '' : 'none';
      });
      // Hide category headers that have no visible items beneath them.
      list.querySelectorAll('.block-palette__sep').forEach(function (sep) {
        var anyVisible = false, n = sep.nextSibling;
        while (n) {
          if (n.classList && n.classList.contains('block-palette__sep')) break;
          if (n.classList && n.classList.contains('block-palette__item') && n.style.display !== 'none') { anyVisible = true; break; }
          n = n.nextSibling;
        }
        sep.style.display = anyVisible ? '' : 'none';
      });
      selIdx = visibleItems().length ? 0 : -1;
      highlight();
    }
    search.addEventListener('input', applyFilter);
    search.addEventListener('keydown', function (e) {
      var items = visibleItems();
      if (e.key === 'ArrowDown') { e.preventDefault(); if (items.length) { selIdx = (selIdx + 1) % items.length; highlight(); } }
      else if (e.key === 'ArrowUp') { e.preventDefault(); if (items.length) { selIdx = (selIdx - 1 + items.length) % items.length; highlight(); } }
      else if (e.key === 'Enter') { e.preventDefault(); var t = items[selIdx >= 0 ? selIdx : 0]; if (t) t.click(); }
      else if (e.key === 'Escape') { closePalette(); }
    });

    document.body.appendChild(paletteEl);
    applyFilter();
    setTimeout(function () { search.focus(); }, 0);
    document.addEventListener('keydown', escClose);
    setTimeout(function () { document.addEventListener('click', outsideClose); }, 0);
  }

  function escClose(e) { if (e.key === 'Escape') closePalette(); }
  function outsideClose(e) { if (paletteEl && !paletteEl.contains(e.target)) closePalette(); }
  function closePalette() {
    if (paletteEl && paletteEl.parentNode) paletteEl.parentNode.removeChild(paletteEl);
    paletteEl = null;
    document.removeEventListener('keydown', escClose);
    document.removeEventListener('click', outsideClose);
  }

  // ── AI assist ─────────────────────────────────────────────────────────────
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
      if (overlay.parentNode) overlay.parentNode.removeChild(overlay);
    });

    var discard = document.createElement('button');
    discard.type = 'button';
    discard.className = 'btn btn--ghost btn--xs';
    discard.textContent = 'Discard';
    discard.addEventListener('click', function () {
      if (overlay.parentNode) overlay.parentNode.removeChild(overlay);
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

  function renderWordDiff(current, old) {
    var pre = document.createElement('div');
    pre.className = 'history-diff__view';
    var oldWords = old.split(/\s+/).filter(Boolean);
    var newWords = current.split(/\s+/).filter(Boolean);
    var ops = lcsWordDiff(oldWords, newWords);
    ops.forEach(function (op) {
      var span = document.createElement('span');
      span.className = op.kind === 'del' ? 'diff-del' : op.kind === 'ins' ? 'diff-ins' : 'diff-eq';
      span.textContent = op.word + ' ';
      pre.appendChild(span);
    });
    var heading = document.createElement('div');
    heading.className = 'history-diff__title text-xs muted';
    heading.textContent = 'Current ↔ selected version (word-level diff)';
    historyDiff.appendChild(heading);
    historyDiff.appendChild(pre);
  }

  function lcsWordDiff(oldW, newW) {
    var m = oldW.length, n = newW.length;
    var maxW = 300;
    if (m > maxW || n > maxW) {
      var ops = [];
      oldW.slice(0, maxW).forEach(function (w) { ops.push({ kind: 'del', word: w }); });
      newW.slice(0, maxW).forEach(function (w) { ops.push({ kind: 'ins', word: w }); });
      return ops;
    }
    var dp = [];
    for (var i = 0; i <= m; i++) dp[i] = new Array(n + 1).fill(0);
    for (var ii = 1; ii <= m; ii++) {
      for (var jj = 1; jj <= n; jj++) {
        dp[ii][jj] = oldW[ii - 1] === newW[jj - 1] ? dp[ii - 1][jj - 1] + 1 : Math.max(dp[ii - 1][jj], dp[ii][jj - 1]);
      }
    }
    var result = [];
    var a = m, b = n;
    while (a > 0 || b > 0) {
      if (a > 0 && b > 0 && oldW[a - 1] === newW[b - 1]) { result.unshift({ kind: 'eq', word: oldW[a - 1] }); a--; b--; }
      else if (b > 0 && (a === 0 || dp[a][b - 1] >= dp[a - 1][b])) { result.unshift({ kind: 'ins', word: newW[b - 1] }); b--; }
      else { result.unshift({ kind: 'del', word: oldW[a - 1] }); a--; }
    }
    return result;
  }

  // ── Persistence ────────────────────────────────────────────────────────────
  function csrfToken() {
    var m = document.cookie.match(/(?:^|;\s*)vp_csrf=([^;]+)/);
    return m ? decodeURIComponent(m[1]) : '';
  }

  function payload() {
    var p = { slug: slug, title: titleEl ? titleEl.value : '', blocks: blocks };
    // Only send publishing-options fields once the panel has hydrated, so an
    // older/incomplete shell can never accidentally clear tags or metadata.
    if (metaHydrated) {
      p.tags = pmTags.slice();
      p.publishDate = pm['publish-date'] ? pm['publish-date'].value : '';
      p.meta = collectMeta();
    }
    return JSON.stringify(p);
  }

  function save() {
    if (!slug && (!titleEl || !titleEl.value.trim())) {
      setStatus('Add a title before saving', 'warn');
      if (titleEl) titleEl.focus();
      return;
    }
    // In HTML mode the source is authoritative — parse it into blocks first so
    // the save reflects the operator's raw edits.
    if (htmlMode) { applyHTMLSource().then(performSave).catch(function () { setStatus('Could not parse HTML', 'danger'); }); return; }
    performSave();
  }

  function performSave() {
    setStatus('Saving…');
    fetch('/os/api/editor/save', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': csrfToken() },
      body: payload()
    }).then(function (r) {
      if (!r.ok) throw new Error('save failed (' + r.status + ')');
      return r.json();
    }).then(function (data) {
      if (!slug && data && data.slug) {
        slug = data.slug;
        if (root) root.setAttribute('data-slug', slug);
        try { history.replaceState({}, '', '/os/editor/' + encodeURIComponent(slug)); } catch (e) {}
        if (typeof pmSet === 'function') { pmSet(pm['slug'], slug); syncSlugUI(); }
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

  // ── Preview (modal + split live pane) ───────────────────────────────────────
  // The server returns UGC-sanitised HTML (blockrender). To display rendered
  // markup we must reinterpret a string as HTML — every sink is funnelled through
  // DOMPurify.sanitize; if DOMPurify is unavailable we degrade to inert text.
  function renderSanitized(target, rawHTML) {
    if (window.DOMPurify && typeof window.DOMPurify.sanitize === 'function') {
      target.innerHTML = window.DOMPurify.sanitize(rawHTML);
    } else {
      target.textContent = rawHTML;
    }
  }

  function fetchPreview() {
    return fetch('/os/api/editor/preview', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': csrfToken() },
      body: JSON.stringify({ blocks: blocks })
    }).then(function (r) { return r.json(); });
  }

  function preview() {
    if (htmlMode) { applyHTMLSource().then(doPreview).catch(function () { setStatus('Preview failed', 'danger'); }); return; }
    doPreview();
  }
  function doPreview() {
    setStatus('Rendering preview…');
    fetchPreview().then(function (data) {
      renderSanitized(previewBody, data.html || '');
      previewModal.hidden = false;
      setStatus('Ready');
    }).catch(function () { setStatus('Preview failed', 'danger'); });
  }

  var splitOn = false, livePreviewTimer = null;
  function toggleSplit() {
    if (htmlMode) { exitHTMLMode(); }
    splitOn = !splitOn;
    root.classList.toggle('is-split', splitOn);
    if (liveEl) liveEl.hidden = !splitOn;
    if (splitBtn) splitBtn.classList.toggle('is-active', splitOn);
    if (splitOn) renderLivePreview();
  }
  function scheduleLivePreview() {
    if (!splitOn) return;
    if (livePreviewTimer) clearTimeout(livePreviewTimer);
    livePreviewTimer = setTimeout(renderLivePreview, 500);
  }
  function renderLivePreview() {
    if (!liveBody) return;
    fetchPreview().then(function (data) {
      renderSanitized(liveBody, data.html || '');
    }).catch(function () {});
  }

  // ── Focus (distraction-free) mode ───────────────────────────────────────────
  var focusOn = false;
  function toggleFocus() {
    focusOn = !focusOn;
    root.classList.toggle('is-focus', focusOn);
    if (focusBtn) focusBtn.classList.toggle('is-active', focusOn);
  }

  // ── HTML source mode (one-click round-trip) ────────────────────────────────
  // The HTML editor lets an operator edit the rendered HTML directly. Entering
  // it asks the server to render the current blocks to sanitised HTML; leaving
  // it parses that HTML back into blocks (server-side importer, which preserves
  // inline formatting as Markdown). A visual → HTML → visual round-trip is
  // therefore lossless for common formatting. Saving while in HTML mode applies
  // the source first so no edit is lost.
  var htmlMode = false, htmlBusy = false;
  function enterHTMLMode() {
    if (!htmlPanel || !htmlArea || htmlBusy) return;
    htmlBusy = true;
    setStatus('Loading HTML…');
    // HTML mode and split preview are mutually exclusive.
    if (splitOn) toggleSplit();
    fetchPreview().then(function (data) {
      htmlArea.value = data.html || '';
      htmlMode = true;
      root.classList.add('is-html');
      htmlPanel.hidden = false;
      if (htmlBtn) { htmlBtn.classList.add('is-active'); htmlBtn.setAttribute('aria-pressed', 'true'); }
      setStatus('Editing HTML', 'ok');
      setTimeout(function () { htmlArea.focus(); }, 0);
      htmlBusy = false;
    }).catch(function () {
      setStatus('Could not load HTML', 'danger');
      htmlBusy = false;
    });
  }
  // applyHTMLSource parses the textarea HTML into blocks and returns a Promise
  // that resolves once `blocks` has been replaced. Used on exit and before save.
  function applyHTMLSource() {
    if (!htmlArea) return Promise.resolve();
    return fetch('/os/api/editor/import', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': csrfToken() },
      body: JSON.stringify({ html: htmlArea.value })
    }).then(function (r) { return r.ok ? r.json() : Promise.reject(r.status); })
      .then(function (d) {
        var next = (d && d.blocks) || [];
        blocks = (Array.isArray(next) && next.length) ? next : [{ type: 'paragraph', text: '' }];
      });
  }
  function exitHTMLMode() {
    if (!htmlMode || htmlBusy) { return Promise.resolve(); }
    htmlBusy = true;
    setStatus('Applying HTML…');
    return applyHTMLSource().then(function () {
      htmlMode = false;
      root.classList.remove('is-html');
      if (htmlPanel) htmlPanel.hidden = true;
      if (htmlBtn) { htmlBtn.classList.remove('is-active'); htmlBtn.setAttribute('aria-pressed', 'false'); }
      renderCanvas();
      commitNow();
      updateStats();
      scheduleAutosave();
      scheduleLivePreview();
      setStatus('Applied', 'ok');
      htmlBusy = false;
    }).catch(function () {
      setStatus('Could not parse HTML', 'danger');
      htmlBusy = false;
    });
  }
  function toggleHTML() {
    if (htmlMode) { exitHTMLMode(); } else { enterHTMLMode(); }
  }

  // ── Post settings panel (publishing options) ────────────────────────────────
  // A slide-out drawer for everything that is not the post body: feature image,
  // URL slug, publish date, excerpt, tags, SEO meta, social cards, and the
  // featured / page flags. Values hydrate from the <script id="vp-editor-meta">
  // document and are sent back inside the save payload (tags + publishDate +
  // meta). The slug is renamed out-of-band via /os/api/editor/slug so it never
  // races the queued content write.
  var settingsBtn = root.querySelector('[data-editor-settings-btn]');
  var settingsPanel = root.querySelector('[data-editor-settings]');
  var settingsBackdrop = root.querySelector('[data-editor-settings-backdrop]');
  var settingsClose = root.querySelector('[data-editor-settings-close]');
  var pm = {};
  ['feature-image', 'feature-preview', 'feature-empty', 'feature-upload', 'feature-remove', 'feature-file',
    'slug', 'slug-apply', 'slug-prefix', 'slug-status', 'publish-date', 'excerpt', 'excerpt-count',
    'tags-input', 'tags-list', 'featured', 'is-page',
    'meta-title', 'meta-title-count', 'meta-description', 'meta-description-count', 'canonical',
    'og-title', 'og-description', 'og-image', 'twitter-title', 'twitter-description', 'twitter-image'
  ].forEach(function (k) { pm[k] = root.querySelector('[data-pm-' + k + ']'); });
  var pmTags = [];
  var metaHydrated = false;

  function pmVal(el) { return el ? (el.value || '') : ''; }
  function pmSet(el, v) { if (el) el.value = v == null ? '' : String(v); }

  function hydrateSettings() {
    var el = document.getElementById('vp-editor-meta');
    if (!el) return;
    var data = {};
    try { data = JSON.parse(el.textContent.trim() || '{}'); } catch (e) { data = {}; }
    metaHydrated = true;
    pmTags = Array.isArray(data.tags) ? data.tags.slice() : [];
    pmSet(pm['excerpt'], data.excerpt);
    pmSet(pm['feature-image'], data.featureImage);
    pmSet(pm['meta-title'], data.metaTitle);
    pmSet(pm['meta-description'], data.metaDescription);
    pmSet(pm['canonical'], data.canonicalURL);
    pmSet(pm['og-title'], data.ogTitle);
    pmSet(pm['og-description'], data.ogDescription);
    pmSet(pm['og-image'], data.ogImage);
    pmSet(pm['twitter-title'], data.twitterTitle);
    pmSet(pm['twitter-description'], data.twitterDescription);
    pmSet(pm['twitter-image'], data.twitterImage);
    pmSet(pm['publish-date'], data.publishDate);
    pmSet(pm['slug'], data.slug || slug);
    if (pm['featured']) pm['featured'].checked = !!data.featured;
    if (pm['is-page']) pm['is-page'].checked = !!data.isPage;
    syncSlugUI();
    updateFeaturePreview();
    renderTags();
    updateCounters();
  }

  // collectMeta mirrors the server PostMeta JSON tags.
  function collectMeta() {
    return {
      excerpt: pmVal(pm['excerpt']),
      featureImage: pmVal(pm['feature-image']),
      metaTitle: pmVal(pm['meta-title']),
      metaDescription: pmVal(pm['meta-description']),
      canonicalURL: pmVal(pm['canonical']),
      ogTitle: pmVal(pm['og-title']),
      ogDescription: pmVal(pm['og-description']),
      ogImage: pmVal(pm['og-image']),
      twitterTitle: pmVal(pm['twitter-title']),
      twitterDescription: pmVal(pm['twitter-description']),
      twitterImage: pmVal(pm['twitter-image']),
      featured: !!(pm['featured'] && pm['featured'].checked),
      isPage: !!(pm['is-page'] && pm['is-page'].checked)
    };
  }

  function updateCounters() {
    if (pm['excerpt'] && pm['excerpt-count']) pm['excerpt-count'].textContent = String(pmVal(pm['excerpt']).length);
    if (pm['meta-title'] && pm['meta-title-count']) pm['meta-title-count'].textContent = String(pmVal(pm['meta-title']).length);
    if (pm['meta-description'] && pm['meta-description-count']) pm['meta-description-count'].textContent = String(pmVal(pm['meta-description']).length);
  }

  function updateFeaturePreview() {
    var url = pmVal(pm['feature-image']).trim();
    if (pm['feature-preview']) {
      var safe = safeMediaURL(url);
      if (safe) { pm['feature-preview'].src = safe; pm['feature-preview'].hidden = false; }
      else { pm['feature-preview'].hidden = true; pm['feature-preview'].removeAttribute('src'); }
    }
    if (pm['feature-empty']) pm['feature-empty'].hidden = !!url;
  }

  function uploadFeatureImage(file) {
    var fd = new FormData();
    fd.append('file', file);
    setStatus('Uploading image…');
    fetch('/os/api/media/upload', { method: 'POST', headers: { 'X-CSRF-Token': csrfToken() }, body: fd })
      .then(function (r) { return r.ok ? r.json() : Promise.reject(r.status); })
      .then(function (d) {
        if (d && d.url) { pmSet(pm['feature-image'], d.url); updateFeaturePreview(); setStatus('Feature image set', 'ok'); scheduleAutosave(); }
        else setStatus('Upload failed', 'danger');
      }).catch(function () { setStatus('Feature image upload failed', 'danger'); });
  }

  // Tags chip editor.
  function renderTags() {
    if (!pm['tags-list']) return;
    while (pm['tags-list'].firstChild) pm['tags-list'].removeChild(pm['tags-list'].firstChild);
    pmTags.forEach(function (t, i) {
      var chip = document.createElement('span');
      chip.className = 'pm-tag';
      var label = document.createElement('span');
      label.textContent = t;
      var x = document.createElement('button');
      x.type = 'button';
      x.className = 'pm-tag-x';
      x.textContent = '×';
      x.setAttribute('aria-label', 'Remove tag ' + t);
      x.addEventListener('click', function () { pmTags.splice(i, 1); renderTags(); scheduleAutosave(); });
      chip.appendChild(label);
      chip.appendChild(x);
      pm['tags-list'].appendChild(chip);
    });
  }
  function addTagsFromInput() {
    if (!pm['tags-input']) return;
    var raw = pm['tags-input'].value || '';
    raw.split(',').forEach(function (part) {
      part = part.trim();
      if (part && pmTags.indexOf(part) < 0) pmTags.push(part);
    });
    pm['tags-input'].value = '';
    renderTags();
    scheduleAutosave();
  }

  // Slug rename (out-of-band; only for saved posts).
  function syncSlugUI() {
    var saved = !!slug;
    if (pm['slug-apply']) pm['slug-apply'].disabled = !saved;
    if (pm['slug-status']) {
      pm['slug-status'].textContent = saved
        ? 'Changing the URL updates every link to this post.'
        : 'The slug is set automatically from the title on first save.';
    }
  }
  function applySlug() {
    if (!slug) { setStatus('Save the post before changing its URL', 'warn'); return; }
    var next = (pmVal(pm['slug']) || '').trim();
    if (!next || next === slug) return;
    setStatus('Updating URL…');
    fetch('/os/api/editor/slug', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': csrfToken() },
      body: JSON.stringify({ slug: slug, newSlug: next })
    }).then(function (r) { return r.ok ? r.json() : Promise.reject(r.status); })
      .then(function (d) {
        if (d && d.slug) {
          slug = d.slug;
          if (root) root.setAttribute('data-slug', slug);
          pmSet(pm['slug'], slug);
          try { history.replaceState({}, '', '/os/editor/' + encodeURIComponent(slug)); } catch (e) {}
          setStatus('URL updated · /' + slug, 'ok');
        }
      }).catch(function () { setStatus('Could not update URL', 'danger'); });
  }

  function openSettings() {
    if (!settingsPanel) return;
    settingsPanel.hidden = false;
    if (settingsBackdrop) settingsBackdrop.hidden = false;
    root.classList.add('is-settings');
    if (settingsBtn) { settingsBtn.classList.add('is-active'); settingsBtn.setAttribute('aria-pressed', 'true'); }
  }
  function closeSettings() {
    if (!settingsPanel) return;
    settingsPanel.hidden = true;
    if (settingsBackdrop) settingsBackdrop.hidden = true;
    root.classList.remove('is-settings');
    if (settingsBtn) { settingsBtn.classList.remove('is-active'); settingsBtn.setAttribute('aria-pressed', 'false'); }
  }
  function toggleSettings() {
    if (settingsPanel && settingsPanel.hidden) openSettings(); else closeSettings();
  }

  // ── Inline formatting toolbar (floating on text selection) ──────────────────
  var fmtBar = null;
  function fmtTarget() {
    var el = document.activeElement;
    if (!el || !canvas.contains(el)) return null;
    if (el.tagName === 'TEXTAREA' && el.classList.contains('eblock__text') && !el.classList.contains('eblock__code')) return el;
    if (el.tagName === 'INPUT' && el.classList.contains('eblock__heading')) return el;
    return null;
  }
  function wrapSelection(el, pre, post) {
    var s = el.selectionStart, e = el.selectionEnd, val = el.value;
    var sel = val.slice(s, e);
    el.value = val.slice(0, s) + pre + sel + post + val.slice(e);
    var ns = s + pre.length;
    el.setSelectionRange(ns, ns + sel.length);
    el.dispatchEvent(new Event('input', { bubbles: true }));
    el.focus();
  }
  function applyLink(el) {
    var s = el.selectionStart, e = el.selectionEnd, val = el.value;
    var sel = val.slice(s, e) || 'link text';
    var insert = '[' + sel + '](url)';
    el.value = val.slice(0, s) + insert + val.slice(e);
    var urlStart = s + ('[' + sel + '](').length;
    el.setSelectionRange(urlStart, urlStart + 3);
    el.dispatchEvent(new Event('input', { bubbles: true }));
    el.focus();
  }
  function buildFmtBar() {
    var bar = document.createElement('div');
    bar.className = 'fmt-bar';
    var defs = [
      { label: 'B', title: 'Bold', cls: 'fmt-bar__btn--b', fn: function (el) { wrapSelection(el, '**', '**'); } },
      { label: 'i', title: 'Italic', cls: 'fmt-bar__btn--i', fn: function (el) { wrapSelection(el, '*', '*'); } },
      { label: '</>', title: 'Inline code', cls: '', fn: function (el) { wrapSelection(el, '`', '`'); } },
      { label: 'S', title: 'Strikethrough', cls: 'fmt-bar__btn--s', fn: function (el) { wrapSelection(el, '~~', '~~'); } },
      { label: '🔗', title: 'Link', cls: '', fn: function (el) { applyLink(el); } }
    ];
    defs.forEach(function (d) {
      var b = document.createElement('button');
      b.type = 'button';
      b.className = 'fmt-bar__btn ' + d.cls;
      b.title = d.title;
      b.textContent = d.label;
      b.addEventListener('mousedown', function (e) { e.preventDefault(); });
      b.addEventListener('click', function (e) {
        e.preventDefault();
        var el = fmtTarget() || lastFmtEl;
        if (el) d.fn(el);
        positionFmtBar(el);
      });
      bar.appendChild(b);
    });
    return bar;
  }
  var lastFmtEl = null;
  function positionFmtBar(el) {
    if (!fmtBar || !el) return;
    var r = el.getBoundingClientRect();
    fmtBar.style.top = (window.scrollY + r.top - fmtBar.offsetHeight - 8) + 'px';
    fmtBar.style.left = (window.scrollX + r.left + 8) + 'px';
  }
  function maybeShowFmtBar() {
    var el = fmtTarget();
    var hasSel = el && el.selectionStart !== el.selectionEnd;
    if (!hasSel) { hideFmtBar(); return; }
    lastFmtEl = el;
    if (!fmtBar) { fmtBar = buildFmtBar(); document.body.appendChild(fmtBar); }
    fmtBar.style.display = 'flex';
    positionFmtBar(el);
  }
  function hideFmtBar() { if (fmtBar) fmtBar.style.display = 'none'; }
  document.addEventListener('mouseup', function () { setTimeout(maybeShowFmtBar, 0); });
  document.addEventListener('keyup', function (e) {
    if (e.shiftKey || e.key === 'ArrowLeft' || e.key === 'ArrowRight' || e.key === 'ArrowUp' || e.key === 'ArrowDown') {
      setTimeout(maybeShowFmtBar, 0);
    }
  });
  document.addEventListener('scroll', hideFmtBar, true);

  // ── Image paste / drop upload ──────────────────────────────────────────────
  function focusedBlockIdx() {
    var el = document.activeElement;
    if (!el || !canvas.contains(el)) return blocks.length;
    var wrap = el.closest ? el.closest('[data-block-idx]') : null;
    if (!wrap) return blocks.length;
    var i = parseInt(wrap.getAttribute('data-block-idx'), 10);
    return isNaN(i) ? blocks.length : i + 1;
  }
  function uploadImageFile(file, insertAt) {
    var fd = new FormData();
    fd.append('file', file);
    setStatus('Uploading image…');
    fetch('/os/api/media/upload', {
      method: 'POST',
      headers: { 'X-CSRF-Token': csrfToken() },
      body: fd
    }).then(function (r) { return r.ok ? r.json() : Promise.reject(r.status); })
      .then(function (d) {
        if (d && d.url) {
          insertBlock(insertAt, { type: 'image', url: d.url, alt: '' });
          setStatus('Image inserted', 'ok');
        } else { setStatus('Upload failed', 'danger'); }
      }).catch(function () { setStatus('Image upload failed', 'danger'); });
  }
  canvas.addEventListener('paste', function (e) {
    var items = (e.clipboardData && e.clipboardData.items) || [];
    for (var i = 0; i < items.length; i++) {
      if (items[i].type && items[i].type.indexOf('image/') === 0) {
        var f = items[i].getAsFile();
        if (f) { e.preventDefault(); uploadImageFile(f, focusedBlockIdx()); }
      }
    }
  });
  canvas.addEventListener('dragover', function (e) {
    if (e.dataTransfer && Array.prototype.indexOf.call(e.dataTransfer.types || [], 'Files') >= 0) {
      e.preventDefault();
      canvas.classList.add('is-dragover');
    }
  });
  canvas.addEventListener('dragleave', function () { canvas.classList.remove('is-dragover'); });
  canvas.addEventListener('drop', function (e) {
    canvas.classList.remove('is-dragover');
    var files = (e.dataTransfer && e.dataTransfer.files) || [];
    var imgs = [];
    for (var i = 0; i < files.length; i++) {
      if (files[i].type && files[i].type.indexOf('image/') === 0) imgs.push(files[i]);
    }
    if (imgs.length) {
      e.preventDefault();
      var at = blocks.length;
      imgs.forEach(function (f) { uploadImageFile(f, at++); });
    }
  });

  // ── Wire up ────────────────────────────────────────────────────────────────
  hydrate();
  renderCanvas();
  commitNow();
  updateStats();

  if (saveBtn) saveBtn.addEventListener('click', save);
  if (previewBtn) previewBtn.addEventListener('click', preview);
  if (previewClose) previewClose.addEventListener('click', function () { previewModal.hidden = true; });
  if (historyBtn) historyBtn.addEventListener('click', openHistory);
  if (historyClose) historyClose.addEventListener('click', function () { historyModal.hidden = true; });
  if (titleEl) titleEl.addEventListener('input', function () { updateStats(); scheduleAutosave(); });
  if (focusBtn) focusBtn.addEventListener('click', toggleFocus);
  if (splitBtn) splitBtn.addEventListener('click', toggleSplit);
  if (htmlBtn) htmlBtn.addEventListener('click', toggleHTML);
  if (htmlArea) htmlArea.addEventListener('input', function () {
    setStatus('Editing HTML…');
    if (slug) scheduleAutosave();
  });

  // Post settings panel wiring.
  hydrateSettings();
  if (settingsBtn) settingsBtn.addEventListener('click', toggleSettings);
  if (settingsClose) settingsClose.addEventListener('click', closeSettings);
  if (settingsBackdrop) settingsBackdrop.addEventListener('click', closeSettings);
  document.addEventListener('keydown', function (e) {
    if (e.key === 'Escape' && settingsPanel && !settingsPanel.hidden) closeSettings();
  });
  if (pm['feature-upload'] && pm['feature-file']) {
    pm['feature-upload'].addEventListener('click', function () { pm['feature-file'].click(); });
    pm['feature-file'].addEventListener('change', function () {
      if (pm['feature-file'].files && pm['feature-file'].files[0]) uploadFeatureImage(pm['feature-file'].files[0]);
      pm['feature-file'].value = '';
    });
  }
  if (pm['feature-image']) pm['feature-image'].addEventListener('input', function () { updateFeaturePreview(); scheduleAutosave(); });
  if (pm['feature-remove']) pm['feature-remove'].addEventListener('click', function () { pmSet(pm['feature-image'], ''); updateFeaturePreview(); scheduleAutosave(); });
  if (pm['slug-apply']) pm['slug-apply'].addEventListener('click', applySlug);
  if (pm['tags-input']) {
    pm['tags-input'].addEventListener('keydown', function (e) {
      if (e.key === 'Enter' || e.key === ',') { e.preventDefault(); addTagsFromInput(); }
      else if (e.key === 'Backspace' && pm['tags-input'].value === '' && pmTags.length) { pmTags.pop(); renderTags(); scheduleAutosave(); }
    });
    pm['tags-input'].addEventListener('blur', addTagsFromInput);
  }
  // Counters + autosave for any text/checkbox edit inside the panel.
  if (settingsPanel) {
    settingsPanel.addEventListener('input', function (e) {
      if (e.target === pm['slug'] || e.target === pm['tags-input']) return; // handled separately
      updateCounters();
      scheduleAutosave();
    });
    settingsPanel.addEventListener('change', function (e) {
      if (e.target === pm['featured'] || e.target === pm['is-page']) scheduleAutosave();
    });
  }
  if (undoBtn) undoBtn.addEventListener('click', undo);
  if (redoBtn) redoBtn.addEventListener('click', redo);

  // Global keyboard shortcuts.
  document.addEventListener('keydown', function (e) {
    var meta = e.metaKey || e.ctrlKey;
    if (!meta) return;
    var k = e.key.toLowerCase();
    if (k === 's') { e.preventDefault(); save(); return; }
    if (k === 'h' && e.shiftKey) { e.preventDefault(); toggleHTML(); return; }
    if (k === 'p' && e.shiftKey) { e.preventDefault(); toggleSettings(); return; }
    if (k === 'k') { e.preventDefault(); if (htmlMode) return; openPalette(focusedBlockIdx(), focusedBlockIdx() - 1); return; }
    if (e.key === '.') { e.preventDefault(); toggleFocus(); return; }
    if (k === 'z') {
      var ae = document.activeElement;
      var typing = ae && (ae.tagName === 'TEXTAREA' || ae.tagName === 'INPUT');
      if (!typing) { e.preventDefault(); if (e.shiftKey) redo(); else undo(); }
      return;
    }
    if (k === 'y') {
      var ae2 = document.activeElement;
      var typing2 = ae2 && (ae2.tagName === 'TEXTAREA' || ae2.tagName === 'INPUT');
      if (!typing2) { e.preventDefault(); redo(); }
    }
  });

  // Probe AI availability (fire-and-forget, affects palette only).
  fetch('/os/api/editor/ai', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': csrfToken() },
    body: JSON.stringify({ op: 'ping', text: '' })
  }).then(function (r) {
    if (r.status !== 503) aiEnabled = true;
  }).catch(function () {});
})();
