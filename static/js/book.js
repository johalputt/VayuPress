// SPDX-License-Identifier: Apache-2.0
// book.js — a booking page in the guest's own time zone (book_public.go).
// On the first visit to a page's days ([data-book-zone]) the page is asked
// for again with ?tz= set to the browser's zone; nowhere else, so a form's
// answer is never asked for again as a plain visit.
(function () {
  "use strict";
  if (!document.querySelector("[data-book-zone]")) return;
  try {
    var tz = Intl.DateTimeFormat().resolvedOptions().timeZone;
    var u = new URL(location.href);
    if (!tz || u.searchParams.get("tz")) return;
    u.searchParams.set("tz", tz);
    location.replace(u.toString());
  } catch (e) { /* no Intl: the page's own zone stands, and says so */ }
})();
