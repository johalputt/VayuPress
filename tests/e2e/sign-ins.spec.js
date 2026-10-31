// Shield › Sign-in attempts, as the operator reads it after a stranger
// guessed at the console: the attempt is there, with its account, its door
// and its source, and the source's address is never written whole.
const { test, expect } = require("@playwright/test");

test("a wrong password at the console shows on Sign-in attempts, its address masked", async ({ page, browser }) => {
  const account = `guess-${Date.now()}@example.com`;
  const stranger = await browser.newContext({ storageState: { cookies: [], origins: [] } });
  const guess = await stranger.newPage();
  await guess.goto("/os/login");
  await guess.fill("input[name=email]", account);
  await guess.fill("input[name=password]", "not-the-password");
  await Promise.all([guess.waitForLoadState("load"), guess.click("form.login-form button[type=submit]")]);
  await expect(guess.locator("body")).toContainText("Invalid email or password");
  await stranger.close();

  // Attempts are written every ten seconds; the page reads what is written.
  await expect(async () => {
    await page.goto("/os/shield/sign-ins?days=1");
    await expect(page.locator("main")).toContainText(account, { timeout: 1000 });
  }).toPass({ timeout: 30000 });
  const main = await page.locator("main").innerText();
  expect(main).toContain("The console");
  expect(main).toMatch(/\b\d+\.\d+\.x\.x\b/);
  expect(main).not.toMatch(/\b127\.0\.0\.1\b/);
});
