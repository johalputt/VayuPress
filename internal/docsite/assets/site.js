// vayupress.com. Everything here is an addition: the pages read, link and list
// in full without it. It adds the search box, the register's filters, copying
// the install command, "synced 4 minutes ago", and "On this page" following
// the reader.
(function () {
  "use strict";

  // Search. The index is the site's own (search.json), fetched the first time
  // the box opens; nothing leaves the site.
  var dialog = document.querySelector("[data-search]");
  var input = dialog && dialog.querySelector("[data-search-input]");
  var list = dialog && dialog.querySelector("[data-search-results]");
  var empty = dialog && dialog.querySelector("[data-search-empty]");
  var index = null;
  var picked = 0;

  function load() {
    if (index) return Promise.resolve(index);
    return fetch("/search.json").then(function (r) { return r.json(); }).then(function (d) { index = d; return d; });
  }
  function score(e, words) {
    var t = e.t.toLowerCase(), s = (e.s || "").toLowerCase(), n = 0;
    for (var i = 0; i < words.length; i++) {
      var w = words[i];
      if (t.indexOf(w) === 0) n += 8;
      else if (t.indexOf(" " + w) >= 0) n += 5;
      else if (t.indexOf(w) >= 0) n += 3;
      else if (s.indexOf(w) >= 0) n += 1;
      else return 0;
    }
    return n + (e.k === "Guide" ? 0.5 : 0);
  }
  function mark(text, words) {
    var out = document.createDocumentFragment(), lower = text.toLowerCase(), at = 0;
    while (at < text.length) {
      var next = -1, len = 0;
      for (var i = 0; i < words.length; i++) {
        var j = lower.indexOf(words[i], at);
        if (j >= 0 && (next < 0 || j < next)) { next = j; len = words[i].length; }
      }
      if (next < 0) { out.appendChild(document.createTextNode(text.slice(at))); break; }
      out.appendChild(document.createTextNode(text.slice(at, next)));
      var m = document.createElement("mark");
      m.textContent = text.slice(next, next + len);
      out.appendChild(m);
      at = next + len;
    }
    return out;
  }
  function show() {
    var q = input.value.trim().toLowerCase();
    var words = q.split(/\s+/).filter(Boolean);
    list.textContent = "";
    picked = 0;
    if (!words.length || !index) { empty.hidden = true; return; }
    var hits = [];
    for (var i = 0; i < index.length; i++) {
      var s = score(index[i], words);
      if (s > 0) hits.push([s, i]);
    }
    hits.sort(function (a, b) { return b[0] - a[0] || a[1] - b[1]; });
    hits = hits.slice(0, 12);
    empty.hidden = hits.length > 0;
    hits.forEach(function (h, n) {
      var e = index[h[1]];
      var li = document.createElement("li");
      var a = document.createElement("a");
      a.href = e.u;
      a.setAttribute("role", "option");
      a.setAttribute("aria-selected", n === 0 ? "true" : "false");
      var t = document.createElement("span"); t.className = "t"; t.appendChild(mark(e.t, words));
      var k = document.createElement("span"); k.className = "k"; k.textContent = e.k;
      var s = document.createElement("span"); s.className = "s"; s.textContent = e.s || "";
      a.appendChild(t); a.appendChild(k); a.appendChild(s);
      li.appendChild(a);
      list.appendChild(li);
    });
  }
  function move(by) {
    var items = list.querySelectorAll("a");
    if (!items.length) return;
    items[picked].setAttribute("aria-selected", "false");
    picked = (picked + by + items.length) % items.length;
    items[picked].setAttribute("aria-selected", "true");
    items[picked].scrollIntoView({ block: "nearest" });
  }
  function open() {
    if (!dialog || dialog.open) return;
    var menu = document.querySelector(".menu[open]");
    if (menu) menu.open = false;
    dialog.showModal();
    input.select();
    load().then(show, function () { empty.textContent = "Search could not load. Every page is still in the menu."; empty.hidden = false; });
  }
  if (dialog) {
    document.querySelectorAll("[data-search-open]").forEach(function (b) { b.addEventListener("click", open); });
    input.addEventListener("input", show);
    input.addEventListener("keydown", function (e) {
      if (e.key === "ArrowDown") { e.preventDefault(); move(1); }
      else if (e.key === "ArrowUp") { e.preventDefault(); move(-1); }
      else if (e.key === "Enter") {
        var a = list.querySelectorAll("a")[picked];
        if (a) { e.preventDefault(); location.href = a.href; }
      }
    });
    dialog.addEventListener("click", function (e) { if (e.target === dialog) dialog.close(); });
    document.addEventListener("keydown", function (e) {
      var typing = /^(INPUT|TEXTAREA|SELECT)$/.test(document.activeElement.tagName) || document.activeElement.isContentEditable;
      if ((e.key === "/" && !typing) || (e.key === "k" && (e.metaKey || e.ctrlKey))) { e.preventDefault(); open(); }
    });
  }
  // The mirror's page has no index of its own: its search sends the reader
  // here with #search, and the box opens on arrival.
  if (dialog && location.hash === "#search") {
    history.replaceState(null, "", location.pathname);
    open();
  }

  // The register's filters: an owner, and words in the title.
  document.querySelectorAll("[data-adrs]").forEach(function (rows) {
    var root = rows.parentNode;
    var chips = root.querySelectorAll("[data-owner]:not(li)");
    var field = root.querySelector("[data-filter]");
    var none = root.querySelector("[data-none]");
    var owner = "";
    function apply() {
      var words = (field ? field.value : "").trim().toLowerCase().split(/\s+/).filter(Boolean), shown = 0;
      rows.querySelectorAll("li").forEach(function (li) {
        var text = li.textContent.toLowerCase();
        var ok = (!owner || li.getAttribute("data-owner") === owner) && words.every(function (w) { return text.indexOf(w) >= 0; });
        li.hidden = !ok;
        if (ok) shown++;
      });
      if (none) none.hidden = shown > 0;
    }
    chips.forEach(function (c) {
      c.addEventListener("click", function () {
        owner = c.getAttribute("data-owner");
        chips.forEach(function (o) { o.setAttribute("aria-pressed", o === c ? "true" : "false"); });
        apply();
      });
    });
    if (field) field.addEventListener("input", apply);
  });

  // The install command copies on a click.
  document.querySelectorAll("[data-copy]").forEach(function (code) {
    code.setAttribute("title", "Copy");
    code.addEventListener("click", function () {
      if (!navigator.clipboard) return;
      navigator.clipboard.writeText(code.textContent).then(function () {
        code.classList.add("copied");
        setTimeout(function () { code.classList.remove("copied"); }, 1600);
      });
    });
  });

  // "Synced from main 4 minutes ago": the page carries the moment, the reader's
  // clock says how long ago it was.
  function ago(t) {
    var s = (Date.now() - t) / 1000;
    if (s < 0 || isNaN(s)) return null;
    if (s < 60) return "just now";
    if (s < 3600) { var m = Math.floor(s / 60); return m + (m === 1 ? " minute ago" : " minutes ago"); }
    if (s < 86400) { var h = Math.floor(s / 3600); return h + (h === 1 ? " hour ago" : " hours ago"); }
    return null;
  }
  document.querySelectorAll("time[data-ago]").forEach(function (el) {
    var text = ago(Date.parse(el.getAttribute("datetime")));
    if (text) { el.title = el.textContent; el.textContent = text; }
  });

  // "On this page" marks the section being read.
  var toc = document.querySelectorAll(".toc ol a");
  if (toc.length && "IntersectionObserver" in window) {
    var byId = {};
    toc.forEach(function (a) { byId[decodeURIComponent(a.hash.slice(1))] = a; });
    var current = null;
    var seen = new IntersectionObserver(function (entries) {
      entries.forEach(function (e) {
        if (!e.isIntersecting) return;
        var a = byId[e.target.id];
        if (!a || a === current) return;
        if (current) current.removeAttribute("aria-current");
        a.setAttribute("aria-current", "true");
        current = a;
      });
    }, { rootMargin: "-80px 0px -70% 0px" });
    Object.keys(byId).forEach(function (id) { var h = document.getElementById(id); if (h) seen.observe(h); });
  }
})();
