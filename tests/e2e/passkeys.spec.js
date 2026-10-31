// Passkeys, through the browser's own WebAuthn with Chromium's virtual
// authenticator: one is added on My profile, and then it alone signs the
// account in at /os/login, with no name and no password typed.
const { test, expect } = require("@playwright/test");

test("a passkey added on My profile signs the account in by itself", async ({ page, browser }) => {
  const email = `passkey-${Date.now()}-${Math.random().toString(36).slice(2, 7)}@example.com`;
  const password = "Passkey-Test-Passw0rd!";
  const made = await page.request.post("/api/v1/admin/users", { data: { email, name: "Passkey Owner", password, role: "admin" } });
  expect(made.status(), await made.text()).toBeLessThan(300);

  // The server's own address: through the proxy every request carries the
  // API key, which the console takes ahead of a session, and a passkey is an
  // account's or a mailbox's, never a key's.
  const direct = process.env.UPSTREAM;
  if (!direct) throw new Error("UPSTREAM is not set: the server's own address, which the e2e workflow sets");
  const ctx = await browser.newContext({ baseURL: direct });
  try {
    const me = await ctx.newPage();
    const cdp = await ctx.newCDPSession(me);
    await cdp.send("WebAuthn.enable");
    await cdp.send("WebAuthn.addVirtualAuthenticator", {
      options: { protocol: "ctap2", transport: "internal", hasResidentKey: true, hasUserVerification: true, isUserVerified: true, automaticPresenceSimulation: true },
    });

    await me.goto("/os/login");
    await me.fill("input[name=email]", email);
    await me.fill("input[name=password]", password);
    await Promise.all([me.waitForURL(/\/os\/?$/), me.click("form.login-form button[type=submit]")]);

    await me.goto("/os/profile");
    await me.fill("[data-passkey-name]", "Test laptop");
    await Promise.all([me.waitForEvent("load"), me.click("[data-passkey-add]")]);
    await expect(me.locator("[data-passkeys]")).toContainText("Test laptop");

    await ctx.clearCookies();
    await me.goto("/os/login");
    const button = me.locator("[data-passkey-signin]");
    await expect(button).toBeVisible();
    await Promise.all([me.waitForURL(/\/os\/?$/), button.click()]);
    await me.goto("/os/profile");
    await expect(me.locator("main")).toContainText(email);
    await expect(me.locator("[data-passkeys]")).toContainText("last used");
  } finally {
    await ctx.close();
  }
});
