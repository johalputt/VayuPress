// SPDX-License-Identifier: Apache-2.0
// admin-os-drive.js — the Drive page (vayuos_mail_drive.go).
//
// Sends the files a holder chooses or drops, one at a time, and says how far
// along each is; opens the entry sheet when its body arrives. A file goes up
// as one request the server streams to disk, so nothing here reads it into
// memory.
(function () {
  "use strict";
  var drive = document.querySelector("[data-drive]");
  if (!drive) return;
  var status = document.getElementById("drive-status");
  var say = function (text) { if (status) status.textContent = text; };

  // One file, as a promise of nothing or of the server's reason.
  function send(file, dir, n, of) {
    return new Promise(function (done, refused) {
      var x = new XMLHttpRequest();
      var form = new FormData();
      form.append("file", file, file.name);
      x.open("POST", "/os/vayumail/drive/upload?dir=" + encodeURIComponent(dir));
      x.setRequestHeader("X-CSRF-Token", window.vpCsrf ? window.vpCsrf() : "");
      var which = of > 1 ? " (" + n + " of " + of + ")" : "";
      x.upload.onprogress = function (e) {
        if (e.lengthComputable) say("Uploading " + file.name + which + ": " + Math.floor(e.loaded * 100 / e.total) + "%");
      };
      x.onload = function () {
        if (x.status >= 200 && x.status < 300) return done();
        var why = "it could not be kept";
        try { why = JSON.parse(x.responseText).error.message || why; } catch (e) { /* not the server's words */ }
        refused(file.name + ": " + why);
      };
      x.onerror = function () { refused(file.name + ": the connection was lost"); };
      say("Uploading " + file.name + which + "…");
      x.send(form);
    });
  }

  var busy = false;
  function sendAll(files, dir) {
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
      send(list[i], dir, i + 1, list.length).then(function () { kept++; i++; next(); }, function (why) {
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
    sendAll(input.files, input.getAttribute("data-dir"));
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
      sendAll(e.dataTransfer.files, drive.getAttribute("data-dir"));
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
