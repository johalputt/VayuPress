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

  /* ---- slash-command snippets ------------------------------------------- */
  var SNIPPETS = {
    heading: { label: "Heading", key: "H", text: "## Heading\n" },
    image: { label: "Image", key: "Img", text: "![alt text](https://)\n" },
    code: { label: "Code block", key: "</>", text: "```\ncode here\n```\n" },
    quote: { label: "Quote", key: "“", text: "> quote\n" },
    table: { label: "Table", key: "Tbl", text: "| Col A | Col B |\n| --- | --- |\n| a | b |\n" },
    callout: { label: "Callout", key: "!", text: "> **Note:** callout text\n" }
  };
  window.vpSnippet = function (name) { return SNIPPETS[name] ? SNIPPETS[name].text : ""; };

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

  /* ---- editor ----------------------------------------------------------- */
  function wireEditor() {
    var ta = $("[data-editor]");
    if (!ta) return;
    var preview = $("[data-preview]");
    var slug = ta.getAttribute("data-slug") || "";
    var titleInput = $("[data-field='title']");
    var slugInput = $("[data-field='slug']");

    function renderPreview() {
      if (preview) preview.innerHTML = markdownToHTML(ta.value);
      updateStats();
      updateSEO();
    }

    function updateStats() {
      var words = ta.value.trim() ? ta.value.trim().split(/\s+/).length : 0;
      var mins = Math.max(1, Math.round(words / 200));
      var wc = $("[data-wordcount]"); if (wc) wc.textContent = words + " words";
      var rt = $("[data-readtime]"); if (rt) rt.textContent = "~" + mins + " min read";
    }

    function updateSEO() {
      var t = titleInput ? titleInput.value : "";
      var sl = slugInput ? slugInput.value : slug;
      var st = $("[data-seo-title]"); if (st) st.textContent = t || "Untitled";
      var su = $("[data-seo-url]"); if (su) su.textContent = "/" + (sl || "your-slug");
      var sd = $("[data-seo-desc]");
      if (sd) { var txt = stripMd(ta.value).slice(0, 160); sd.textContent = txt || "No description yet."; }
    }

    /* live preview */
    ta.addEventListener("input", renderPreview);
    if (titleInput) titleInput.addEventListener("input", updateSEO);
    if (slugInput) slugInput.addEventListener("input", updateSEO);
    renderPreview();

    /* toolbar wrap buttons */
    $all("[data-wrap]").forEach(function (b) {
      b.addEventListener("click", function () {
        var spec = b.getAttribute("data-wrap").split("|");
        wrapSelection(ta, spec[0] || "", spec[1] || "");
      });
    });
    $all("[data-prefix]").forEach(function (b) {
      b.addEventListener("click", function () { insertAtCursor(ta, b.getAttribute("data-prefix")); });
    });

    /* distraction-free toggle */
    var df = $("[data-action='toggle-distraction']");
    if (df) df.addEventListener("click", function () { document.body.classList.toggle("distraction-free"); });

    /* slash palette */
    var palette = $("[data-slash-palette]");
    if (palette) {
      $all(".slash-item", palette).forEach(function (item) {
        item.addEventListener("mousedown", function (ev) {
          ev.preventDefault();
          insertAtCursor(ta, window.vpSnippet(item.getAttribute("data-snippet")));
          palette.classList.remove("open");
        });
      });
      ta.addEventListener("keyup", function (e) {
        if (e.key === "/") {
          palette.classList.add("open");
        } else if (e.key === "Escape") {
          palette.classList.remove("open");
        }
      });
      ta.addEventListener("blur", function () {
        setTimeout(function () { palette.classList.remove("open"); }, 150);
      });
    }

    /* autosave (debounced PUT to existing /api/v1/articles/{slug}) */
    var saveStatus = $("[data-save-status]");
    var saveTimer;
    function setStatus(text, cls) {
      if (!saveStatus) return;
      saveStatus.textContent = text;
      saveStatus.className = "save-status" + (cls ? " " + cls : "");
    }
    function doSave() {
      if (!slug) { return; } // new posts: operator creates via POST first
      setStatus("Saving…", "saving");
      /* AUTH HANDSHAKE: admin pages are protected by the API key (cookie/header
         depending on the operator's reverse proxy). The update endpoint also
         enforces CSRF. We send the double-submit CSRF token from the vp_csrf
         cookie as X-CSRF-Token, and forward any API key the operator wired into
         the hidden #vp-api-key field. If the deployment keys the API via cookie,
         RequireAPIKey reads it transparently and the header may be empty. */
      var headers = { "Content-Type": "application/json" };
      var csrf = cookie("vp_csrf");
      if (csrf) headers["X-CSRF-Token"] = csrf;
      var apiKeyField = document.getElementById("vp-api-key");
      if (apiKeyField && apiKeyField.value) headers["X-API-Key"] = apiKeyField.value;
      var body = {
        title: titleInput ? titleInput.value : undefined,
        content: ta.value
      };
      fetch("/api/v1/articles/" + encodeURIComponent(slug), {
        method: "PUT",
        headers: headers,
        credentials: "same-origin",
        body: JSON.stringify(body)
      }).then(function (r) {
        if (r.ok) { setStatus("Saved", "saved"); toast("Saved", "ok"); }
        else { setStatus("Save failed", ""); toast("Save failed (" + r.status + ")", "err"); }
      }).catch(function () { setStatus("Save failed", ""); toast("Network error", "err"); });
    }
    ta.addEventListener("input", function () {
      clearTimeout(saveTimer);
      setStatus("Unsaved…", "saving");
      saveTimer = setTimeout(doSave, 1500);
    });
    var saveBtn = $("[data-action='save-now']");
    if (saveBtn) saveBtn.addEventListener("click", function () { clearTimeout(saveTimer); doSave(); });

    /* version history fetch */
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
            if (!list.length) { menu.innerHTML = "<div class='version-item muted'>No versions</div>"; return; }
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

  /* ---- settings: update checker ----------------------------------------- */
  function wireUpdateCheck() {
    var btn = $("[data-action='check-updates']");
    if (!btn) return;
    var status = $("[data-update-status]");
    btn.addEventListener("click", function () {
      if (status) status.textContent = "Checking…";
      fetch("/admin/api/updates/check", { credentials: "same-origin" })
        .then(function (r) { return r.ok ? r.json() : Promise.reject(r.status); })
        .then(function (data) {
          if (!status) return;
          if (data && data.available) status.textContent = "Update available: " + (data.latest || "");
          else status.textContent = "Up to date (" + (data.current || "current") + ")";
        })
        .catch(function (st) { if (status) status.textContent = "Check failed (" + st + ")"; });
    });
  }

  /* ---- boot ------------------------------------------------------------- */
  function boot() {
    wireSidebar();
    wireModals();
    wireDropdowns();
    wireEditor();
    wireUpdateCheck();
  }
  if (document.readyState === "loading") document.addEventListener("DOMContentLoaded", boot);
  else boot();
})();
