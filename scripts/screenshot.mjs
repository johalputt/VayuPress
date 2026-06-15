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

import puppeteer from 'puppeteer-core';
import { execSync } from 'child_process';
import { resolve } from 'path';

const [,, url, out, w, h] = process.argv;
if (!url || !out) {
  console.error('Usage: screenshot.mjs <url> <output.png> [width] [height]');
  process.exit(1);
}

const width  = parseInt(w  || '1440', 10);
const height = parseInt(h  || '1024', 10);

function findChromium() {
  if (process.env.CHROMIUM_PATH) return process.env.CHROMIUM_PATH;
  const candidates = [
    'chromium-browser', 'chromium', 'google-chrome-stable', 'google-chrome', 'chrome',
  ];
  for (const c of candidates) {
    try { return execSync(`which ${c}`, { stdio: ['pipe','pipe','pipe'] }).toString().trim(); }
    catch { /* try next */ }
  }
  throw new Error('No Chromium/Chrome binary found. Set CHROMIUM_PATH or install chromium.');
}

const executablePath = findChromium();

const browser = await puppeteer.launch({
  executablePath,
  headless: true,
  args: [
    '--no-sandbox',
    '--disable-setuid-sandbox',
    '--disable-dev-shm-usage',
    '--disable-gpu',
    '--hide-scrollbars',
  ],
});

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
