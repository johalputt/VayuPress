// VayuPress Updates — renders the mirror's live state from /mirror/status.json.
//
// Everything is built with DOM nodes and textContent, never innerHTML: the
// release notes are text from GitHub, and a page about verification should not
// be the place a crafted release body turns into markup.
(function () {
  'use strict';

  var CHECKS = {
    'sigstore': 'Sigstore signature',
    'ed25519': 'Release key',
    'sha256-file': 'SHA-256 file',
    'github-digest': 'GitHub digest',
    'executable': 'Linux executable'
  };
  var ORDER = ['sigstore', 'ed25519', 'sha256-file', 'github-digest', 'executable'];

  function $(id) { return document.getElementById(id); }
  function el(tag, cls, text) {
    var n = document.createElement(tag);
    if (cls) n.className = cls;
    if (text !== undefined && text !== null) n.textContent = text;
    return n;
  }
  function clear(n) { while (n.firstChild) n.removeChild(n.firstChild); }

  function size(b) {
    if (b >= 1048576) return (b / 1048576).toFixed(1) + ' MB';
    if (b >= 1024) return (b / 1024).toFixed(1) + ' KB';
    return b + ' B';
  }
  function ago(iso) {
    var s = Math.max(0, (Date.now() - new Date(iso).getTime()) / 1000);
    if (s < 90) return 'just now';
    if (s < 5400) return Math.round(s / 60) + ' min ago';
    if (s < 172800) return Math.round(s / 3600) + ' h ago';
    return Math.round(s / 86400) + ' days ago';
  }
  function day(iso) {
    return new Date(iso).toLocaleDateString(undefined, { year: 'numeric', month: 'long', day: 'numeric' });
  }

  function chips(list, into) {
    ORDER.forEach(function (k) {
      if (list.indexOf(k) >= 0) into.appendChild(el('li', 'chip', CHECKS[k]));
    });
  }

  function pulse(state, text) {
    var p = $('pulse');
    p.setAttribute('data-state', state);
    $('pulse-text').textContent = text;
  }

  // Inline markdown: **bold**, `code` and [text](https://…). Anything else
  // stays literal text.
  function inline(text, into) {
    var re = /\*\*([^*]+)\*\*|`([^`]+)`|\[([^\]]+)\]\((https:\/\/[^)\s]+)\)/g;
    var last = 0, m;
    while ((m = re.exec(text))) {
      if (m.index > last) into.appendChild(document.createTextNode(text.slice(last, m.index)));
      if (m[1] !== undefined) into.appendChild(el('b', null, m[1]));
      else if (m[2] !== undefined) into.appendChild(el('code', null, m[2]));
      else {
        var a = el('a', null, m[3]);
        a.href = m[4];
        a.rel = 'noopener';
        into.appendChild(a);
      }
      last = re.lastIndex;
    }
    if (last < text.length) into.appendChild(document.createTextNode(text.slice(last)));
  }

  // The release notes' own shape: ## / ### headings, "- " bullets whose text
  // wraps onto indented lines, and paragraphs. GitHub appends a "What's
  // Changed" footer of pull-request links, which is left out.
  function renderNotes(md, into) {
    clear(into);
    md = String(md || '').replace(/\r/g, '').split(/\n## What's Changed/)[0];
    var lines = md.split('\n'), list = null, item = null, para = null;
    function flush() { list = null; item = null; para = null; }
    lines.forEach(function (raw) {
      var line = raw.replace(/\s+$/, '');
      var h = /^#{2,4}\s+(.*)$/.exec(line);
      if (h) { flush(); into.appendChild(el('h3', null, h[1])); return; }
      var b = /^[-*]\s+(.*)$/.exec(line);
      if (b) {
        if (!list) { list = el('ul'); into.appendChild(list); }
        item = el('li'); list.appendChild(item); para = null;
        item.setAttribute('data-md', b[1]);
        return;
      }
      if (!line.trim()) { if (item) { item = null; } else { para = null; list = null; } return; }
      if (item && /^\s+/.test(raw)) { item.setAttribute('data-md', item.getAttribute('data-md') + ' ' + line.trim()); return; }
      list = null; item = null;
      if (!para) { para = el('p'); into.appendChild(para); para.setAttribute('data-md', line.trim()); return; }
      para.setAttribute('data-md', para.getAttribute('data-md') + ' ' + line.trim());
    });
    Array.prototype.forEach.call(into.querySelectorAll('[data-md]'), function (n) {
      var t = n.getAttribute('data-md');
      n.removeAttribute('data-md');
      inline(t, n);
    });
    if (!into.firstChild) into.appendChild(el('p', 'muted', 'This release carries no notes.'));
  }

  function copyButton(text) {
    var btn = el('button', 'mini', 'Copy');
    btn.type = 'button';
    btn.addEventListener('click', function () { copy(text, btn); });
    return btn;
  }
  function copy(text, btn) {
    var done = function () { btn.textContent = 'Copied'; setTimeout(function () { btn.textContent = 'Copy'; }, 1400); };
    if (navigator.clipboard && window.isSecureContext) {
      navigator.clipboard.writeText(text).then(done, function () { btn.textContent = 'Select and copy'; });
    } else {
      btn.textContent = 'Select and copy';
    }
  }

  function render(st) {
    var releases = st.releases || [];
    var latest = null;
    releases.forEach(function (r) { if (!latest && !r.prerelease) latest = r; });

    if (!releases.length) {
      pulse('down', 'Nothing held yet');
    } else if (!st.healthy) {
      pulse('warn', 'Serving ' + releases[0].version + ' · last sync incomplete');
    } else if (st.synced_at && Date.now() - new Date(st.synced_at).getTime() > 3 * 3600 * 1000) {
      pulse('warn', 'Last synced ' + ago(st.synced_at));
    } else {
      pulse('ok', 'In sync · checked ' + ago(st.checked_at));
    }

    var held = $('held-list');
    clear(held);
    releases.forEach(function (r) {
      var li = el('li');
      var left = el('div');
      left.appendChild(el('span', 'held__v', r.version));
      left.appendChild(document.createTextNode(' '));
      left.appendChild(el('span', 'held__meta', 'published ' + day(r.published) + ' · verified ' + ago(r.verified_at) +
        ' · ' + r.files.length + ' files'));
      li.appendChild(left);
      li.appendChild(el('span', r.prerelease ? 'chip chip--pre' : 'chip', r.prerelease ? 'Pre-release' : 'Stable'));
      held.appendChild(li);
    });
    if (!releases.length) held.appendChild(el('li', 'muted', 'The mirror has not completed a sync yet.'));

    if (!latest) {
      $('latest-meta').textContent = 'No stable release is held right now.';
      clear($('files-body'));
      clear($('notes-body'));
      return;
    }

    $('latest-version').textContent = latest.version;
    $('latest-meta').textContent = 'Published ' + day(latest.published) + ' · verified by this mirror ' + ago(latest.verified_at);
    $('files-version').textContent = latest.version;

    var bin = null;
    latest.files.forEach(function (f) { if (f.name === 'vayupress') bin = f; });
    var dl = $('latest-download');
    if (bin) {
      dl.href = bin.url;
      dl.removeAttribute('aria-disabled');
      dl.textContent = 'Download binary · ' + size(bin.size);
      var checks = $('latest-checks');
      clear(checks);
      chips(bin.checks, checks);
    }

    var body = $('files-body');
    clear(body);
    latest.files.forEach(function (f) {
      var tr = el('tr');
      var name = el('td');
      var a = el('a', null, f.name);
      a.href = f.url;
      name.appendChild(a);
      tr.appendChild(name);
      tr.appendChild(el('td', null, size(f.size)));
      var h = el('td');
      var wrap = el('span', 'hash');
      var code = el('code', null, f.sha256.slice(0, 16) + '…');
      code.title = f.sha256;
      wrap.appendChild(code);
      wrap.appendChild(copyButton(f.sha256));
      h.appendChild(wrap);
      tr.appendChild(h);
      var c = el('td');
      var ul = el('ul', 'checks');
      chips(f.checks, ul);
      c.appendChild(ul);
      tr.appendChild(c);
      body.appendChild(tr);
    });

    renderNotes(latest.notes, $('notes-body'));
    if (latest.source_url) {
      var src = $('notes-source');
      clear(src);
      src.appendChild(document.createTextNode('As published with ' + latest.version + ' — '));
      var gh = el('a', null, 'view it on GitHub');
      gh.href = latest.source_url;
      gh.rel = 'noopener';
      src.appendChild(gh);
      src.appendChild(document.createTextNode('.'));
    }
  }

  function load() {
    fetch('/mirror/status.json', { cache: 'no-store' })
      .then(function (r) { if (!r.ok) throw new Error('status ' + r.status); return r.json(); })
      .then(render)
      .catch(function () {
        pulse('down', 'Mirror status unavailable');
        $('latest-meta').textContent = 'The mirror did not answer. Try again in a minute.';
      });
  }

  Array.prototype.forEach.call(document.querySelectorAll('[data-copy]'), function (btn) {
    btn.addEventListener('click', function () {
      copy($(btn.getAttribute('data-copy')).textContent, btn);
    });
  });

  load();
  setInterval(load, 5 * 60 * 1000);
})();
