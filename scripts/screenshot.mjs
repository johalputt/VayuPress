#!/usr/bin/env node
// Takes a screenshot of a URL after waiting for network idle (all CSS/JS/fonts
// loaded). Uses puppeteer-core so it works with the system Chromium installed
// by the CI workflow — no bundled browser download.
//
// Usage:
//   node scripts/screenshot.mjs <url> <output.png> [width] [height]
//   CHROMIUM_PATH=/usr/bin/chromium node scripts/screenshot.mjs ...
//
// Exits 0 on success, 1 on error (non-fatal — capture-screenshots.sh warns
// and continues so one bad page doesn't kill the whole run).

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

const browser = await chromium.launch();
try {
  const page = await browser.newPage();
  await page.setViewportSize({ width, height });
  // waitUntil networkidle waits for all CSS/fonts/JS to finish loading.
  await page.goto(url, { waitUntil: 'networkidle', timeout: 30000 });
  await page.screenshot({ path: resolve(out), fullPage: false });
  console.log(`  OK  ${out}`);
} finally {
  await browser.close();
}


try {
  const page = await browser.newPage();
  await page.setViewport({ width, height });

  // waitUntil: networkidle0 means 0 in-flight network requests for 500 ms —
  // guarantees CSS, fonts, and JS have all finished loading before the shot.
  await page.goto(url, { waitUntil: 'networkidle0', timeout: 30000 });

  await page.screenshot({ path: resolve(out), fullPage: false });
  console.log(`  OK  ${out}`);
} finally {
  await browser.close();
}
