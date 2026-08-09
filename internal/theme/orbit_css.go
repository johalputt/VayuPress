// SPDX-License-Identifier: Apache-2.0

package theme

// orbit_css.go — the component CSS for the Orbit preset (presets.go).
//
// Held to the same three rules as Vayu and Editor, and they are the reason this
// theme can be fast rather than a claim that it is:
//
//  1. NO EXTERNAL REQUEST. No web font, no image, no icon set. Every ornament
//     on this page is a gradient. A theme that fetches one font file has
//     already given up the render-blocking round trip that decides LCP.
//  2. NO JAVASCRIPT. The blog's own scripts stay as they are; this adds none.
//  3. NOTHING MOVES ONCE PAINTED. Every animation below is transform or opacity
//     only, and every one is disabled under prefers-reduced-motion. No
//     animation touches width, height, top, left, margin or font-size, so there
//     is no layout shift to score against — CLS is a measurement of exactly the
//     mistake this rule forbids.
//
// The look is carried over from vayuweb.vayupress.com: concentric rings, mono
// labels tracked wide, glass with a masked gradient hairline, and display type
// allowed to be genuinely large.
const orbitCSS = `
/* ── Orbit ─────────────────────────────────────────────────────────────── */

.vayu-hero {
  position: relative;
  isolation: isolate;
  overflow: hidden;
  border-bottom: none;
  padding-block: 5rem 4rem;
  /* Reserved up-front so the rings, which are painted into ::before, can never
     change the height of anything. */
  min-height: 22rem;
}

/* The signature: concentric orbits, drawn as repeating radial stops so the
   whole field is one gradient rather than a stack of elements. */
.vayu-hero::before {
  content: "";
  position: absolute;
  inset: -40% -10%;
  z-index: -2;
  pointer-events: none;
  background:
    repeating-radial-gradient(circle at 50% 42%,
      color-mix(in oklab, var(--pico-primary) 22%, transparent) 0 1px,
      transparent 1px 78px);
  -webkit-mask-image: radial-gradient(circle at 50% 42%, #000 0%, transparent 68%);
          mask-image: radial-gradient(circle at 50% 42%, #000 0%, transparent 68%);
  opacity: .5;
  animation: vayuOrbitDrift 64s linear infinite;
}

/* A slow warm core behind the rings, so the field sits in light. */
.vayu-hero::after {
  content: "";
  position: absolute;
  inset: 0;
  z-index: -1;
  pointer-events: none;
  background:
    radial-gradient(ellipse 60% 70% at 50% 30%,
      color-mix(in oklab, var(--pico-primary) 16%, transparent), transparent 70%),
    radial-gradient(ellipse 50% 60% at 78% 78%,
      color-mix(in oklab, var(--vayu-accent2, var(--pico-primary)) 12%, transparent), transparent 72%);
}

@keyframes vayuOrbitDrift { to { transform: rotate(360deg); } }

.vayu-hero h1 {
  font-size: clamp(2.4rem, 6.4vw, 4.6rem);
  line-height: 1.02;
  letter-spacing: -0.025em;
  font-weight: 600;
  max-width: 18ch;
}

.vayu-hero-tagline {
  font-size: clamp(1.02rem, 1.6vw, 1.2rem);
  max-width: 52ch;
  opacity: .88;
}

/* ── Section labels ───────────────────────────────────────────────────────── */

.vayu-section-label {
  font-family: var(--vayu-font-mono, ui-monospace, monospace);
  font-size: .7rem;
  letter-spacing: .18em;
  text-transform: uppercase;
  opacity: .58;
  display: flex;
  align-items: center;
  gap: .75rem;
}

.vayu-section-label::after {
  content: "";
  flex: 1;
  height: 1px;
  background: linear-gradient(90deg, var(--pico-muted-border-color), transparent);
}

/* ── Cards ─────────────────────────────────────────────────────────────────
   The lift is transform-only, so hovering a card cannot reflow the grid. */

.vayu-post-card {
  position: relative;
  isolation: isolate;
  border-radius: var(--vayu-radius-lg, 1rem);
  background: color-mix(in oklab, var(--pico-card-background-color) 72%, transparent);
  backdrop-filter: blur(16px);
  box-shadow: var(--sh-sm, 0 1px 2px rgba(0,0,0,.18));
  transition: transform var(--t, .28s cubic-bezier(.22,1,.36,1)),
              box-shadow var(--t, .28s ease),
              background-color var(--t, .28s ease);
}

/* A hairline that is bright where light would fall and fades to nothing
   elsewhere. A flat 1px border reads dead against a near-black canvas. */
.vayu-post-card::before {
  content: "";
  position: absolute;
  inset: 0;
  z-index: -1;
  border-radius: inherit;
  padding: 1px;
  background: linear-gradient(140deg,
    color-mix(in oklab, var(--pico-primary) 44%, transparent),
    transparent 42%, transparent 60%,
    color-mix(in oklab, var(--vayu-accent2, var(--pico-primary)) 30%, transparent));
  -webkit-mask: linear-gradient(#000 0 0) content-box, linear-gradient(#000 0 0);
  -webkit-mask-composite: xor;
          mask: linear-gradient(#000 0 0) content-box, linear-gradient(#000 0 0);
          mask-composite: exclude;
  opacity: .55;
  transition: opacity var(--t, .28s ease);
}

.vayu-post-card:hover {
  transform: translateY(-3px);
  box-shadow: var(--sh-lg, 0 18px 40px rgba(0,0,0,.4));
}
.vayu-post-card:hover::before { opacity: 1; }

.vayu-post-title {
  letter-spacing: -0.015em;
  line-height: 1.2;
}

.vayu-post-meta {
  font-family: var(--vayu-font-mono, ui-monospace, monospace);
  font-size: .72rem;
  letter-spacing: .1em;
  text-transform: uppercase;
  opacity: .62;
}

/* Reserving the thumbnail's box is what keeps a late-arriving image from
   shoving the headline down the page. */
.vayu-post-thumb { aspect-ratio: 16 / 9; overflow: hidden; border-radius: inherit; }
.vayu-post-thumb img { width: 100%; height: 100%; object-fit: cover; display: block; }

/* ── Reading ───────────────────────────────────────────────────────────────── */

.vayu-article-header h1 {
  font-size: clamp(2rem, 5vw, 3.2rem);
  line-height: 1.05;
  letter-spacing: -0.022em;
}

.vayu-prose :is(h2, h3) { letter-spacing: -0.015em; }

.vayu-prose blockquote {
  border-left: 2px solid color-mix(in oklab, var(--pico-primary) 60%, transparent);
  padding-left: 1.15rem;
  font-style: normal;
  opacity: .92;
}

/* Media cards put the image beside the copy from the wide breakpoint up. The
   aspect-ratio is what keeps the row height fixed before the image arrives. */
.vayu-post-card--media .vayu-post-thumb { aspect-ratio: 16 / 9; }

@media (min-width: 48rem) {
  .vayu-post-card--media {
    display: grid;
    grid-template-columns: minmax(0, 15rem) minmax(0, 1fr);
    gap: 1.25rem;
    align-items: stretch;
  }
  .vayu-post-card--media .vayu-post-thumb { height: 100%; aspect-ratio: auto; }
}

.vayu-author-box {
  border-radius: var(--vayu-radius-lg, 1rem);
  background: color-mix(in oklab, var(--pico-card-background-color) 72%, transparent);
  border: 1px solid var(--pico-muted-border-color);
  box-shadow: var(--sh-sm, 0 1px 2px rgba(0,0,0,.18));
  padding: 1.25rem 1.4rem;
}

.vayu-footer { border-top: 1px solid var(--pico-muted-border-color); }

.vayu-footer-col-links a {
  display: inline-block;
  padding-block: .18rem;
  opacity: .78;
  transition: opacity var(--t, .25s ease), transform var(--t, .25s ease);
}

.vayu-footer-col-links a:hover { opacity: 1; transform: translateX(2px); }

@media (prefers-reduced-motion: reduce) {
  .vayu-hero::before { animation: none; }
  .vayu-post-card,
  .vayu-post-card::before,
  .vayu-hero-search button,
  .vayu-hero-search .vayu-search-input { transition: none; }
  .vayu-post-card:hover { transform: none; }
  .vayu-footer-col-links a { transition: none; }
  .vayu-footer-col-links a:hover { transform: none; }
}
`
