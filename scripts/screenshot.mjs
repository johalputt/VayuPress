#!/usr/bin/env node
// Takes a styled screenshot using Playwright Chromium.
// Playwright installs its own browser and handles CI sandbox correctly.
//
// Usage:
//   node scripts/screenshot.mjs <url> <output.png> [width] [height]

import { chromium } from 'playwright';
import { resolve } from 'path';

const [,, url, out, w, h] = process.argv;
if (!url || !out) {
  console.error('Usage: screenshot.mjs <url> <output.png> [width] [height]');
  process.exit(1);
}

const width  = parseInt(w  || '1440', 10);
const height = parseInt(h  || '1024', 10);

// Use a preinstalled Chromium when one is provided. CI runs `npx playwright
// install` and needs none of this, but sandboxes and air-gapped machines often
// ship a browser already and cannot download Playwright's pinned revision.
const executablePath = process.env.PLAYWRIGHT_CHROMIUM_PATH || undefined;
const browser = await chromium.launch(executablePath ? { executablePath } : {});
try {
  const page = await browser.newPage();
  await page.setViewportSize({ width, height });
  // networkidle waits for all CSS/fonts/JS to finish loading.
  const response = await page.goto(url, { waitUntil: 'networkidle', timeout: 30000 });

  // Refuse to photograph an error. Headless Chrome announces itself in its user
  // agent, so VayuShield classifies it as automation and can answer with a block
  // page — which is a valid 403 that screenshots perfectly happily. Without this
  // check a capture run silently replaces the README gallery with "Access denied",
  // and nobody notices until someone looks at the repository. Run the capture
  // against an instance started with VAYUSHIELD=off.
  const status = response ? response.status() : 0;
  if (status >= 400) {
    throw new Error(`${url} returned HTTP ${status} — refusing to screenshot an error page`);
  }
  // Match on the document TITLE, not body text: the Bot Shield and VayuMCP admin
  // pages legitimately quote these phrases while describing what a challenge looks
  // like, and a body-text check refuses to photograph them.
  const title = await page.title();
  if (['Verifying your browser', 'Access denied'].some(t => title.startsWith(t))) {
    throw new Error(`${url} rendered a bot-protection page ("${title}") — start the instance with VAYUSHIELD=off`);
  }

  await page.screenshot({ path: resolve(out), fullPage: false });
  console.log(`  OK  ${out}`);
} finally {
  await browser.close();
}
