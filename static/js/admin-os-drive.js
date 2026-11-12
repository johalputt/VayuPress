// SPDX-License-Identifier: Apache-2.0
// admin-os-drive.js — the Drive page (vayuos_mail_drive.go).
//
// Sends the files a holder chooses or drops, one at a time, in pieces the
// server seals as they come (vayuos_mail_drive_upload.go), and says how far
// along each is. A dropped connection is waited out and the upload goes on
// from where the server says it stands, so a large file is not sent again
// from its start. Opens the entry sheet when its body arrives.
(function () {
  "use strict";
  var drive = document.querySelector("[data-drive]");
  if (!drive) return;
  var status = document.getElementById("drive-status");
  var say = function (text) { if (status) status.textContent = text; };
  var token = function () { return window.vpCsrf ? window.vpCsrf() : ""; };

  // The server's answer as an object, or its words as a refusal.
  function ask(method, url, body) {
    return fetch(url, { method: method, body: body, credentials: "same-origin", headers: { "X-CSRF-Token": token() } }).then(function (res) {
      return res.json().catch(function () { return {}; }).then(function (json) {
        if (res.ok) return json;
        var err = new Error((json.error && json.error.message) || "it could not be kept");
        err.status = res.status;
        err.received = json.received;
        throw err;
      });
    });
  }

  // One file, in pieces, as a promise of nothing or of the server's reason.
  // grant: a folder another mailbox shares with this one, "" for its own.
  function send(file, dir, grant, n, of) {
    var which = of > 1 ? " (" + n + " of " + of + ")" : "";
    // As a form is sent, not multipart: the server reads these as one.
    var form = new URLSearchParams({ dir: dir, name: file.name, size: String(file.size), type: file.type || "" });
    if (grant) form.set("grant", grant);
    say("Uploading " + file.name + which + "…");
    return ask("POST", "/os/vayumail/drive/uploads", form).then(function (up) {
      var url = "/os/vayumail/drive/uploads/" + encodeURIComponent(up.id);
      var tries = 0;
      function from(at) {
        var piece = file.slice(at, Math.min(at + up.piece, file.size));
        say("Uploading " + file.name + which + ": " + (file.size ? Math.floor(at * 100 / file.size) : 0) + "%");
        return ask("PUT", url + "?offset=" + at, piece).then(function (st) {
          tries = 0;
          if (st.file || st.received >= file.size) return;
          return from(st.received);
        }, again);
      }
      // The server's refusal of a piece stands; a lost connection, or a
      // piece the server says is not the next, is taken up again from where
      // the upload stands, after a wait that doubles, five times at most.
      function again(err) {
        if (err.status === 409 && typeof err.received === "number") return from(err.received);
        if (err.status || tries >= 5) throw err;
        tries++;
        say("Uploading " + file.name + which + ": the connection was lost; trying again…");
        return new Promise(function (wait) { setTimeout(wait, 2000 * Math.pow(2, tries - 1)); }).then(function () {
          return ask("GET", url).then(function (st) { return from(st.received); }, again);
        });
      }
      return from(0);
    }).catch(function (err) {
      throw file.name + ": " + (err.status ? err.message : "the connection was lost");
    });
  }

  var busy = false;
  function sendAll(files, dir, grant) {
    if (busy || !files || !files.length) return;
    busy = true;
    var list = Array.prototype.slice.call(files), i = 0, kept = 0;
    (function next() {
      if (i >= list.length) {
        // The list is the server's: read it again, and leave a refusal on
        // screen until then.
        say(kept === 1 ? "1 file kept." : kept + " files kept.");
        location.reload();
        return;
      }
      send(list[i], dir, grant, i + 1, list.length).then(function () { kept++; i++; next(); }, function (why) {
        busy = false;
        say(why + "." + (kept ? " " + kept + " kept before it." : ""));
        if (status) status.classList.add("drive-status--bad");
      });
    })();
  }

  document.addEventListener("change", function (e) {
    var input = e.target;
    if (!input.matches || !input.matches("[data-drive-upload]")) return;
    if (status) status.classList.remove("drive-status--bad");
    sendAll(input.files, input.getAttribute("data-dir"), input.getAttribute("data-grant"));
    input.value = "";
  });

  // Files dropped on the list go into the folder that is open. Trash takes
  // none: it has no upload either.
  var open = document.querySelector("[data-drive-upload]");
  if (open) {
    var over = function (e) {
      if (!e.dataTransfer || Array.prototype.indexOf.call(e.dataTransfer.types || [], "Files") < 0) return false;
      e.preventDefault();
      return true;
    };
    drive.addEventListener("dragover", function (e) { if (over(e)) drive.classList.add("drive--over"); });
    drive.addEventListener("dragleave", function (e) { if (e.target === drive) drive.classList.remove("drive--over"); });
    drive.addEventListener("drop", function (e) {
      if (!over(e)) return;
      drive.classList.remove("drive--over");
      if (status) status.classList.remove("drive-status--bad");
      sendAll(e.dataTransfer.files, drive.getAttribute("data-dir"), drive.getAttribute("data-grant"));
    });
  }

  var sheet = document.getElementById("drive-sheet");
  var body = document.getElementById("drive-sheet-body");
  if (body) {
    document.body.addEventListener("htmx:afterSwap", function (e) {
      if (e.detail.target !== body) return;
      var t = body.querySelector("[data-sheet-title]");
      var title = document.getElementById("drive-sheet-title");
      if (t && title) title.textContent = t.getAttribute("data-sheet-title");
      if (sheet && !sheet.open && sheet.showModal) sheet.showModal();
      var name = body.querySelector("form input[name=name]");
      if (name && t && t.getAttribute("data-sheet-title") === "New folder") name.focus();
    });
  }
})();
