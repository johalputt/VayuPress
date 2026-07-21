/*
 * admin-os-talk.js — the VayuTalk web client.
 *
 * Ephemeral, end-to-end-encrypted chat that shares ONE relay with VayuMail
 * Mobile: a message sent here reaches the app and vice-versa. The browser holds
 * no private key — the VayuPress server signs/encrypts on send and decrypts on
 * receive for the signed-in mailbox (the same trust model as reading encrypted
 * mail in webmail). We speak plaintext to same-origin endpoints only:
 *
 *   GET  /os/talk/stream        → SSE: decrypted `message`, `receipt`, `ping`.
 *   GET  /os/talk/peer?email=   → { found, fingerprint, safety } for a recipient.
 *   POST /os/talk/send          → { to, text, ttl_seconds, mode } (CSRF).
 *
 * Privacy: conversations live only in this tab's memory. A reload wipes them —
 * no message is ever written to disk, and each self-removes at its expiry. The
 * ONE thing persisted (localStorage) is a verification mark: the public
 * fingerprint you confirmed for a peer, so you verify once instead of on every
 * reload; it reveals nothing and is dropped if the peer's key ever changes.
 * Strict-CSP compliant: no inline styles, no innerHTML with dynamic data — every
 * node is built with createElement / textContent.
 */
