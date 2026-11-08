// SPDX-License-Identifier: Apache-2.0
// admin-os-calendar.js — the Calendar page (vayuos_mail_calendar.go).
//
// The server lays the week out in minutes; this places each block, since
// the console's CSP takes no style attributes and the CSSOM is how a page
// positions things under it. It also keeps the browser's time zone in the
// cookie the server lays the page out by, opens the event sheet when its
// body arrives, and shows the editor's repeat fields as they apply.
(function () {
  "use strict";
  var cal = document.querySelector("[data-cal]");
  if (!cal) return;

  // The browser knows where the holder is; the server only knows once it is
  // told. One reload at most per session, so a browser that refuses the
  // cookie sees the install's zone, not a page that reloads for ever.
  try {
    var tz = Intl.DateTimeFormat().resolvedOptions().timeZone;
    if (tz && tz !== cal.getAttribute("data-zone") && !sessionStorage.getItem("vm-tz-tried")) {
      sessionStorage.setItem("vm-tz-tried", "1");
      document.cookie = "vm_tz=" + encodeURIComponent(tz) + "; path=/os/vayumail; max-age=31536000; SameSite=Lax" +
        (location.protocol === "https:" ? "; Secure" : "");
      location.reload();
      return;
    }
  } catch (e) { /* no Intl or no storage: the install's zone stands */ }

  var hourSpan = cal.querySelector(".cal-hours span");
  var hour = hourSpan ? hourSpan.getBoundingClientRect().height : 48;
  var px = function (mins) { return (mins * hour / 60) + "px"; };

  cal.querySelectorAll(".cal-ev").forEach(function (b) {
    var lanes = +b.getAttribute("data-lanes") || 1, lane = +b.getAttribute("data-lane") || 0;
    b.style.top = px(+b.getAttribute("data-top"));
    // Half an hour is the least a block is drawn, so its title fits.
    b.style.height = "calc(" + px(Math.max(+b.getAttribute("data-mins"), 30)) + " - 2px)";
    b.style.left = (lane * 100 / lanes) + "%";
    b.style.width = "calc(" + (100 / lanes) + "% - 4px)";
  });

  // The working day in view: the first event of the week, or half past seven.
  var scroll = cal.querySelector("[data-cal-scroll]");
  if (scroll) {
    var first = 7.5 * 60;
    cal.querySelectorAll(".cal-ev").forEach(function (b) { first = Math.min(first, +b.getAttribute("data-top")); });
    scroll.scrollTop = first * hour / 60;
  }

  // Now, on today's column, kept up to the minute.
  var today = cal.querySelector("[data-cal-today]");
  if (today) {
    var line = document.createElement("div");
    line.className = "cal-now";
    line.setAttribute("aria-hidden", "true");
    today.appendChild(line);
    var place = function () { var n = new Date(); line.style.top = px(n.getHours() * 60 + n.getMinutes()); };
    place();
    setInterval(place, 60000);
  }

  // A click on an empty part of a day starts an event at that hour.
  cal.addEventListener("click", function (e) {
    var col = e.target;
    if (!col.classList || !col.classList.contains("cal-col") || !window.htmx) return;
    var h = Math.floor((e.clientY - col.getBoundingClientRect().top) / hour);
    window.htmx.ajax("GET", "/os/vayumail/calendar/edit?d=" + col.getAttribute("data-day") + "&h=" + Math.max(0, Math.min(23, h)),
      { target: "#cal-sheet-body", swap: "innerHTML" });
  });

  var sheet = document.getElementById("cal-sheet");
  var body = document.getElementById("cal-sheet-body");

  // The editor shows what its choices use: an interval and an end for a
  // repeat, weekdays for a weekly one, times when not all day, and no
  // repeat at all for one occurrence of a series.
  function fit(form) {
    if (!form) return;
    var freq = form.querySelector("[data-cal-freq]");
    var f = freq ? freq.value : "";
    var scope = form.querySelector("[data-cal-scope]:checked");
    var one = scope && scope.value === "one";
    var allDay = form.querySelector("[data-cal-allday]");
    var rep = form.querySelector("[data-cal-repeat]");
    if (rep) rep.hidden = one;
    form.querySelectorAll("[data-cal-when-freq]").forEach(function (x) { x.hidden = f === "" || f === "keep"; });
    form.querySelectorAll("[data-cal-when-weekly]").forEach(function (x) { x.hidden = f !== "WEEKLY"; });
    form.querySelectorAll("[data-cal-time]").forEach(function (x) {
      x.hidden = !!(allDay && allDay.checked);
      var label = x.closest(".field");
      if (label && x.name === "zone") label.hidden = x.hidden;
    });
  }
  if (body) {
    body.addEventListener("change", function (e) { fit(e.target.closest("[data-cal-form]")); });
    document.body.addEventListener("htmx:afterSwap", function (e) {
      if (e.detail.target !== body) return;
      var t = body.querySelector("[data-sheet-title]");
      var title = document.getElementById("cal-sheet-title");
      if (t && title) title.textContent = t.getAttribute("data-sheet-title");
      fit(body.querySelector("[data-cal-form]"));
      if (sheet && !sheet.open && sheet.showModal) sheet.showModal();
      var focus = body.querySelector("input[name=title]");
      if (focus) focus.focus();
    });
  }
})();
