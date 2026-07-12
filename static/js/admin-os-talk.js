/*
 * admin-os-talk.js — the VayuTalk web client.
 *
 * Ephemeral, end-to-end-encrypted chat that shares ONE relay with VayuMail
 * Mobile: a message sent here reaches the app and vice-versa. The browser holds
 * no private key — the VayuPress server signs/encrypts on send and decrypts on
 * receive for the signed-in mailbox (the same trust model as reading encrypted
 * mail in webmail). We speak plaintext to same-origin endpoints only:
 *
 *   GET  /os/talk/stream  → Server-Sent Events: decrypted `message`
 *                                    envelopes, `receipt` (read/expired), `ping`.
 *   POST /os/talk/send    → { to, text, ttl_seconds, mode } (CSRF).
 *
 * Privacy: conversations live only in this tab's memory. A reload wipes them —
 * there is no localStorage, no history, nothing on disk. Messages self-remove
 * at their expiry. Strict-CSP compliant: no inline styles, no innerHTML with
 * dynamic data — every node is built with createElement / textContent.
 */
(function () {
  'use strict';

  var root = document.querySelector('.vtalk');
  if (!root) return;
  var self = root.getAttribute('data-self') || '';

  var els = {
    status: document.getElementById('vtalk-status'),
    newchat: document.getElementById('vtalk-newchat'),
    peer: document.getElementById('vtalk-peer'),
    convos: document.getElementById('vtalk-convos'),
    main: document.getElementById('vtalk-main'),
    head: document.getElementById('vtalk-thread-head'),
    thread: document.getElementById('vtalk-thread'),
    composer: document.getElementById('vtalk-composer'),
    input: document.getElementById('vtalk-input'),
    ttl: document.getElementById('vtalk-ttl'),
    live: document.getElementById('vtalk-live'),
    send: document.getElementById('vtalk-send')
  };

  // peer(lowercased) -> { peer, messages:[], unread, item, dot }
  var convos = Object.create(null);
  // message id -> { peer, node, statusEl } so receipts can find their bubble.
  var byId = Object.create(null);
  var active = '';

  function cookie(name) {
    var m = document.cookie.match(new RegExp('(?:^|; )' + name + '=([^;]*)'));
    return m ? decodeURIComponent(m[1]) : '';
  }

  function norm(addr) { return (addr || '').trim().toLowerCase(); }

  function initials(addr) {
    var s = (addr || '?').replace(/@.*/, '');
    return (s[0] || '?').toUpperCase();
  }

  function fmtTime(iso) {
    var d = iso ? new Date(iso) : new Date();
    if (isNaN(d.getTime())) return '';
    var h = d.getHours(), m = d.getMinutes();
    return (h < 10 ? '0' : '') + h + ':' + (m < 10 ? '0' : '') + m;
  }

  function elem(tag, cls, text) {
    var n = document.createElement(tag);
    if (cls) n.className = cls;
    if (text != null) n.textContent = text;
    return n;
  }

  // ── Conversations ──────────────────────────────────────────────────────────

  function getConvo(peer) {
    peer = norm(peer);
    if (!peer) return null;
    if (convos[peer]) return convos[peer];

    var item = elem('li', 'vtalk-convo');
    var av = elem('span', 'vtalk-avatar', initials(peer));
    var meta = elem('span', 'vtalk-convo-meta');
    meta.appendChild(elem('span', 'vtalk-convo-name', peer));
    var dot = elem('span', 'vtalk-convo-badge');
    dot.hidden = true;
    item.appendChild(av);
    item.appendChild(meta);
    item.appendChild(dot);
    item.addEventListener('click', function () { activate(peer); });
    els.convos.appendChild(item);

    var c = { peer: peer, messages: [], unread: 0, item: item, dot: dot };
    convos[peer] = c;
    return c;
  }

  function activate(peer) {
    peer = norm(peer);
    var c = getConvo(peer);
    if (!c) return;
    active = peer;
    c.unread = 0;
    c.dot.hidden = true;
    c.dot.textContent = '';
    Object.keys(convos).forEach(function (k) {
      convos[k].item.classList.toggle('vtalk-convo--active', k === peer);
    });
    els.main.removeAttribute('data-empty');

    els.head.textContent = '';
    var av = elem('span', 'vtalk-avatar', initials(peer));
    var hmeta = elem('div', 'vtalk-head-meta');
    hmeta.appendChild(elem('strong', null, peer));
    hmeta.appendChild(elem('span', 'text-sm muted', 'End-to-end encrypted · disappears when read'));
    els.head.appendChild(av);
    els.head.appendChild(hmeta);

    renderThread(c);
    els.input.disabled = false;
    els.send.disabled = false;
    els.input.focus();
  }

  function renderThread(c) {
    els.thread.textContent = '';
    if (!c.messages.length) {
      var hint = elem('div', 'vtalk-hint');
      hint.appendChild(elem('p', null, 'No messages yet. Say hello — it will vanish once they read it.'));
      els.thread.appendChild(hint);
      return;
    }
    c.messages.forEach(function (m) { els.thread.appendChild(m.node); });
    scrollDown();
  }

  function scrollDown() { els.thread.scrollTop = els.thread.scrollHeight; }

  // ── Message bubbles ────────────────────────────────────────────────────────

  function bubble(m) {
    var row = elem('div', 'vtalk-msg vtalk-msg--' + (m.mine ? 'out' : 'in'));
    var body = elem('div', 'vtalk-bubble');
    body.appendChild(elem('div', 'vtalk-bubble-text', m.text));
    var foot = elem('div', 'vtalk-bubble-foot');
    foot.appendChild(elem('span', 'vtalk-bubble-time', fmtTime(m.createdAt)));
    if (m.mine) {
      m.statusEl = elem('span', 'vtalk-bubble-status', 'Sent');
      foot.appendChild(m.statusEl);
    }
    body.appendChild(foot);
    row.appendChild(body);
    m.node = row;
    return row;
  }

  function addMessage(peer, m) {
    var c = getConvo(peer);
    if (!c) return;
    bubble(m);
    c.messages.push(m);
    if (m.id) byId[m.id] = m;
    if (active === c.peer) {
      var hint = els.thread.querySelector('.vtalk-hint');
      if (hint) els.thread.textContent = '';
      els.thread.appendChild(m.node);
      scrollDown();
    } else if (!m.mine) {
      c.unread++;
      c.dot.hidden = false;
      c.dot.textContent = String(c.unread);
      // Float the freshest conversation to the top.
      els.convos.insertBefore(c.item, els.convos.firstChild);
    }
    scheduleExpiry(m);
  }

  // Ephemeral: a message removes itself from view at its expiry.
  function scheduleExpiry(m) {
    if (!m.expiresAt) return;
    var ms = new Date(m.expiresAt).getTime() - Date.now();
    if (isNaN(ms)) return;
    if (ms < 0) ms = 0;
    m.timer = setTimeout(function () { expireMessage(m); }, Math.min(ms, 2147483647));
  }

  function expireMessage(m) {
    if (m.node && m.node.parentNode) {
      m.node.classList.add('vtalk-msg--gone');
      setTimeout(function () { if (m.node && m.node.parentNode) m.node.parentNode.removeChild(m.node); }, 400);
    }
    var c = convos[m.peer];
    if (c) {
      var i = c.messages.indexOf(m);
      if (i >= 0) c.messages.splice(i, 1);
    }
    if (m.id) delete byId[m.id];
  }

  function setStatus(m, label, cls) {
    if (!m || !m.statusEl) return;
    m.statusEl.textContent = label;
    m.statusEl.className = 'vtalk-bubble-status' + (cls ? ' ' + cls : '');
  }

  // ── Stream ─────────────────────────────────────────────────────────────────

  var es = null;
  function connect() {
    if (es) es.close();
    es = new EventSource('/os/talk/stream');

    es.addEventListener('open', function () { markStatus('online', 'Online'); });
    es.addEventListener('error', function () { markStatus('offline', 'Reconnecting…'); });

    es.addEventListener('message', function (e) {
      var d = parse(e.data);
      if (!d || !d.from) return;
      addMessage(d.from, {
        peer: norm(d.from), mine: false, text: d.text || '',
        id: d.id, createdAt: d.created_at, expiresAt: d.expires_at, mode: d.mode
      });
    });

    es.addEventListener('receipt', function (e) {
      var d = parse(e.data);
      if (!d || !d.id) return;
      var m = byId[d.id];
      if (!m) return;
      if (d.status === 'read') setStatus(m, 'Read', 'is-read');
      else if (d.status === 'expired') { setStatus(m, 'Expired', 'is-expired'); }
    });

    es.addEventListener('ping', function () { markStatus('online', 'Online'); });
  }

  function markStatus(state, label) {
    if (!els.status) return;
    els.status.setAttribute('data-state', state);
    els.status.textContent = label;
  }

  function parse(s) { try { return JSON.parse(s); } catch (_) { return null; } }

  // ── Sending ────────────────────────────────────────────────────────────────

  function send() {
    var text = els.input.value.trim();
    if (!text || !active) return;
    var mode = els.live && els.live.checked ? 'live' : 'store';
    var ttl = parseInt(els.ttl && els.ttl.value, 10) || 3600;
    var to = active;

    var m = { peer: to, mine: true, text: text, createdAt: new Date().toISOString(), mode: mode };
    addMessage(to, m);
    els.input.value = '';
    autogrow();

    fetch('/os/talk/send', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': cookie('vp_csrf') },
      body: JSON.stringify({ to: to, text: text, ttl_seconds: ttl, mode: mode })
    }).then(function (r) {
      return r.json().then(function (j) { return { ok: r.ok, j: j }; });
    }).then(function (res) {
      if (!res.ok) {
        var msg = (res.j && res.j.error && res.j.error.message) || 'Could not send';
        setStatus(m, msg, 'is-error');
        return;
      }
      if (res.j.id) { m.id = res.j.id; byId[m.id] = m; }
      m.expiresAt = new Date(Date.now() + ttl * 1000).toISOString();
      scheduleExpiry(m);
      if (mode === 'live') setStatus(m, res.j.delivered ? 'Delivered' : 'Not online', res.j.delivered ? '' : 'is-error');
      else setStatus(m, res.j.delivered ? 'Delivered' : 'Queued');
    }).catch(function () { setStatus(m, 'Failed', 'is-error'); });
  }

  function autogrow() {
    els.input.style.height = 'auto';
    els.input.style.height = Math.min(els.input.scrollHeight, 160) + 'px';
  }

  // ── Wiring ─────────────────────────────────────────────────────────────────

  els.newchat.addEventListener('submit', function (e) {
    e.preventDefault();
    var peer = norm(els.peer.value);
    if (!peer || peer === norm(self)) { els.peer.value = ''; return; }
    els.peer.value = '';
    activate(peer);
  });

  els.composer.addEventListener('submit', function (e) { e.preventDefault(); send(); });

  els.input.addEventListener('keydown', function (e) {
    if (e.key === 'Enter' && !e.shiftKey) { e.preventDefault(); send(); }
  });
  els.input.addEventListener('input', autogrow);

  connect();
  window.addEventListener('beforeunload', function () { if (es) es.close(); });
})();