(function () {
  'use strict';

  var root = document.querySelector('.vtalk');
  if (!root) return;
  var self = root.getAttribute('data-self') || '';
  var selfFp = root.getAttribute('data-self-fp') || '';
  var onionWorld = root.getAttribute('data-onion') === '1'; // Tor world: this console is the sole reader
  var asSel = document.getElementById('vtalk-as');   // "chat as" switcher (admins)
  var currentSelf = (asSel ? asSel.value : self).trim().toLowerCase();

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

  // Live mode burns a message a few seconds after it is read (readable, then
  // gone) and is never stored on the server — see the composer's 🔥 Live toggle.
  var LIVE_GRACE_SECONDS = 5;

  // peer(lowercased) -> { peer, messages:[], unread, item, dot }
  var convos = Object.create(null);
  var byId = Object.create(null);           // message id -> message
  var peerInfo = Object.create(null);       // peer -> { found, safety }
  var verified = Object.create(null);       // peer -> bool (this tab only)
  // Contacts: the ONLY conversation state that survives a reload, and it holds no
  // messages — just a chosen display name and a "keep" flag per peer. "Keep" pins
  // the contact so its row is restored on reload (chats are ephemeral by default);
  // the name lets you show a friendly label instead of the raw address once you've
  // added someone. Both are per-identity and stored locally only.
  var contacts = Object.create(null);       // peer -> { name, kept }
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
  function parse(s) { try { return JSON.parse(s); } catch (_) { return null; } }

  // ── Contacts (names + kept flag, the only reload-surviving state) ────────────
  // Stored per identity as vtalk-contacts:<self>. Never contains messages.

  function contactsKey() { return 'vtalk-contacts:' + currentSelf; }
  function loadContacts() {
    contacts = Object.create(null);
    var obj = parse(lsGet(contactsKey()));
    if (obj && typeof obj === 'object') {
      Object.keys(obj).forEach(function (k) {
        var v = obj[k];
        if (v && typeof v === 'object') contacts[norm(k)] = { name: (v.name || '').trim(), kept: !!v.kept };
      });
    }
  }
  function saveContacts() {
    var out = {};
    Object.keys(contacts).forEach(function (k) {
      var v = contacts[k];
      if (v && (v.name || v.kept)) out[k] = { name: v.name || '', kept: !!v.kept };
    });
    lsSet(contactsKey(), JSON.stringify(out));
  }
  function contactEntry(peer, create) {
    peer = norm(peer);
    if (!contacts[peer] && create) contacts[peer] = { name: '', kept: false };
    return contacts[peer] || null;
  }
  function isKept(peer) { var e = contacts[norm(peer)]; return !!(e && e.kept); }

  // A default friendly label derived from the address' local part, so once a chat
  // is added the raw mail id is not shown — a name is. "john.doe@x" -> "John Doe".
  function friendlyName(peer) {
    var local = norm(peer).replace(/@.*/, '');
    if (!local) return peer;
    var words = local.split(/[._+\-]+/).filter(Boolean).map(function (w) {
      return w.charAt(0).toUpperCase() + w.slice(1);
    });
    return words.join(' ') || peer;
  }
  // The label shown in the list and thread header: a name the user set, else the
  // friendly default. The full address is only ever revealed in the verify panel.
  function displayName(peer) {
    var e = contacts[norm(peer)];
    if (e && e.name) return e.name;
    return friendlyName(peer);
  }

  // ── Conversations ──────────────────────────────────────────────────────────

  function getConvo(peer) {
    peer = norm(peer);
    if (!peer) return null;
    if (convos[peer]) return convos[peer];

    var name = displayName(peer);
    var item = elem('li', 'vtalk-convo');
    var av = elem('span', 'vtalk-avatar', initials(name));
    var meta = elem('span', 'vtalk-convo-meta');
    var nameEl = elem('span', 'vtalk-convo-name', name);
    meta.appendChild(nameEl);
    var pin = elem('span', 'vtalk-convo-pin', '📌');
    pin.title = 'Kept — stays in your list after reload';
    pin.hidden = !isKept(peer);
    if (isKept(peer)) item.classList.add('vtalk-convo--kept');
    var dot = elem('span', 'vtalk-convo-badge');
    dot.hidden = true;
    item.appendChild(av);
    item.appendChild(meta);
    item.appendChild(pin);
    item.appendChild(dot);
    item.addEventListener('click', function () { activate(peer); });
    els.convos.appendChild(item);

    var c = { peer: peer, messages: [], unread: 0, item: item, dot: dot, nameEl: nameEl, av: av, pin: pin };
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
    buildHeader(peer);
    renderThread(c);
    els.input.disabled = false;
    els.send.disabled = false;
    els.input.focus();
    preflightPeer(peer);
  }

  // ── Thread header + verify panel ────────────────────────────────────────────

  function buildHeader(peer) {
    els.head.textContent = '';
    var name = displayName(peer);
    var av = elem('span', 'vtalk-avatar', initials(name));
    var hmeta = elem('div', 'vtalk-head-meta');
    hmeta.appendChild(elem('strong', null, name));
    hmeta.appendChild(elem('span', 'text-sm muted', 'End-to-end encrypted · disappears when read'));

    var actions = elem('div', 'vtalk-head-actions');

    // Rename: choose a name to show instead of the address (the address itself is
    // never shown outside the verify panel once a chat is added).
    var rbtn = elem('button', 'vtalk-head-btn');
    rbtn.type = 'button';
    rbtn.textContent = '✎ Rename';
    rbtn.title = 'Show a name instead of the address';
    rbtn.addEventListener('click', function () { renameContact(peer); });

    // Keep: pin the contact so its row survives a reload (chats are ephemeral by
    // default — nothing but this flag and the chosen name is ever persisted).
    var kbtn = elem('button', 'vtalk-head-btn');
    kbtn.type = 'button';
    kbtn.textContent = isKept(peer) ? '📌 Kept' : '📌 Keep';
    kbtn.classList.toggle('is-on', isKept(peer));
    kbtn.title = isKept(peer)
      ? 'This contact stays in your list after reload'
      : 'Keep this contact in your list after reload';
    kbtn.addEventListener('click', function () { toggleKeep(peer); });

    var vbtn = elem('button', 'vtalk-verify-btn');
    vbtn.type = 'button';
    setVerifyBtn(vbtn, peer);
    vbtn.addEventListener('click', function () { toggleVerify(peer); });

    actions.appendChild(rbtn);
    actions.appendChild(kbtn);
    actions.appendChild(vbtn);

    els.head.appendChild(av);
    els.head.appendChild(hmeta);
    els.head.appendChild(actions);

    // Collapsible verify panel (hidden until the shield is clicked).
    var panel = elem('div', 'vtalk-verify');
    panel.id = 'vtalk-verify';
    panel.hidden = true;
    els.head.appendChild(panel);
  }

  // Rename / Keep mutate the contact store, persist, then refresh the row + header.
  function renameContact(peer) {
    peer = norm(peer);
    var next = window.prompt('Name for this contact (leave blank to use the default)', displayName(peer));
    if (next == null) return; // cancelled
    var e = contactEntry(peer, true);
    e.name = next.trim();
    if (!e.name && !e.kept) delete contacts[peer];
    saveContacts();
    refreshContactUI(peer);
  }
  function toggleKeep(peer) {
    peer = norm(peer);
    var e = contactEntry(peer, true);
    e.kept = !e.kept;
    if (!e.name && !e.kept) delete contacts[peer];
    saveContacts();
    if (e.kept) getConvo(peer); // ensure the row exists even with no messages yet
    refreshContactUI(peer);
  }
  function refreshContactUI(peer) {
    peer = norm(peer);
    var c = convos[peer];
    if (c) {
      var name = displayName(peer);
      if (c.nameEl) c.nameEl.textContent = name;
      if (c.av) c.av.textContent = initials(name);
      if (c.pin) c.pin.hidden = !isKept(peer);
      c.item.classList.toggle('vtalk-convo--kept', isKept(peer));
    }
    if (active === peer) buildHeader(peer);
  }
  // Restore kept contacts (per current identity) as empty rows on load / identity
  // switch. Messages are never restored — only the id you chose to keep.
  function restoreKeptContacts() {
    Object.keys(contacts).forEach(function (peer) {
      if (contacts[peer] && contacts[peer].kept) getConvo(peer);
    });
  }

  function setVerifyBtn(btn, peer) {
    btn.textContent = verified[peer] ? '🛡 Verified' : '🛡 Verify';
    btn.classList.toggle('is-verified', !!verified[peer]);
  }

  function toggleVerify(peer) {
    var panel = document.getElementById('vtalk-verify');
    if (!panel) return;
    if (!panel.hidden) { panel.hidden = true; return; }
    renderVerify(panel, peer);
    panel.hidden = false;
  }

  function safetyRow(label, value, muted) {
    var row = elem('div', 'vtalk-safety-row');
    row.appendChild(elem('span', 'vtalk-safety-label', label));
    row.appendChild(elem('code', 'vtalk-safety-code' + (muted ? ' muted' : ''), value));
    return row;
  }

  function renderVerify(panel, peer) {
    panel.textContent = '';
    panel.appendChild(elem('p', 'vtalk-verify-intro',
      'Compare these safety numbers with ' + displayName(peer) + ' (' + peer + ') over a call or in person. If they match, your conversation is private end to end and no one is in the middle.'));
    panel.appendChild(safetyRow('You', selfFp || '—', !selfFp));
    var info = peerInfo[peer];
    if (info && info.found) {
      panel.appendChild(safetyRow(peer, info.safety || '—'));
      var lab = elem('label', 'vtalk-verify-toggle');
      var cb = document.createElement('input');
      cb.type = 'checkbox';
      cb.checked = !!verified[peer];
      cb.addEventListener('change', function () {
        setVerified(peer, cb.checked, true);
        var btn = els.head.querySelector('.vtalk-verify-btn');
        if (btn) setVerifyBtn(btn, peer);
      });
      lab.appendChild(cb);
      lab.appendChild(elem('span', null, 'I compared these and they match'));
      panel.appendChild(lab);
    } else {
      panel.appendChild(elem('div', 'vtalk-safety-missing',
        'No key found for ' + peer + ' yet — this address can’t receive VayuTalk messages until they open VayuMail (app or web) on this server at least once.'));
    }
  }

  // Verification is remembered across reloads, bound to the exact fingerprint
  // that was verified (fingerprints are public, so persisting them locally leaks
  // nothing). If the peer's key ever changes, the stored mark no longer matches
  // and you're asked to verify again — which is the correct behaviour. Messages
  // themselves are still never persisted.
  function verifyKey(peer) { return 'vtalk-verified:' + currentSelf + ':' + peer; }
  function lsGet(k) { try { return window.localStorage.getItem(k); } catch (_) { return null; } }
  function lsSet(k, v) { try { window.localStorage.setItem(k, v); } catch (_) {} }
  function lsDel(k) { try { window.localStorage.removeItem(k); } catch (_) {} }

  // Fetch the peer's key so we can (a) warn early if they're unreachable,
  // (b) show a safety number to verify, and (c) restore a prior verification
  // when the key is unchanged.
  function preflightPeer(peer) {
    fetch('/os/talk/peer?email=' + encodeURIComponent(peer), { headers: { 'Accept': 'application/json' } })
      .then(function (r) { return r.json(); })
      .then(function (j) {
        peerInfo[peer] = { found: !!j.found, safety: j.safety || '', fingerprint: j.fingerprint || '' };
        // Restore verification only if the fingerprint matches what was verified.
        if (j.found && j.fingerprint && lsGet(verifyKey(peer)) === j.fingerprint) {
          setVerified(peer, true, false);
        }
        if (active === peer) {
          reachabilityBanner(peer);
          var btn = els.head.querySelector('.vtalk-verify-btn');
          if (btn) setVerifyBtn(btn, peer);
          var panel = document.getElementById('vtalk-verify');
          if (panel && !panel.hidden) renderVerify(panel, peer);
        }
      })
      .catch(function () { /* offline; send will report the real error */ });
  }

  // setVerified updates state, the conversation badge, and (when persist) the
  // remembered fingerprint.
  function setVerified(peer, on, persist) {
    verified[peer] = on;
    var c = convos[peer];
    if (c) c.item.classList.toggle('vtalk-convo--verified', on);
    if (persist) {
      var info = peerInfo[peer];
      if (on && info && info.fingerprint) lsSet(verifyKey(peer), info.fingerprint);
      else lsDel(verifyKey(peer));
    }
  }

  function reachabilityBanner(peer) {
    var existing = els.thread.querySelector('.vtalk-warn');
    if (existing) existing.parentNode.removeChild(existing);
    var info = peerInfo[peer];
    if (info && info.found === false) {
      var warn = elem('div', 'vtalk-warn',
        '⚠ No VayuTalk key for ' + peer + ' yet. They need to open VayuMail (app or web) on this server once so a key exists — until then messages can’t be delivered.');
      els.thread.insertBefore(warn, els.thread.firstChild);
    }
  }

  function renderThread(c) {
    els.thread.textContent = '';
    if (!c.messages.length) {
      var hint = elem('div', 'vtalk-hint');
      hint.appendChild(elem('p', null, 'No messages yet. Say hello — it will vanish once they read it.'));
      els.thread.appendChild(hint);
    } else {
      c.messages.forEach(function (m) {
        els.thread.appendChild(m.node);
        // Opening the thread reveals any incoming messages that arrived while it
        // was in the background — start their burn-after-read countdown now.
        if (!m.mine) armBurn(m);
      });
    }
    reachabilityBanner(c.peer);
    scrollDown();
  }
  function scrollDown() { els.thread.scrollTop = els.thread.scrollHeight; }

  // ── Message bubbles ─────────────────────────────────────────────────────────

  function bubble(m) {
    var row = elem('div', 'vtalk-msg vtalk-msg--' + (m.mine ? 'out' : 'in'));
    var body = elem('div', 'vtalk-bubble');
    body.appendChild(elem('div', 'vtalk-bubble-text', m.text));
    var foot = elem('div', 'vtalk-bubble-foot');
    foot.appendChild(elem('span', 'vtalk-bubble-time', fmtTime(m.createdAt)));
    if (m.mine) {
      m.statusEl = elem('span', 'vtalk-bubble-status', 'Sending…');
      foot.appendChild(m.statusEl);
    }
    // Authenticity: flag an incoming message we could not verify as coming from
    // the sender it claims (its signing key is not known here). Verified messages
    // stay unbadged to avoid noise.
    if (!m.mine && m.verified === false) {
      var unv = elem('span', 'vtalk-bubble-unverified', '⚠ unverified');
      unv.title = 'This message could not be verified as coming from the sender it claims.';
      foot.appendChild(unv);
    }
    // Burn countdown — hidden until the message is read (armed), then ticks down.
    m.burnEl = elem('span', 'vtalk-bubble-burn');
    m.burnEl.hidden = true;
    foot.appendChild(m.burnEl);
    body.appendChild(foot);
    if (m.mode === 'live') row.classList.add('vtalk-msg--live');
    row.appendChild(body);
    m.node = row;
    return row;
  }

  function addMessage(peer, m) {
    // Dedupe by server id: the console no longer read-destroys on delivery (so
    // the recipient's app keeps its queued copy), which means a stream reconnect
    // re-flushes messages we've already shown. Skip anything already on screen.
    if (m.id && byId[m.id]) return;
    var c = getConvo(peer);
    if (!c) return;
    bubble(m);
    c.messages.push(m);
    if (m.id) byId[m.id] = m;
    if (active === c.peer) {
      var hint = els.thread.querySelector('.vtalk-hint');
      if (hint) hint.parentNode.removeChild(hint);
      els.thread.appendChild(m.node);
      scrollDown();
    } else if (!m.mine) {
      c.unread++;
      c.dot.hidden = false;
      c.dot.textContent = String(c.unread);
      els.convos.insertBefore(c.item, els.convos.firstChild);
    }
    // Fallback: an unread message still vanishes when the server's unread hold
    // (expires_at) elapses, even if it is never opened here.
    scheduleExpiry(m);
    // Incoming messages start their burn-after-read countdown the moment they are
    // shown in the open thread (displaying them in the console IS reading them).
    if (!m.mine && active === peer) armBurn(m);
  }

  function scheduleExpiry(m) {
    if (!m.expiresAt) return;
    var ms = new Date(m.expiresAt).getTime() - Date.now();
    if (isNaN(ms)) return;
    if (ms < 0) ms = 0;
    m.timer = setTimeout(function () { expireMessage(m); }, Math.min(ms, 2147483647));
  }

  // ── Burn-after-read countdown ────────────────────────────────────────────────
  // A single shared ticker updates every armed message once a second and burns
  // the ones whose timer has elapsed. This keeps the DOM churn bounded no matter
  // how many messages are counting down.
  var burning = [];
  var burnTicker = null;
  // signalRead tells the server an incoming message has been read. Only the Tor
  // world console does this (it is the sole reader there); the server read-destroys
  // and, for a message from another .onion, forwards a "read" receipt over Tor so
  // the sender sees it. Fire-and-forget, once per message. (ADR-0142)
  function signalRead(m) {
    if (!onionWorld || !m || m.mine || !m.id || m.readSignaled) return;
    m.readSignaled = true;
    fetch('/os/talk/read', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': cookie('vp_csrf') },
      body: JSON.stringify({ id: m.id })
    }).catch(function () {});
  }
  function armBurn(m) {
    if (!m || m.armed) return;
    if (!m.mine) signalRead(m);
    m.armed = true;
    var secs = (m.mode === 'live') ? LIVE_GRACE_SECONDS : (m.burnSeconds || 300);
    m.burnAt = Date.now() + secs * 1000;
    if (m.node) m.node.classList.add('vtalk-msg--armed');
    burning.push(m);
    tickBurns();
    if (!burnTicker) burnTicker = setInterval(tickBurns, 1000);
  }
  function fmtCountdown(sec) {
    if (sec >= 3600) return Math.round(sec / 3600) + 'h';
    if (sec >= 60) { var mm = Math.floor(sec / 60), ss = sec % 60; return mm + ':' + (ss < 10 ? '0' : '') + ss; }
    return sec + 's';
  }
  function tickBurns() {
    var now = Date.now();
    for (var i = burning.length - 1; i >= 0; i--) {
      var m = burning[i];
      var remain = Math.round((m.burnAt - now) / 1000);
      if (remain <= 0) {
        burning.splice(i, 1);
        expireMessage(m);
        continue;
      }
      if (m.burnEl) {
        m.burnEl.hidden = false;
        m.burnEl.textContent = '🔥 ' + fmtCountdown(remain);
      }
    }
    if (!burning.length && burnTicker) { clearInterval(burnTicker); burnTicker = null; }
  }
  function expireMessage(m) {
    if (m.timer) { clearTimeout(m.timer); m.timer = null; }
    if (m.node && m.node.parentNode) {
      m.node.classList.add('vtalk-msg--gone');
      setTimeout(function () { if (m.node && m.node.parentNode) m.node.parentNode.removeChild(m.node); }, 400);
    }
    var c = convos[m.peer];
    if (c) { var i = c.messages.indexOf(m); if (i >= 0) c.messages.splice(i, 1); }
    if (m.id) delete byId[m.id];
  }
  function setStatus(m, label, cls) {
    if (!m || !m.statusEl) return;
    m.statusEl.textContent = label;
    m.statusEl.className = 'vtalk-bubble-status' + (cls ? ' ' + cls : '');
  }

  // ── Stream ──────────────────────────────────────────────────────────────────

  var es = null;
  function connect() {
    if (es) es.close();
    es = new EventSource('/os/talk/stream?as=' + encodeURIComponent(currentSelf));
    es.addEventListener('open', function () { markStatus('online', 'Online'); });
    es.addEventListener('error', function () { markStatus('offline', 'Reconnecting…'); });
    es.addEventListener('message', function (e) {
      var d = parse(e.data);
      if (!d || !d.from) return;
      addMessage(d.from, {
        peer: norm(d.from), mine: false, text: d.text || '',
        id: d.id, createdAt: d.created_at, expiresAt: d.expires_at,
        burnSeconds: d.burn_seconds, mode: d.mode, verified: !!d.verified
      });
    });
    es.addEventListener('receipt', function (e) {
      var d = parse(e.data);
      if (!d || !d.id) return;
      var m = byId[d.id];
      if (!m) return;
      if (d.status === 'read') {
        // The recipient opened it: mark read and start our copy's burn countdown
        // so the sender's view disappears on the same timer.
        setStatus(m, 'Read', 'is-read');
        armBurn(m);
      } else if (d.status === 'expired') {
        setStatus(m, 'Expired', 'is-expired');
      }
    });
    es.addEventListener('ping', function () { markStatus('online', 'Online'); });
  }
  function markStatus(state, label) {
    if (!els.status) return;
    els.status.setAttribute('data-state', state);
    els.status.textContent = label;
  }

  // ── Sending ─────────────────────────────────────────────────────────────────

  function send() {
    var text = els.input.value.trim();
    if (!text || !active) return;
    var burn = parseInt(els.ttl && els.ttl.value, 10) || 300; // burn-after-read seconds
    var live = !!(els.live && els.live.checked);
    var mode = live ? 'live' : 'store';
    var to = active;

    // store mode: delivered live if they're connected now, otherwise queued and
    // delivered the moment they next connect. live mode: never stored, delivered
    // only if they're online now. Either way the message burns after it's read.
    var m = { peer: to, mine: true, text: text, createdAt: new Date().toISOString(), mode: mode, burnSeconds: burn };
    addMessage(to, m);
    els.input.value = '';
    autogrow();

    fetch('/os/talk/send', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': cookie('vp_csrf') },
      body: JSON.stringify({ to: to, text: text, ttl_seconds: burn, mode: mode, as: currentSelf })
    }).then(function (r) {
      return r.json().then(function (j) { return { ok: r.ok, j: j }; }, function () { return { ok: r.ok, j: null }; });
    }).then(function (res) {
      if (!res.ok) {
        var msg = (res.j && res.j.error && res.j.error.message) || 'Could not send';
        setStatus(m, msg, 'is-error');
        return;
      }
      if (res.j.id) { m.id = res.j.id; byId[m.id] = m; }
      // Outgoing messages do NOT start their countdown until the recipient reads
      // them (a 'read' receipt arms the burn). Live messages that couldn't be
      // delivered (recipient offline) are gone already.
      if (live && !res.j.delivered) { setStatus(m, 'Not delivered — they’re offline', 'is-error'); return; }
      setStatus(m, res.j.delivered ? 'Delivered' : 'Sent');
    }).catch(function () { setStatus(m, 'Network error — not sent', 'is-error'); });
  }

  function autogrow() {
    els.input.style.height = 'auto';
    els.input.style.height = Math.min(els.input.scrollHeight, 160) + 'px';
  }

  // ── Wiring ──────────────────────────────────────────────────────────────────

  els.newchat.addEventListener('submit', function (e) {
    e.preventDefault();
    var peer = norm(els.peer.value);
    if (!peer || peer === currentSelf) { els.peer.value = ''; return; }
    els.peer.value = '';
    activate(peer);
  });

  // "Chat as" switcher: a different identity is a different inbox and a
  // different key, so we tear the session down and reconnect cleanly.
  if (asSel) {
    asSel.addEventListener('change', function () { switchIdentity(norm(asSel.value)); });
  }
  function switchIdentity(next) {
    if (!next || next === currentSelf) return;
    currentSelf = next;
    // Reset all conversation state — nothing carries across identities.
    convos = Object.create(null);
    byId = Object.create(null);
    peerInfo = Object.create(null);
    verified = Object.create(null);
    active = '';
    els.convos.textContent = '';
    els.head.textContent = '';
    els.thread.textContent = '';
    // Contacts are per identity — reload the new self's kept rows.
    loadContacts();
    restoreKeptContacts();
    els.main.setAttribute('data-empty', '1');
    els.input.disabled = true;
    els.send.disabled = true;
    var av = root.querySelector('.vtalk-identity .vm-av');
    if (av) av.textContent = initials(currentSelf);
    // Refresh our own safety number for the new identity, then reconnect.
    selfFp = '';
    fetch('/os/talk/peer?email=' + encodeURIComponent(currentSelf), { headers: { 'Accept': 'application/json' } })
      .then(function (r) { return r.json(); })
      .then(function (j) { if (j && j.safety) selfFp = j.safety; })
      .catch(function () {});
    markStatus('connecting', 'Connecting…');
    connect();
  }
  els.composer.addEventListener('submit', function (e) { e.preventDefault(); send(); });
  els.input.addEventListener('keydown', function (e) {
    if (e.key === 'Enter' && !e.shiftKey) { e.preventDefault(); send(); }
  });
  els.input.addEventListener('input', autogrow);

  // Restore kept contacts for the starting identity before the stream opens, so a
  // reload brings back the ids you chose to keep (never their messages).
  loadContacts();
  restoreKeptContacts();
  connect();
  window.addEventListener('beforeunload', function () { if (es) es.close(); });
})();
