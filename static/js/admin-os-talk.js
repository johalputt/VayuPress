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
    var hm = (h < 10 ? '0' : '') + h + ':' + (m < 10 ? '0' : '') + m;
    // A bare HH:MM is ambiguous next to the server's 24h unread hold, so anything
    // not from today carries its date.
    var now = new Date();
    if (d.getFullYear() === now.getFullYear() && d.getMonth() === now.getMonth() && d.getDate() === now.getDate()) {
      return hm;
    }
    var mm = d.getMonth() + 1, dd = d.getDate();
    return (dd < 10 ? '0' : '') + dd + '/' + (mm < 10 ? '0' : '') + mm + ' ' + hm;
  }
  function elem(tag, cls, text) {
    var n = document.createElement(tag);
    if (cls) n.className = cls;
    if (text != null) n.textContent = text;
    return n;
  }
  // Line icons for Talk's controls and badges, drawn in currentColor like the
  // rest of the console. Emoji rendered as colour pictograms that differ on every
  // platform, beside the console's own monochrome icons.
  var ICONS = {
    pin: 'M9 4h6l-1 6 3 3H7l3-3zM12 13v7',
    edit: 'M4 20h4L19 9l-4-4L4 16zM14 6l4 4',
    shield: 'M12 3l7 3v5c0 5-3.5 8-7 10-3.5-2-7-5-7-10V6z',
    flame: 'M12 3c1 3 5 5.5 5 10a5 5 0 0 1-10 0c0-2.5 1.5-4 2.5-5.5.5 1.5 1.5 2.5 2.5 3 .3-2.5 0-5-0-7.5z',
    alert: 'M12 4l9 16H3zM12 10v4M12 17.5v.01'
  };
  function icon(name) {
    var svg = document.createElementNS('http://www.w3.org/2000/svg', 'svg');
    svg.setAttribute('viewBox', '0 0 24 24');
    svg.setAttribute('class', 'vtalk-ico');
    svg.setAttribute('aria-hidden', 'true');
    var path = document.createElementNS('http://www.w3.org/2000/svg', 'path');
    path.setAttribute('d', ICONS[name]);
    svg.appendChild(path);
    return svg;
  }
  // iconText replaces an element's content with an icon and a label.
  function iconText(el, name, text) {
    el.textContent = '';
    el.appendChild(icon(name));
    el.appendChild(document.createTextNode(text));
    return el;
  }
  function parse(s) { try { return JSON.parse(s); } catch (_) { return null; } }

  // avatarEl builds a round avatar chip that shows the address' real mailbox
  // picture when the server has one (your own identity, or any local mailbox),
  // falling back to coloured initials otherwise. The <img> load/error handlers are
  // attached in JS (never an inline onerror) so it stays strict-CSP-safe: a 404
  // (no picture) simply keeps the initials. It is same-origin only and never
  // reaches another host, so it is safe in the Tor world too.
  function avatarEl(addr, label) {
    var el = elem('span', 'vtalk-avatar', initials(label || addr));
    var a = norm(addr);
    if (!a) return el;
    var img = document.createElement('img');
    img.alt = '';
    img.addEventListener('load', function () {
      el.textContent = '';
      el.appendChild(img);
      el.classList.add('vtalk-avatar--img');
    });
    img.src = '/os/vayumail/accounts/avatar?email=' + encodeURIComponent(a);
    return el;
  }

  // ── New-message notifications ────────────────────────────────────────────────
  // A peer's message that you are not actively looking at flashes the tab title
  // and (when the tab is in the background and permission was granted) raises a
  // desktop notification — sender name only, never the message text, to preserve
  // the private/ephemeral ethos. Desktop notifications need a secure context, so
  // over an http onion they are skipped gracefully and the title badge still works.
  var baseTitle = document.title;
  var unreadTotal = 0;
  var notifyReady = false;
  var notifyAsked = false;
  // ask=false only picks up a permission already granted. The browser's prompt
  // is shown only from a person's own action and at most once per visit: one
  // that greeted every page load and came back with every new chat is the kind
  // people learn to deny, which then can never be undone from this page.
  function ensureNotifyPermission(ask) {
    if (!('Notification' in window)) return;
    if (Notification.permission === 'granted') { notifyReady = true; return; }
    if (!ask || notifyAsked || Notification.permission !== 'default') return;
    notifyAsked = true;
    try { Notification.requestPermission().then(function (p) { notifyReady = (p === 'granted'); }); } catch (_) {}
  }
  function clearTitleBadge() { unreadTotal = 0; document.title = baseTitle; }
  function notifyIncoming(peer, m) {
    if (!m || m.mine) return;
    var lookingHere = (active === peer) && !document.hidden;
    if (lookingHere) return;
    unreadTotal++;
    document.title = '(' + unreadTotal + ') ' + baseTitle;
    announce('New message from ' + displayName(peer));
    if (notifyReady && document.hidden && ('Notification' in window)) {
      try {
        var n = new Notification(displayName(peer), { body: 'New message', tag: 'vtalk:' + peer });
        n.onclick = function () { try { window.focus(); } catch (_) {} activate(peer); clearTitleBadge(); n.close(); };
      } catch (_) {}
    }
  }
  window.addEventListener('focus', clearTitleBadge);
  document.addEventListener('visibilitychange', function () { if (!document.hidden) clearTitleBadge(); });

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
    var av = avatarEl(peer, name);
    var meta = elem('span', 'vtalk-convo-meta');
    var nameEl = elem('span', 'vtalk-convo-name', name);
    meta.appendChild(nameEl);
    var pin = iconText(elem('span', 'vtalk-convo-pin'), 'pin', '');
    pin.title = 'Kept — stays in your list after reload';
    pin.hidden = !isKept(peer);
    if (isKept(peer)) item.classList.add('vtalk-convo--kept');
    var dot = elem('span', 'vtalk-convo-badge');
    dot.hidden = true;
    item.appendChild(av);
    item.appendChild(meta);
    item.appendChild(pin);
    item.appendChild(dot);
    // A conversation row is the primary navigation of this page, so it has to be
    // operable without a mouse: a real button role, a tab stop, and Enter/Space.
    item.setAttribute('role', 'button');
    item.tabIndex = 0;
    item.addEventListener('click', function () { activate(peer); });
    item.addEventListener('keydown', function (e) {
      if (e.key === 'Enter' || e.key === ' ' || e.key === 'Spacebar') {
        e.preventDefault();
        activate(peer);
      }
    });
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
    // Phone layout: opening a conversation switches to the thread view. The CSS
    // only honours this under the mobile breakpoint, so desktop is unaffected.
    root.setAttribute('data-view', 'thread');
    c.unread = 0;
    c.dot.hidden = true;
    c.dot.textContent = '';
    Object.keys(convos).forEach(function (k) {
      var on = k === peer;
      convos[k].item.classList.toggle('vtalk-convo--active', on);
      // The keyboard user needs the selection announced, not just shaded.
      if (on) convos[k].item.setAttribute('aria-current', 'true');
      else convos[k].item.removeAttribute('aria-current');
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
    var av = avatarEl(peer, name);
    // Back to the list. Only visible in the mobile thread view (see the CSS);
    // on desktop both panes are on screen, so it stays hidden.
    var back = elem('button', 'vtalk-head-btn vtalk-back');
    back.type = 'button';
    back.textContent = '← Chats';
    back.setAttribute('aria-label', 'Back to conversations');
    back.addEventListener('click', function () { root.setAttribute('data-view', 'list'); });
    els.head.appendChild(back);
    var hmeta = elem('div', 'vtalk-head-meta');
    hmeta.appendChild(elem('strong', null, name));
    hmeta.appendChild(elem('span', 'text-sm muted', 'End-to-end encrypted · disappears when read'));
    // Presence: a separate, honest line. See paintPresence for why the "offline"
    // wording is about a connection, not about the person.
    var presence = elem('span', 'vtalk-presence');
    presence.id = 'vtalk-presence';
    paintPresence(peer);
    hmeta.appendChild(presence);
    // Messages that arrived but could not be read: the sender is known (routing
    // metadata), the content is not. Saying so beats a silent gap.
    var convo = convos[peer];
    if (convo && convo.undecryptable) {
      hmeta.appendChild(iconText(elem('span', 'vtalk-undec'), 'alert',
        convo.undecryptable + ' message' + (convo.undecryptable === 1 ? '' : 's') + ' couldn’t be decrypted'));
    }

    var actions = elem('div', 'vtalk-head-actions');

    // Rename: choose a name to show instead of the address (the address itself is
    // never shown outside the verify panel once a chat is added).
    var rbtn = elem('button', 'vtalk-head-btn');
    rbtn.type = 'button';
    iconText(rbtn, 'edit', 'Rename');
    rbtn.title = 'Show a name instead of the address';
    rbtn.addEventListener('click', function () { renameContact(peer); });

    // Keep: pin the contact so its row survives a reload (chats are ephemeral by
    // default — nothing but this flag and the chosen name is ever persisted).
    var kbtn = elem('button', 'vtalk-head-btn');
    kbtn.type = 'button';
    iconText(kbtn, 'pin', isKept(peer) ? 'Kept' : 'Keep');
    kbtn.classList.toggle('is-on', isKept(peer));
    kbtn.title = isKept(peer)
      ? 'This contact stays in your list after reload'
      : 'Keep this contact in your list after reload';
    kbtn.addEventListener('click', function () { toggleKeep(peer); });

    var vbtn = elem('button', 'vtalk-verify-btn');
    vbtn.type = 'button';
    setVerifyBtn(vbtn, peer);
    // The shield opens a panel: assistive tech needs to know that, and whether
    // it is currently open.
    vbtn.setAttribute('aria-expanded', 'false');
    vbtn.setAttribute('aria-controls', 'vtalk-verify');
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
  // Rename edits in place: window.prompt is an unstyled browser blob that blocks
  // the tab and ignores the console's own look and keyboard idioms.
  function renameContact(peer) {
    peer = norm(peer);
    var open = document.getElementById('vtalk-rename');
    if (open) { open.parentNode.removeChild(open); return; } // second click closes
    var row = elem('div', 'vtalk-rename');
    row.id = 'vtalk-rename';
    var input = document.createElement('input');
    input.className = 'input input--sm';
    input.type = 'text';
    input.value = (contacts[peer] && contacts[peer].name) || '';
    input.placeholder = displayName(peer);
    input.setAttribute('aria-label', 'Contact name');
    var save = elem('button', 'btn btn--sm btn--primary', 'Save'); save.type = 'button';
    var cancel = elem('button', 'btn btn--sm btn--ghost', 'Cancel'); cancel.type = 'button';
    function closeRow() { if (row.parentNode) row.parentNode.removeChild(row); }
    function commit() {
      var next = input.value.trim();
      var e = contactEntry(peer, true);
      e.name = next;
      if (!e.name && !e.kept) delete contacts[peer];
      saveContacts();
      closeRow();
      refreshContactUI(peer);
    }
    save.addEventListener('click', commit);
    cancel.addEventListener('click', closeRow);
    input.addEventListener('keydown', function (ev) {
      if (ev.key === 'Enter') { ev.preventDefault(); commit(); }
      else if (ev.key === 'Escape') { ev.preventDefault(); closeRow(); }
    });
    row.appendChild(input); row.appendChild(save); row.appendChild(cancel);
    els.head.appendChild(row);
    input.focus();
    input.select();
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
      // Only refresh the initials fallback — never wipe a loaded picture.
      if (c.av && !c.av.querySelector('img')) c.av.textContent = initials(name);
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
    iconText(btn, 'shield', verified[peer] ? 'Verified' : 'Verify');
    btn.classList.toggle('is-verified', !!verified[peer]);
  }

  function toggleVerify(peer) {
    var panel = document.getElementById('vtalk-verify');
    if (!panel) return;
    var btn = els.head.querySelector('.vtalk-verify-btn');
    if (!panel.hidden) {
      panel.hidden = true;
      if (btn) btn.setAttribute('aria-expanded', 'false');
      return;
    }
    renderVerify(panel, peer);
    panel.hidden = false;
    if (btn) btn.setAttribute('aria-expanded', 'true');
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

  // paintPresence renders the presence line in the thread header.
  //
  // The claim is deliberately one-sided. "Online" is asserted only when the server
  // reports a live stream for that identity, which is the only thing the data
  // supports — and it is exactly what makes instant delivery a promise rather than
  // a hope. The negative says "no client connected right now", NEVER "offline" or
  // "won't get it": a store-mode message still queues and arrives when they
  // connect. Guessing about someone else's phone is how a chat app loses trust.
  function paintPresence(peer) {
    var el = document.getElementById('vtalk-presence');
    if (!el) return;
    var info = peerInfo[peer];
    if (!info) {
      el.textContent = 'checking…';
      el.className = 'vtalk-presence';
      return;
    }
    if (info.online) {
      el.textContent = '● online now — delivers instantly';
      el.className = 'vtalk-presence vtalk-presence--on';
      return;
    }
    el.textContent = '○ no client connected right now — store-mode messages queue';
    el.className = 'vtalk-presence';
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
        var stored = lsGet(verifyKey(peer));
        peerInfo[peer] = {
          found: !!j.found,
          safety: j.safety || '',
          fingerprint: j.fingerprint || '',
          online: !!j.online // advisory, one-sided: see paintPresence
        };
        if (j.found && j.fingerprint && stored === j.fingerprint) {
          // The same key we verified before — keep the mark, say nothing.
          setVerified(peer, true, false);
        } else if (j.found && j.fingerprint && stored) {
          // We verified a DIFFERENT key for this contact earlier. In an
          // end-to-end-encrypted chat that is the one event that must never pass
          // quietly, so the mark drops and a banner stays up until they compare
          // safety numbers again.
          peerInfo[peer].keyChanged = true;
          setVerified(peer, false, false);
        }
        if (active === peer) {
          paintPresence(peer);
          reachabilityBanner(peer);
          var btn = els.head.querySelector('.vtalk-verify-btn');
          if (btn) setVerifyBtn(btn, peer);
          var panel = document.getElementById('vtalk-verify');
          if (panel && !panel.hidden) renderVerify(panel, peer);
        }
      })
      .catch(function () {
        // Offline: report that the CHECK failed rather than leaving a bare "—"
        // that reads like a verdict about the peer's key.
        peerInfo[peer] = { found: null, unknown: true };
        if (active === peer) reachabilityBanner(peer);
      });
  }

  // setVerified updates state, the conversation badge, and (when persist) the
  // remembered fingerprint.
  function setVerified(peer, on, persist) {
    verified[peer] = on;
    var c = convos[peer];
    if (c) c.item.classList.toggle('vtalk-convo--verified', on);
    if (persist) {
      var info = peerInfo[peer];
      if (on && info && info.fingerprint) {
        lsSet(verifyKey(peer), info.fingerprint);
        // A fresh comparison supersedes the "their number changed" alarm.
        delete info.keyChanged;
        if (active === peer) reachabilityBanner(peer);
      } else {
        lsDel(verifyKey(peer));
      }
    }
  }

  // reachabilityBanner paints the thread's single warning strip: either "their
  // safety number changed since you verified" (the loud one) or "no key yet, we
  // can't reach them" (the actionable one). The two are mutually exclusive.
  function reachabilityBanner(peer) {
    Array.prototype.forEach.call(els.thread.querySelectorAll('.vtalk-warn'), function (n) {
      if (n.parentNode) n.parentNode.removeChild(n);
    });
    var info = peerInfo[peer];
    if (!info) return;
    var warn = null;
    if (info.keyChanged) {
      warn = iconText(elem('div', 'vtalk-warn vtalk-warn--key'), 'alert',
        displayName(peer) + '’s safety number has changed since you verified it. ' +
        'That is what happens when the key is rebuilt — or when someone is in the middle. ' +
        'Compare safety numbers again before trusting this chat.');
    } else if (info.unknown) {
      warn = elem('div', 'vtalk-warn vtalk-warn--unknown',
        'Could not check whether ' + displayName(peer) + ' can receive messages just now — you may be offline. Sending will report the real result.');
    } else if (info.found === false) {
      warn = iconText(elem('div', 'vtalk-warn'), 'alert',
        'No VayuTalk key for ' + peer + ' yet. They need to open VayuMail (app or web) on this server once so a key exists — until then messages can’t be delivered.');
    }
    if (warn) els.thread.insertBefore(warn, els.thread.firstChild);
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
      var unv = iconText(elem('span', 'vtalk-bubble-unverified'), 'alert', 'unverified');
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
    // Dedupe by server id: the console marks messages it has shown as read for
    // this reader (the server's cursor does the same on its side), but it never
    // read-destroys them — the recipient's app keeps its queued copy, so a
    // reconnect can still re-flush something we have shown. Skip anything already
    // on screen.
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
    // Tab-title badge + (backgrounded) desktop notification for a peer message
    // you are not currently looking at — including brand-new conversations.
    notifyIncoming(peer, m);
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
  // signalRead tells the server that this console has displayed an incoming
  // message, so a stream reconnect does not re-flush it (the server keeps a
  // per-reader cursor). In the clearnet world that mark is non-destructive: the
  // phone app is still the reader that destroys it, and its copy is untouched. In
  // the Tor world the console is the sole reader, so there the same call
  // read-destroys and forwards a "read" receipt over Tor. Fire-and-forget, once
  // per message. (ADR-0141, ADR-0142)
  function signalRead(m) {
    if (!m || m.mine || !m.id || m.readSignaled) return;
    m.readSignaled = true;
    fetch('/os/talk/read', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': cookie('vp_csrf') },
      body: JSON.stringify({ id: m.id, as: currentSelf })
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
        iconText(m.burnEl, 'flame', fmtCountdown(remain));
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
    // KEEP the id as a tombstone — do NOT delete byId[m.id].
    //
    // The server now keeps a per-reader cursor, so a reconnect should not re-flush
    // this envelope. This is the client-side half of the same promise: a read
    // signal lost in flight (offline, tab closed mid-request) must not be able to
    // resurrect a message the user just watched burn. Belt and braces, because the
    // product's headline claim depends on it. A few bytes, gone with the tab.
    if (m.id) m.expired = true;
  }
  function setStatus(m, label, cls) {
    if (!m || !m.statusEl) return;
    m.statusEl.textContent = label;
    m.statusEl.className = 'vtalk-bubble-status' + (cls ? ' ' + cls : '');
  }

  // ── Stream ──────────────────────────────────────────────────────────────────

  var es = null;
  var streamFails = 0;
  function connect() {
    if (es) es.close();
    streamFails = 0;
    es = new EventSource('/os/talk/stream?as=' + encodeURIComponent(currentSelf));
    es.addEventListener('open', function () { streamFails = 0; markStatus('online', 'Online'); });
    es.addEventListener('error', function () {
      // EventSource retries on its own forever. A server that keeps refusing —
      // the per-identity stream cap, a revoked session — would otherwise leave
      // the pill saying "Reconnecting…" for the rest of the session with no way
      // to tell it apart from a slow network. Bound the retries and name the
      // likely cause and the remedy.
      streamFails++;
      if (streamFails >= 5) {
        markStatus('failed', 'Can’t connect — close VayuTalk on another device or tab, then reload');
        if (es) es.close();
        es = null;
        return;
      }
      markStatus('offline', 'Reconnecting… (' + streamFails + ')');
    });
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
    // A peer's key arrived locally after their message did (a late over-Tor
    // fetch). Re-check the messages we already hold so the "unverified" badge
    // tells the truth now, instead of staying stale until a reload.
    es.addEventListener('peerkey', function (e) {
      var d = parse(e.data);
      if (!d || !d.peer) return;
      var peer = norm(d.peer);
      var c = convos[peer];
      if (c) {
        c.messages.forEach(function (m) { if (!m.mine) m.verified = true; });
        if (active === peer) renderThread(c);
      }
      // Re-run the preflight too: it restores the verification mark when the
      // fingerprint now matches what was verified, and refreshes the panel.
      preflightPeer(peer);
    });
    // A message reached us but its plaintext could not be recovered. Report the
    // shape of it (there is one, and who it came from) without content, so the
    // gap is visible rather than looking like silence.
    es.addEventListener('undecryptable', function (e) {
      var d = parse(e.data);
      if (!d || !d.from) return;
      var peer = norm(d.from);
      var c = getConvo(peer);
      if (!c) return;
      c.undecryptable = (c.undecryptable || 0) + 1;
      if (active === peer) buildHeader(peer);
      announce('A message from ' + displayName(peer) + ' could not be decrypted');
    });
  }
  function markStatus(state, label) {
    if (!els.status) return;
    els.status.setAttribute('data-state', state);
    els.status.textContent = label;
    announce(label);
  }

  // announce speaks a short line through the page's polite live region: the
  // status pill and arriving messages are changes to a page a screen-reader user
  // is otherwise never told about.
  function announce(text) {
    var region = document.getElementById('vtalk-live-region');
    if (region) region.textContent = text;
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
    postSend(m, { to: to, text: text, burn: burn, mode: mode, live: live });
  }

  // postSend delivers one message and reflects the outcome on its bubble. Retry
  // reuses it verbatim, so a failed send is one click from recovery and the text
  // the user typed is never thrown away.
  function postSend(m, p) {
    fetch('/os/talk/send', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': cookie('vp_csrf') },
      body: JSON.stringify({ to: p.to, text: p.text, ttl_seconds: p.burn, mode: p.mode, as: currentSelf })
    }).then(function (r) {
      return r.json().then(function (j) { return { ok: r.ok, j: j }; }, function () { return { ok: r.ok, j: null }; });
    }).then(function (res) {
      if (!res.ok) {
        // The server's reason is the useful one (no key for that address, queue
        // full, message too large) — fall back only when the body carried none.
        setStatus(m, (res.j && res.j.error && res.j.error.message) || 'Could not send', 'is-error');
        offerRetry(m, p);
        return;
      }
      if (res.j.id) { m.id = res.j.id; byId[m.id] = m; }
      // Outgoing messages do NOT start their countdown until the recipient reads
      // them (a 'read' receipt arms the burn). Live messages that couldn't be
      // delivered (recipient offline) are gone already.
      if (p.live && !res.j.delivered) { setStatus(m, 'Not delivered — they’re offline', 'is-error'); return; }
      setStatus(m, res.j.delivered ? 'Delivered' : 'Sent');
    }).catch(function () {
      setStatus(m, 'Network error — not sent', 'is-error');
      offerRetry(m, p);
    });
  }

  // offerRetry attaches a single resend control to a failed bubble (the original
  // text is already on it). Note the honest trade: when a response is lost after
  // the server accepted it, retrying can deliver a duplicate — which beats
  // silently dropping what someone wrote.
  function offerRetry(m, p) {
    if (!m.node || m.retryEl) return;
    var b = elem('button', 'vtalk-bubble-retry', '↻ Retry');
    b.type = 'button';
    b.title = 'Send this message again';
    b.addEventListener('click', function () {
      if (b.parentNode) b.parentNode.removeChild(b);
      m.retryEl = null;
      setStatus(m, 'Sending…');
      postSend(m, p);
    });
    var foot = m.node.querySelector('.vtalk-bubble-foot');
    (foot || m.node).appendChild(b);
    m.retryEl = b;
  }

  function autogrow() {
    els.input.style.height = 'auto';
    els.input.style.height = Math.min(els.input.scrollHeight, 160) + 'px';
  }

  // ── Wiring ──────────────────────────────────────────────────────────────────

  els.newchat.addEventListener('submit', function (e) {
    e.preventDefault();
    var peer = norm(els.peer.value);
    // Say why nothing happened. Silently clearing the box looked like a broken
    // Start button — most often because someone typed their own address.
    if (!peer) { newChatNote('Enter an address (or paste a code) to start a chat.'); return; }
    if (peer === currentSelf) { newChatNote('That is your own address — start a chat with someone else.'); return; }
    els.peer.value = '';
    // Starting a chat is a user gesture — the right moment to ask to notify.
    ensureNotifyPermission(true);
    activate(peer);
  });

  // newChatNote shows a short, self-clearing hint under the new-chat box.
  function newChatNote(text) {
    var n = document.getElementById('vtalk-newchat-note');
    if (!n) return;
    n.textContent = text;
    n.hidden = false;
    if (newChatNoteTimer) clearTimeout(newChatNoteTimer);
    newChatNoteTimer = setTimeout(function () { n.hidden = true; }, 4500);
  }
  var newChatNoteTimer = null;

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
    // A different identity has no open conversation, so the phone layout returns
    // to the list rather than staring at an empty thread.
    root.setAttribute('data-view', 'list');
    els.input.disabled = true;
    els.send.disabled = true;
    // Drop the half-written message too: a draft belongs to the identity it was
    // typed as, and sending it from another mailbox would be a leak, not a
    // convenience.
    els.input.value = '';
    autogrow();
    var av = root.querySelector('.vtalk-identity .vm-av');
    if (av) av.textContent = initials(currentSelf);
    // The share panel belongs to the identity too: its link and QR would
    // otherwise invite people to chat with the mailbox just switched away from.
    var shareQR = root.querySelector('[data-share-qr]');
    var shareLink = root.querySelector('[data-share-link]');
    var shareCopy = root.querySelector('[data-share-copy]');
    if (shareLink) {
      var link = shareLink.textContent.replace(/\?t=.*$/, '?t=' + encodeURIComponent(currentSelf));
      shareLink.textContent = link;
      if (shareCopy) shareCopy.setAttribute('data-copy', link);
    }
    if (shareQR) shareQR.src = '/os/talk/qr?as=' + encodeURIComponent(currentSelf);
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
  // Start on the conversation list: on a phone that is the only pane that makes
  // sense before a chat is chosen (the CSS ignores this on desktop).
  root.setAttribute('data-view', 'list');
  // Notifications already allowed work from the first message; asking waits for
  // Start. The tab-title badge works either way.
  ensureNotifyPermission(false);
  connect();
  window.addEventListener('beforeunload', function () { if (es) es.close(); });
})();
