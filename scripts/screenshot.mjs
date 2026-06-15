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
  // networkidle waits for all CSS/fonts/JS to finish loading.
  await page.goto(url, { waitUntil: 'networkidle', timeout: 30000 });
  await page.screenshot({ path: resolve(out), fullPage: false });
  console.log(`  OK  ${out}`);
} finally {
  await browser.close();
}
