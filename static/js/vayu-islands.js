/* vayu-islands.js — the VayuOS Alpine.js island registry (ADR-0136).
 *
 * Loaded BEFORE alpine-csp.min.js (both deferred) so these components are
 * registered on the `alpine:init` event before Alpine starts. Every component
 * is a plain factory function referenced BY NAME in x-data="…"; there are no
 * inline expressions, so this works under the eval-free Alpine CSP build and
 * the strict CSP (script-src 'self' 'nonce'; no unsafe-eval). Islands are the
 * exception, not the rule — HTMX remains the backbone. Each island degrades to
 * plain HTML if Alpine fails to load.
 */
(function () {
  'use strict';
  document.addEventListener('alpine:init', function () {
    var Alpine = window.Alpine;
    if (!Alpine) return;

    // filterList — a live, client-only text filter over rows tagged with
    // data-filter-text. Filtering happens in the method body (not in x-show
    // expressions), so the only directives on the page are x-model + @input.
    // If Alpine is absent, the input is inert and every row stays visible.
    // Results are announced to assistive tech via an aria-live [data-filter-
    // status] region (WCAG 4.1.3 status messages), so a screen-reader user
    // hears how many rows matched instead of the table silently emptying.
    Alpine.data('filterList', function () {
      return {
        q: '',
        apply: function () {
          var needle = this.q.trim().toLowerCase();
          var root = this.$root;
          var shown = 0;
          root.querySelectorAll('[data-filter-text]').forEach(function (el) {
            var hay = (el.getAttribute('data-filter-text') || '').toLowerCase();
            var hit = needle === '' || hay.indexOf(needle) !== -1;
            el.hidden = !hit;
            if (hit) shown++;
          });
          var empty = root.querySelector('[data-filter-empty]');
          if (empty) empty.hidden = !(needle !== '' && shown === 0);
          var status = root.querySelector('[data-filter-status]');
          if (status) {
            if (needle === '') {
              status.textContent = '';
            } else {
              var noun = root.getAttribute('data-filter-noun') || 'results';
              status.textContent = shown === 0
                ? ('No ' + noun + ' match your filter.')
                : (shown + ' ' + noun + ' match your filter.');
            }
          }
        }
      };
    });

    // disclosure — a reusable open/close island (details-like, but animatable
    // and controllable). Use: x-data="disclosure" with @click="toggle()" and
    // x-show="open".
    Alpine.data('disclosure', function () {
      return {
        open: false,
        toggle: function () { this.open = !this.open; }
      };
    });

    // copyable — copy the root's data-copy value to the clipboard with a brief
    // "copied" flag. Use: x-data="copyable" @click="copy()" and
    // x-text via a bound label, or x-show="copied" for feedback.
    Alpine.data('copyable', function () {
      return {
        copied: false,
        copy: function () {
          var text = this.$root.getAttribute('data-copy') || '';
          try {
            if (navigator.clipboard) { navigator.clipboard.writeText(text); }
          } catch (e) { /* clipboard denied — no-op */ }
          var self = this;
          this.copied = true;
          setTimeout(function () { self.copied = false; }, 1400);
        }
      };
    });
  });
})();
