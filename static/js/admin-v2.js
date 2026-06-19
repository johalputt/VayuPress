/* ==========================================================================
   VayuPress Admin v2 — application JS.

   PRODUCTION NOTE: In production the operator vendors the real Alpine.js CSP
   build locally as `static/js/alpine-csp.min.js` (package `@alpinejs/csp`) and
   loads it before this file with a same-origin <script> tag. Components would
   then be registered with `Alpine.data(name, () => ({...}))` — objects, never
   string expressions — so nothing requires eval/new Function and the strict CSP
   (`script-src 'self' 'nonce-...'`, no 'unsafe-eval') stays satisfied.

   Because Alpine cannot be fetched offline, THIS file implements the small bit
   of interactivity directly with plain DOM APIs. It is intentionally CSP-safe:
   NO eval, NO new Function, NO string-to-code, NO inline event-handler strings.
   Everything is wired via addEventListener + data-* attributes.
   ========================================================================== */
(function () {
  "use strict";

  /* ---- tiny helpers ----------------------------------------------------- */
  function $(sel, root) { return (root || document).querySelector(sel); }
  function $all(sel, root) { return Array.prototype.slice.call((root || document).querySelectorAll(sel)); }

  /* ---- toast ------------------------------------------------------------ */
  var toastEl, toastTimer;
  function toast(msg, kind) {
    if (!toastEl) {
      toastEl = document.createElement("div");
      toastEl.className = "toast";
      document.body.appendChild(toastEl);
    }
    toastEl.textContent = msg;
    toastEl.className = "toast show" + (kind ? " toast-" + kind : "");
    clearTimeout(toastTimer);
    toastTimer = setTimeout(function () { toastEl.className = "toast"; }, 2600);
  }
  window.vpToast = toast;

  /* ---- cookie reader (for CSRF) ----------------------------------------- */
  function cookie(name) {
    var parts = document.cookie.split(";");
    for (var i = 0; i < parts.length; i++) {
      var kv = parts[i].trim().split("=");
      if (kv[0] === name) return decodeURIComponent(kv.slice(1).join("="));
    }
    return "";
  }

  /* ---- markdown -> HTML (self-contained, no deps) ----------------------- */
  function escapeHTML(s) {
    return s.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;");
  }
  function inline(s) {
    // code spans first (protect their contents)
    s = s.replace(/`([^`]+)`/g, function (_, c) { return "<code>" + escapeHTML(c) + "</code>"; });
    s = s.replace(/!\[([^\]]*)\]\(([^)\s]+)\)/g, '<img alt="$1" src="$2">');
    s = s.replace(/\[([^\]]+)\]\(([^)\s]+)\)/g, '<a href="$2">$1</a>');
    s = s.replace(/\*\*([^*]+)\*\*/g, "<strong>$1</strong>");
    s = s.replace(/(^|[^*])\*([^*]+)\*/g, "$1<em>$2</em>");
    s = s.replace(/_([^_]+)_/g, "<em>$1</em>");
    return s;
  }
  function markdownToHTML(src) {
    var lines = src.replace(/\r\n/g, "\n").split("\n");
    var out = [], i = 0;
    function flushList(tag, items) {
      out.push("<" + tag + ">" + items.map(function (it) { return "<li>" + inline(escapeHTML(it)) + "</li>"; }).join("") + "</" + tag + ">");
    }
    while (i < lines.length) {
      var line = lines[i];
      // fenced code block
      if (/^```/.test(line)) {
        var buf = [];
        i++;
        while (i < lines.length && !/^```/.test(lines[i])) { buf.push(lines[i]); i++; }
        i++;
        out.push("<pre><code>" + escapeHTML(buf.join("\n")) + "</code></pre>");
        continue;
      }
      // heading
      var h = /^(#{1,6})\s+(.*)$/.exec(line);
      if (h) { var lv = h[1].length; out.push("<h" + lv + ">" + inline(escapeHTML(h[2])) + "</h" + lv + ">"); i++; continue; }
      // blockquote
      if (/^>\s?/.test(line)) {
        var qb = [];
        while (i < lines.length && /^>\s?/.test(lines[i])) { qb.push(lines[i].replace(/^>\s?/, "")); i++; }
        out.push("<blockquote>" + inline(escapeHTML(qb.join(" "))) + "</blockquote>");
        continue;
      }
      // unordered list
      if (/^\s*[-*+]\s+/.test(line)) {
        var ul = [];
        while (i < lines.length && /^\s*[-*+]\s+/.test(lines[i])) { ul.push(lines[i].replace(/^\s*[-*+]\s+/, "")); i++; }
        flushList("ul", ul); continue;
      }
      // ordered list
      if (/^\s*\d+\.\s+/.test(line)) {
        var ol = [];
        while (i < lines.length && /^\s*\d+\.\s+/.test(lines[i])) { ol.push(lines[i].replace(/^\s*\d+\.\s+/, "")); i++; }
        flushList("ol", ol); continue;
      }
      // blank
      if (/^\s*$/.test(line)) { i++; continue; }
      // paragraph (gather consecutive non-blank, non-special lines)
      var para = [line]; i++;
      while (i < lines.length && !/^\s*$/.test(lines[i]) && !/^(#{1,6}\s|>|\s*[-*+]\s|\s*\d+\.\s|```)/.test(lines[i])) {
        para.push(lines[i]); i++;
      }
      out.push("<p>" + inline(escapeHTML(para.join(" "))) + "</p>");
    }
    return out.join("\n");
  }
  window.vpMarkdown = markdownToHTML;

  /* ---- HTML sanitiser (CSP-safe, no innerHTML sink) --------------------- */
  /* Sanitises an HTML string and returns a DocumentFragment of safe nodes.
     Callers use `el.replaceChildren(fragment)` instead of assigning innerHTML,
     so tainted text is never reinterpreted as live HTML.

     The sanitiser is DOMPurify (Cure53), vendored same-origin at
     /admin/v2/static/js/purify.min.js and loaded before this file — no CDN, no
     unsafe-eval, so the strict CSP and the project's offline-sovereignty
     constraint both hold (ADR-0065). DOMPurify strips every execution vector
     (scripts, event handlers, javascript:/data: URLs, mXSS, etc.). This is the
     same trust boundary the server enforces with bluemonday on publish.

     Fail-closed: if DOMPurify is somehow unavailable, the content is rendered
     as inert text (textContent) rather than risking unsanitised HTML — and no
     HTML sink is touched on that path either. */
  function sanitizeToFragment(html) {
    if (window.DOMPurify && typeof window.DOMPurify.sanitize === "function") {
      return window.DOMPurify.sanitize(String(html), {
        RETURN_DOM_FRAGMENT: true,
        USE_PROFILES: { html: true }
      });
    }
    var frag = document.createDocumentFragment();
    var pre = document.createElement("pre");
    pre.textContent = String(html);
    frag.appendChild(pre);
    return frag;
  }
  window.vpSanitize = sanitizeToFragment;

  function stripMd(src) {
    return src.replace(/[#>*_`\-]/g, " ").replace(/\s+/g, " ").trim();
  }

  /* ---- sidebar toggle --------------------------------------------------- */
  function wireSidebar() {
    var btn = $("[data-action='toggle-sidebar']");
    if (btn) btn.addEventListener("click", function () { document.body.classList.toggle("sidebar-open"); });
  }

  /* ---- modal ------------------------------------------------------------ */
  function wireModals() {
    $all("[data-modal-open]").forEach(function (t) {
      t.addEventListener("click", function () {
        var m = document.getElementById(t.getAttribute("data-modal-open"));
        if (m) m.classList.add("open");
      });
    });
    $all("[data-modal-close]").forEach(function (t) {
      t.addEventListener("click", function () {
        var m = t.closest(".modal-backdrop");
        if (m) m.classList.remove("open");
      });
    });
    $all(".modal-backdrop").forEach(function (bk) {
      bk.addEventListener("click", function (e) { if (e.target === bk) bk.classList.remove("open"); });
    });
  }

  /* ---- generic dropdown toggle (version history) ------------------------ */
  function wireDropdowns() {
    $all("[data-dropdown-toggle]").forEach(function (t) {
      t.addEventListener("click", function (e) {
        e.stopPropagation();
        var menu = document.getElementById(t.getAttribute("data-dropdown-toggle"));
        if (menu) menu.classList.toggle("open");
      });
    });
    document.addEventListener("click", function () {
      $all(".dropdown-menu.open").forEach(function (m) { m.classList.remove("open"); });
    });
  }

  /* ---- slash-command catalog -------------------------------------------- */
  /* Ordered list so the palette is keyboard-navigable and filterable. Each
     command carries the keywords it matches and the Markdown it inserts. */
  var COMMANDS = [
    { id: "h1", label: "Heading 1", hint: "H1", words: "heading title h1", text: "# " },
    { id: "h2", label: "Heading 2", hint: "H2", words: "heading subtitle h2", text: "## " },
    { id: "h3", label: "Heading 3", hint: "H3", words: "heading h3", text: "### " },
    { id: "image", label: "Image", hint: "Img", words: "image picture photo upload", text: "![alt text](https://)\n" },
    { id: "code", label: "Code block", hint: "</>", words: "code snippet pre fenced", text: "```\ncode here\n```\n" },
    { id: "quote", label: "Quote", hint: "“", words: "quote blockquote citation", text: "> " },
    { id: "callout", label: "Callout", hint: "!", words: "callout note tip warning", text: "> **Note:** callout text\n" },
    { id: "ul", label: "Bullet list", hint: "•", words: "list bullet unordered ul", text: "- " },
    { id: "ol", label: "Numbered list", hint: "1.", words: "list numbered ordered ol", text: "1. " },
    { id: "table", label: "Table", hint: "Tbl", words: "table grid columns", text: "| Col A | Col B |\n| --- | --- |\n| a | b |\n" },
    { id: "hr", label: "Divider", hint: "—", words: "divider rule separator hr", text: "\n---\n" }
  ];
  window.vpSnippet = function (id) {
    for (var i = 0; i < COMMANDS.length; i++) if (COMMANDS[i].id === id) return COMMANDS[i].text;
    return "";
  };

  function insertAtCursor(ta, text) {
    var start = ta.selectionStart, end = ta.selectionEnd;
    ta.value = ta.value.slice(0, start) + text + ta.value.slice(end);
    var pos = start + text.length;
    ta.selectionStart = ta.selectionEnd = pos;
    ta.dispatchEvent(new Event("input", { bubbles: true }));
    ta.focus();
  }

  function wrapSelection(ta, before, after) {
    var s = ta.selectionStart, e = ta.selectionEnd;
    var sel = ta.value.slice(s, e) || "text";
    var rep = before + sel + after;
    ta.value = ta.value.slice(0, s) + rep + ta.value.slice(e);
    ta.selectionStart = s + before.length;
    ta.selectionEnd = s + before.length + sel.length;
    ta.dispatchEvent(new Event("input", { bubbles: true }));
    ta.focus();
  }

  /* ---- slugify (mirrors the server's rule closely enough for previews) --- */
  function slugify(s) {
    return s.toLowerCase().replace(/[^a-z0-9]+/g, "-").replace(/^-+|-+$/g, "").slice(0, 80);
  }

  /* ---- editor ----------------------------------------------------------- */
  function wireEditor() {
    var ta = $("[data-editor]");
    if (!ta) return;
    var preview = $("[data-preview]");
    var slug = ta.getAttribute("data-slug") || "";
    var titleInput = $("[data-field='title']");
    var slugInput = $("[data-field='slug']");
    var slugTouched = !!(slugInput && slugInput.value);
    var dirty = false;

    /* ---- authoring format (markdown | html) ----------------------------- */
    var fmtState = $("[data-format-state]");
    var format = (fmtState && fmtState.value) || "markdown";

    // The HTML actually published: raw in HTML mode, converted from Markdown
    // otherwise. The public renderer sanitizes it (bluemonday) regardless.
    function computeHTML() { return format === "html" ? ta.value : markdownToHTML(ta.value); }

    function renderPreview() {
      if (preview) preview.replaceChildren(sanitizeToFragment(computeHTML()));
      updateStats();
      updateSEO();
    }

    function setFormat(f) {
      if (f !== "markdown" && f !== "html") return;
      format = f;
      if (fmtState) fmtState.value = f;
      $all("[data-format-btn]").forEach(function (b) {
        b.classList.toggle("active", b.getAttribute("data-format-btn") === f);
      });
      ta.setAttribute("placeholder", f === "html"
        ? "Write raw HTML… it is sanitized on publish"
        : "Write in Markdown… type / for commands, drag an image to upload");
      renderPreview(); markDirty();
    }
    $all("[data-format-btn]").forEach(function (b) {
      b.addEventListener("click", function () { setFormat(b.getAttribute("data-format-btn")); });
    });

    function wordCount() { return ta.value.trim() ? ta.value.trim().split(/\s+/).length : 0; }

    function updateStats() {
      var words = wordCount();
      var mins = Math.max(1, Math.round(words / 200));
      var wc = $("[data-wordcount]"); if (wc) wc.textContent = words + (words === 1 ? " word" : " words");
      var cc = $("[data-charcount]"); if (cc) cc.textContent = ta.value.length + " chars";
      var rt = $("[data-readtime]"); if (rt) rt.textContent = "~" + mins + " min read";
    }

    function updateSEO() {
      var t = titleInput ? titleInput.value : "";
      var sl = slugInput ? slugInput.value : slug;
      var st = $("[data-seo-title]"); if (st) st.textContent = t || "Untitled";
      var su = $("[data-seo-url]"); if (su) su.textContent = "/" + (sl || "your-slug");
      var desc = stripMd(ta.value).slice(0, 160);
      var sd = $("[data-seo-desc]"); if (sd) sd.textContent = desc || "No description yet.";

      /* lightweight readiness score (0–100) — title, length, slug, structure */
      var words = wordCount(), score = 0;
      if (t.length >= 10 && t.length <= 65) score += 30; else if (t) score += 12;
      if (words >= 300) score += 35; else if (words >= 50) score += 20; else if (words > 0) score += 6;
      if (sl) score += 15;
      if (/^#{1,3}\s/m.test(ta.value)) score += 10;        // has a heading
      if (/!\[[^\]]*\]\([^)]+\)/.test(ta.value)) score += 10; // has an image
      score = Math.min(100, score);
      var meter = $("[data-seo-meter]");
      if (meter) {
        meter.style.width = score + "%";
        meter.className = "seo-meter-bar " + (score >= 75 ? "good" : score >= 45 ? "ok" : "low");
      }
      var hint = $("[data-seo-hint]");
      if (hint) {
        if (!t) hint.textContent = "Add a title to start scoring.";
        else if (words < 50) hint.textContent = "Write 50+ words for a healthier score.";
        else if (score >= 75) hint.textContent = "Looking great — well-structured and substantial.";
        else hint.textContent = "Good. Add a heading or image to lift the score.";
      }
    }

    function markDirty() { dirty = true; }

    /* live preview + auto-slug from title */
    ta.addEventListener("input", function () { renderPreview(); markDirty(); });
    if (titleInput) titleInput.addEventListener("input", function () {
      if (slugInput && !slugTouched) slugInput.value = slugify(titleInput.value);
      updateSEO(); markDirty();
    });
    if (slugInput) slugInput.addEventListener("input", function () { slugTouched = true; updateSEO(); markDirty(); });
    renderPreview();

    /* toolbar wrap / prefix buttons */
    $all("[data-wrap]").forEach(function (b) {
      b.addEventListener("click", function () {
        var spec = b.getAttribute("data-wrap").split("|");
        wrapSelection(ta, spec[0] || "", spec[1] || "");
      });
    });
    $all("[data-prefix]").forEach(function (b) {
      b.addEventListener("click", function () { insertLinePrefix(ta, b.getAttribute("data-prefix")); });
    });

    /* distraction-free + preview toggles */
    $all("[data-action='toggle-distraction']").forEach(function (df) {
      df.addEventListener("click", function () { document.body.classList.toggle("distraction-free"); });
    });
    var pvBtn = $("[data-action='toggle-preview']");
    if (pvBtn) pvBtn.addEventListener("click", function () { document.body.classList.toggle("preview-hidden"); });

    /* ---- slash palette (filterable, keyboard-navigable) ----------------- */
    var palette = $("[data-slash-palette]");
    var paletteOpen = false, activeIdx = 0, slashPos = -1;
    function renderPalette(filter) {
      if (!palette) return;
      var f = (filter || "").toLowerCase();
      var matches = COMMANDS.filter(function (c) {
        return !f || c.label.toLowerCase().indexOf(f) >= 0 || c.words.indexOf(f) >= 0;
      });
      palette.innerHTML = "";
      if (!matches.length) { closePalette(); return; }
      if (activeIdx >= matches.length) activeIdx = matches.length - 1;
      matches.forEach(function (c, idx) {
        var el = document.createElement("div");
        el.className = "slash-item" + (idx === activeIdx ? " active" : "");
        el.setAttribute("role", "option");
        el.innerHTML = "";
        var lab = document.createElement("span"); lab.textContent = c.label;
        var key = document.createElement("span"); key.className = "slash-key"; key.textContent = c.hint;
        el.appendChild(lab); el.appendChild(key);
        el.addEventListener("mousedown", function (ev) { ev.preventDefault(); choose(c); });
        palette.appendChild(el);
      });
      palette._matches = matches;
      palette.classList.add("open");
      paletteOpen = true;
    }
    function closePalette() { if (palette) { palette.classList.remove("open"); paletteOpen = false; slashPos = -1; activeIdx = 0; } }
    function choose(c) {
      /* replace the "/filter" the user typed with the snippet */
      if (slashPos >= 0) {
        var caret = ta.selectionStart;
        ta.value = ta.value.slice(0, slashPos) + ta.value.slice(caret);
        ta.selectionStart = ta.selectionEnd = slashPos;
      }
      insertAtCursor(ta, c.text);
      closePalette(); markDirty();
    }
    if (palette) {
      ta.addEventListener("input", function () {
        var caret = ta.selectionStart;
        var upto = ta.value.slice(0, caret);
        var m = /(?:^|\s)\/([a-z0-9]*)$/i.exec(upto);
        if (m) { slashPos = caret - m[1].length - 1; activeIdx = 0; renderPalette(m[1]); }
        else closePalette();
      });
      ta.addEventListener("keydown", function (e) {
        if (!paletteOpen) return;
        var matches = palette._matches || [];
        if (e.key === "ArrowDown") { e.preventDefault(); activeIdx = (activeIdx + 1) % matches.length; renderPalette(currentFilter()); }
        else if (e.key === "ArrowUp") { e.preventDefault(); activeIdx = (activeIdx - 1 + matches.length) % matches.length; renderPalette(currentFilter()); }
        else if (e.key === "Enter") { e.preventDefault(); if (matches[activeIdx]) choose(matches[activeIdx]); }
        else if (e.key === "Escape") { e.preventDefault(); closePalette(); }
      });
      function currentFilter() {
        var upto = ta.value.slice(0, ta.selectionStart);
        var m = /(?:^|\s)\/([a-z0-9]*)$/i.exec(upto);
        return m ? m[1] : "";
      }
      ta.addEventListener("blur", function () { setTimeout(closePalette, 150); });
    }

    /* ---- Tab to indent (don't lose focus) ------------------------------- */
    ta.addEventListener("keydown", function (e) {
      if (e.key === "Tab" && !paletteOpen) { e.preventDefault(); insertAtCursor(ta, "  "); }
    });

    /* ---- autosave -------------------------------------------------------- */
    var saveTimer;
    function setStatus(text, cls) {
      $all("[data-save-status]").forEach(function (s) {
        s.textContent = text;
        s.className = (s.classList.contains("badge") ? "badge save-status" : "save-status") + (cls ? " " + cls : "");
      });
    }

    function buildHeaders() {
      var h = { "Content-Type": "application/json" };
      var csrf = cookie("vp_csrf"); if (csrf) h["X-CSRF-Token"] = csrf;
      var k = document.getElementById("vp-api-key"); if (k && k.value) h["X-API-Key"] = k.value;
      return h;
    }

    /* Create a new post, then redirect the editor to the permanent URL. */
    function doCreate() {
      var titleVal = titleInput ? titleInput.value.trim() : "";
      var slugVal = slugInput ? slugInput.value.trim() : "";
      if (!titleVal) { toast("Add a title first", "err"); return; }
      setStatus("Creating…", "saving");
      fetch("/api/v1/articles", {
        method: "POST", headers: buildHeaders(), credentials: "same-origin",
        body: JSON.stringify({ title: titleVal, slug: slugVal || slugify(titleVal), content: computeHTML(), tags: [] })
      })
        .then(function (r) { return r.ok ? r.json() : r.json().then(function (j) { return Promise.reject(j.error || r.status); }); })
        .then(function (data) {
          dirty = false;
          window.location.href = "/admin/v2/editor/" + encodeURIComponent(data.slug || (slugVal || slugify(titleVal)));
        })
        .catch(function (err) { setStatus("Create failed", "err"); toast("Create failed: " + err, "err"); });
    }

    /* Save both the editable source side-car AND the rendered HTML. */
    function doSave() {
      if (!slug) { doCreate(); return; }
      setStatus("Saving…", "saving");
      var h = buildHeaders();
      var srcPromise = fetch("/api/v1/admin/articles/" + encodeURIComponent(slug) + "/source", {
        method: "PUT", headers: h, credentials: "same-origin",
        body: JSON.stringify({ format: format, source: ta.value })
      });
      var artBody = { title: titleInput ? titleInput.value : undefined, content: computeHTML() };
      var artPromise = fetch("/api/v1/articles/" + encodeURIComponent(slug), {
        method: "PUT", headers: h, credentials: "same-origin", body: JSON.stringify(artBody)
      });
      Promise.all([srcPromise, artPromise]).then(function (rs) {
        if (rs[0].ok && rs[1].ok) { setStatus("Saved", "saved"); dirty = false; }
        else { setStatus("Save failed", "err"); toast("Save failed", "err"); }
      }).catch(function () { setStatus("Offline", "err"); toast("Network error — changes kept locally", "err"); });
    }
    ta.addEventListener("input", function () {
      clearTimeout(saveTimer);
      setStatus("Unsaved…", "saving");
      saveTimer = setTimeout(doSave, 1500);
    });
    var saveBtn = $("[data-action='save-now']");
    if (saveBtn) saveBtn.addEventListener("click", function () { clearTimeout(saveTimer); doSave(); });

    /* warn before leaving with unsaved edits */
    window.addEventListener("beforeunload", function (e) {
      if (dirty) { e.preventDefault(); e.returnValue = ""; return ""; }
    });

    /* ---- keyboard shortcuts --------------------------------------------- */
    ta.addEventListener("keydown", function (e) {
      var mod = e.ctrlKey || e.metaKey;
      if (!mod) return;
      var k = e.key.toLowerCase();
      if (k === "b") { e.preventDefault(); wrapSelection(ta, "**", "**"); }
      else if (k === "i") { e.preventDefault(); wrapSelection(ta, "*", "*"); }
      else if (k === "k") { e.preventDefault(); wrapSelection(ta, "[", "](https://)"); }
      else if (k === "s") { e.preventDefault(); clearTimeout(saveTimer); doSave(); }
    });
    document.addEventListener("keydown", function (e) {
      var mod = e.ctrlKey || e.metaKey;
      if (mod && e.key === ".") { e.preventDefault(); document.body.classList.toggle("distraction-free"); }
      if (mod && e.key.toLowerCase() === "p") { e.preventDefault(); document.body.classList.toggle("preview-hidden"); }
    });

    /* ---- image upload: button + drag&drop + paste ----------------------- */
    var imageInput = $("[data-image-input]");
    var dropOverlay = $("[data-drop-overlay]");
    function uploadImage(fileBlob) {
      if (!fileBlob) return;
      if (fileBlob.size > 8 * 1024 * 1024) { toast("Image exceeds 8 MB", "err"); return; }
      setStatus("Uploading image…", "saving");
      var fd = new FormData();
      fd.append("file", fileBlob, fileBlob.name || "image.png");
      var headers = {};
      var csrf = cookie("vp_csrf");
      if (csrf) headers["X-CSRF-Token"] = csrf;
      var apiKeyField = document.getElementById("vp-api-key");
      if (apiKeyField && apiKeyField.value) headers["X-API-Key"] = apiKeyField.value;
      fetch("/api/v1/admin/media", { method: "POST", headers: headers, credentials: "same-origin", body: fd })
        .then(function (r) { return r.ok ? r.json() : r.json().then(function (j) { return Promise.reject(j.error || r.status); }); })
        .then(function (data) {
          insertAtCursor(ta, "![](" + data.url + ")\n");
          setStatus("Saved", "saved"); toast("Image uploaded", "ok"); markDirty();
        })
        .catch(function (err) { setStatus("Idle", ""); toast("Upload failed: " + err, "err"); });
    }
    var imgBtn = $("[data-action='insert-image']");
    if (imgBtn && imageInput) imgBtn.addEventListener("click", function () { imageInput.click(); });
    if (imageInput) imageInput.addEventListener("change", function () {
      if (imageInput.files && imageInput.files[0]) uploadImage(imageInput.files[0]);
      imageInput.value = "";
    });
    ta.addEventListener("paste", function (e) {
      var items = (e.clipboardData && e.clipboardData.items) || [];
      for (var i = 0; i < items.length; i++) {
        if (items[i].type && items[i].type.indexOf("image/") === 0) {
          e.preventDefault(); uploadImage(items[i].getAsFile()); return;
        }
      }
    });
    var dragDepth = 0;
    ta.addEventListener("dragenter", function (e) { e.preventDefault(); dragDepth++; if (dropOverlay) dropOverlay.classList.add("show"); });
    ta.addEventListener("dragover", function (e) { e.preventDefault(); });
    ta.addEventListener("dragleave", function () { dragDepth--; if (dragDepth <= 0 && dropOverlay) dropOverlay.classList.remove("show"); });
    ta.addEventListener("drop", function (e) {
      e.preventDefault(); dragDepth = 0; if (dropOverlay) dropOverlay.classList.remove("show");
      var f = e.dataTransfer && e.dataTransfer.files && e.dataTransfer.files[0];
      if (f && f.type.indexOf("image/") === 0) uploadImage(f);
    });

    /* ---- version history fetch ------------------------------------------ */
    var verBtn = $("[data-load-versions]");
    if (verBtn) {
      verBtn.addEventListener("click", function () {
        var menu = document.getElementById(verBtn.getAttribute("data-load-versions"));
        if (!menu || !slug) return;
        menu.innerHTML = "<div class='version-item muted'>Loading…</div>";
        fetch("/api/v1/admin/articles/" + encodeURIComponent(slug) + "/versions", { credentials: "same-origin" })
          .then(function (r) { return r.ok ? r.json() : Promise.reject(r.status); })
          .then(function (data) {
            var list = (data && data.versions) || data || [];
            if (!list.length) { menu.innerHTML = "<div class='version-item muted'>No versions yet</div>"; return; }
            menu.innerHTML = "";
            list.forEach(function (v) {
              var d = document.createElement("div");
              d.className = "version-item";
              var when = v.created_at || v.updated_at || v.when || "";
              d.textContent = (v.title || v.id || "revision");
              var s = document.createElement("div"); s.className = "v-when"; s.textContent = String(when);
              d.appendChild(s);
              menu.appendChild(d);
            });
          })
          .catch(function (st) { menu.innerHTML = "<div class='version-item muted'>Failed (" + st + ")</div>"; });
      });
    }
  }

  /* insert a line-level prefix (heading/quote/list) at the cursor's line start */
  function insertLinePrefix(ta, prefix) {
    var s = ta.selectionStart;
    var lineStart = ta.value.lastIndexOf("\n", s - 1) + 1;
    ta.value = ta.value.slice(0, lineStart) + prefix + ta.value.slice(lineStart);
    ta.selectionStart = ta.selectionEnd = s + prefix.length;
    ta.dispatchEvent(new Event("input", { bubbles: true }));
    ta.focus();
  }

  /* ---- settings: rich update checker ------------------------------------ */
  function wireUpdateCheck() {
    var btn = $("[data-action='check-updates']");
    if (!btn) return;
    var result = $("[data-update-result]");
    var banner = $("[data-update-banner]");
    var changelog = $("[data-update-changelog]");
    var guide = $("[data-apply-guide]");

    btn.addEventListener("click", function () {
      btn.disabled = true;
      var orig = btn.textContent;
      btn.textContent = "Checking…";
      fetch("/admin/api/updates/check", { credentials: "same-origin" })
        .then(function (r) { return r.ok ? r.json() : Promise.reject(r.status); })
        .then(function (data) {
          if (result) result.hidden = false;
          var avail = data.updateAvailable;
          if (banner) {
            banner.className = "update-banner " + (avail ? "is-available" : "is-current");
            banner.textContent = avail
              ? "Update available: v" + (data.latest || "?") + " (you have v" + (data.current || "?") + ")"
              : "You're on the latest version (v" + (data.current || "?") + ").";
          }
          if (changelog) {
            changelog.innerHTML = "";
            if (avail && data.changelog) {
              var h = document.createElement("div"); h.className = "card-title"; h.textContent = "What's new";
              var pre = document.createElement("div"); pre.className = "changelog-body";
              pre.replaceChildren(sanitizeToFragment(markdownToHTML(String(data.changelog))));
              changelog.appendChild(h); changelog.appendChild(pre);
            }
          }
          if (guide) guide.hidden = !avail;
        })
        .catch(function (st) {
          if (result) result.hidden = false;
          if (banner) { banner.className = "update-banner is-error"; banner.textContent = "Check failed (" + st + ")."; }
        })
        .finally(function () { btn.disabled = false; btn.textContent = orig; });
    });
  }

  /* ---- settings: update history ----------------------------------------- */
  function wireUpdateHistory() {
    var host = $("[data-update-history]");
    if (!host) return;
    fetch("/admin/api/updates/history", { credentials: "same-origin" })
      .then(function (r) { return r.ok ? r.json() : Promise.reject(r.status); })
      .then(function (data) {
        var list = (data && data.history) || [];
        if (!list.length) { host.innerHTML = "<p class='muted'>No update activity recorded yet.</p>"; return; }
        var rows = list.map(function (h) {
          var when = h.started_at || h.StartedAt || "";
          var from = h.from_version || h.FromVersion || "—";
          var to = h.to_version || h.ToVersion || "—";
          var status = h.status || h.Status || "";
          return "<tr><td class='muted'>" + escapeHTML(String(when)) + "</td><td>" +
            escapeHTML(String(from)) + " → " + escapeHTML(String(to)) +
            "</td><td><span class='badge'>" + escapeHTML(String(status)) + "</span></td></tr>";
        }).join("");
        host.innerHTML = "<table class='table'><thead><tr><th>When</th><th>Version</th><th>Status</th></tr></thead><tbody>" + rows + "</tbody></table>";
      })
      .catch(function () { host.innerHTML = "<p class='muted'>Update history unavailable.</p>"; });
  }

  /* ---- copy-to-clipboard (CLI commands) --------------------------------- */
  function wireCopy() {
    $all("[data-copy]").forEach(function (el) {
      el.addEventListener("click", function () {
        var text = el.getAttribute("data-copy");
        if (navigator.clipboard && navigator.clipboard.writeText) {
          navigator.clipboard.writeText(text).then(function () { toast("Copied", "ok"); });
        } else {
          var ta = document.createElement("textarea"); ta.value = text; document.body.appendChild(ta);
          ta.select(); try { document.execCommand("copy"); toast("Copied", "ok"); } catch (e) { /* ignore */ }
          document.body.removeChild(ta);
        }
      });
    });
  }

  /* ---- posts list: instant client-side search --------------------------- */
  function wirePostsSearch() {
    var input = $("[data-posts-search]");
    if (!input) return;
    var emptyMsg = $("[data-search-empty]");
    input.addEventListener("input", function () {
      var q = input.value.trim().toLowerCase();
      var shown = 0;
      $all("[data-post-row]").forEach(function (row) {
        var hit = !q || (row.getAttribute("data-search") || "").indexOf(q) >= 0;
        row.hidden = !hit;
        if (hit) shown++;
      });
      if (emptyMsg) emptyMsg.hidden = shown !== 0;
    });
  }

  /* ---- SEO: regenerate artefacts ---------------------------------------- */
  function wireSEO() {
    var btn = $("[data-action='seo-regenerate']");
    if (!btn) return;
    var status = $("[data-seo-status]");
    btn.addEventListener("click", function () {
      btn.disabled = true;
      var orig = btn.textContent; btn.textContent = "Regenerating…";
      var headers = { "Content-Type": "application/json" };
      var csrf = cookie("vp_csrf"); if (csrf) headers["X-CSRF-Token"] = csrf;
      fetch("/admin/v2/api/seo/regenerate", { method: "POST", headers: headers, credentials: "same-origin" })
        .then(function (r) { return r.ok ? r.json() : Promise.reject(r.status); })
        .then(function (data) {
          if (status) { status.hidden = false; status.className = "seo-status ok"; status.textContent = "Regenerated: " + ((data.regenerated || []).join(", ")); }
          toast("SEO artefacts regenerated", "ok");
        })
        .catch(function (st) {
          if (status) { status.hidden = false; status.className = "seo-status err"; status.textContent = "Regenerate failed (" + st + ")"; }
          toast("Regenerate failed", "err");
        })
        .finally(function () { btn.disabled = false; btn.textContent = orig; });
    });
  }

  /* ---- boot ------------------------------------------------------------- */
  function boot() {
    wireSidebar();
    wireModals();
    wireDropdowns();
    wireEditor();
    wireUpdateCheck();
    wireUpdateHistory();
    wireCopy();
    wirePostsSearch();
    wireSEO();
  }
  if (document.readyState === "loading") document.addEventListener("DOMContentLoaded", boot);
  else boot();
})();
