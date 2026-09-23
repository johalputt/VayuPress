/* admin-os-bundle.js — send a website .zip to the server in pieces, then deploy it.
 *
 * Pieces rather than one request because the proxies in front of an install
 * (nginx 50M, Caddy 50MiB, Cloudflare 100 MB) refuse a large body before the
 * server sees it. Each piece is acknowledged with the byte offset the server
 * now holds; a piece whose answer was lost is resent, and the server's 409
 * names the offset to resume from, so nothing is appended twice.
 *
 * vpBundleUpload(base, file, csrf, progress) resolves with the deploy result
 * ({files, bytes, skipped, skipped_names}) and rejects with an Error whose
 * message is for the operator. Shared by the primary and hosted-site pages. */
(function () {
  'use strict';

  function mb(n) {
    if (n >= 1073741824) return (n / 1073741824).toFixed(1) + ' GiB';
    return (n / 1048576).toFixed(1) + ' MiB';
  }
  function reason(r) {
    if (r.j && r.j.error && r.j.error.message) return r.j.error.message;
    if (r.status === 413) return 'A proxy in front of this server refused the upload (HTTP 413).';
    return 'The server answered HTTP ' + r.status + '.';
  }
  function post(url, csrf, body) {
    return fetch(url, {
      method: 'POST', credentials: 'same-origin',
      headers: { 'X-CSRF-Token': csrf }, body: body
    }).then(function (r) {
      return r.json().catch(function () { return null; })
        .then(function (j) { return { ok: r.ok, status: r.status, j: j }; });
    });
  }
  function wait(ms) { return new Promise(function (res) { setTimeout(res, ms); }); }

  window.vpBundleUpload = function (base, file, csrf, progress) {
    progress = progress || function () {};
    return post(base + '/uploads?size=' + file.size, csrf, null).then(function (start) {
      if (!start.ok) throw new Error(reason(start));
      var url = base + '/uploads/' + start.j.id;
      var piece = start.j.chunk_bytes || 8388608;
      var failures = 0;
      function send(offset) {
        if (offset >= file.size) {
          progress(file.size, file.size, 'unpacking');
          return post(url + '/deploy', csrf, null).then(function (r) {
            if (r.ok) return r.j;
            // A gateway that gave up waiting (Cloudflare's 524, nginx's 504)
            // says nothing about whether the unpack finished.
            if (!r.j && r.status >= 502) {
              throw new Error('The server is still unpacking a large site and the connection timed out. ' +
                'Reload in a minute to see whether it deployed.');
            }
            throw new Error(reason(r));
          });
        }
        progress(offset, file.size, 'uploading');
        return post(url + '?offset=' + offset, csrf, file.slice(offset, offset + piece)).then(function (r) {
          if (r.ok) { failures = 0; return send(r.j.offset); }
          if (r.status === 409 && r.j && typeof r.j.offset === 'number') return send(r.j.offset);
          throw new Error(reason(r));
        }, function (e) {
          // A dropped connection: resend this piece; the offset check makes
          // that safe even if the first copy arrived.
          if (++failures > 4) throw e;
          return wait(1000 * failures).then(function () { return send(offset); });
        });
      }
      return send(0);
    });
  };

  window.vpBundleProgressText = function (done, total, phase) {
    if (phase === 'unpacking') return 'Unpacking ' + mb(total) + '…';
    var pct = total ? Math.floor(done * 100 / total) : 0;
    return 'Uploading ' + pct + '% (' + mb(done) + ' of ' + mb(total) + ')…';
  };
})();
