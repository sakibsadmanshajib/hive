// Captures /console/api-keys/{id}/limits against the real prod-container
// pair (hive-web-console-prod-1 behind hive-caddy-console-1), the substrate
// gap the README's "Container-substrate verification" section flagged as
// open. Points that container pair at the same class of local stand-in
// (fake-auth-server.js, fake-control-plane-server.js) the earlier next-dev
// capture (limits-after.png) used, instead of live Supabase Cloud or the
// shared session-mode DB pooler; neither is touched by this script or the
// containers it drives.
//
// Run with: node capture-prod.js <base-url>
// <base-url> is the caddy-console origin, e.g. http://localhost:3005.
// Requires `playwright` resolvable on NODE_PATH (see README "Reproducing").

const { chromium } = require("playwright");

const BASE_URL = process.argv[2] || "http://localhost:3005";
const KEY_ID = "a1b2c3d4-e5f6-4a7b-8c9d-0e1f2a3b4c5d";
const EMAIL = process.env.FAKE_AUTH_EMAIL || "proof552@example.invalid";
const PASSWORD = "not-checked-by-the-stand-in";

function log(line) {
  console.log(line);
}

async function main() {
  const browser = await chromium.launch(
    process.env.PLAYWRIGHT_CHROMIUM_PATH
      ? { executablePath: process.env.PLAYWRIGHT_CHROMIUM_PATH }
      : {},
  );
  const page = await browser.newPage();
  page.on("console", (msg) => log(`[browser console:${msg.type()}] ${msg.text()}`));
  page.on("pageerror", (err) => log(`[browser pageerror] ${err.message}`));

  await page.goto(`${BASE_URL}/auth/sign-in`, { waitUntil: "networkidle" });
  await page.getByLabel("Email").fill(EMAIL);
  await page.getByLabel("Password").fill(PASSWORD);
  await page.getByRole("button", { name: /continue/i }).click();
  try {
    await page.waitForURL(`${BASE_URL}/console`, { timeout: 15000 });
  } catch (err) {
    log(`[diagnostic] waitForURL failed, current url: ${page.url()}`);
    log(`[diagnostic] body text: ${(await page.locator("body").innerText()).slice(0, 1000)}`);
    throw err;
  }
  log(`signed in, landed on ${page.url()}`);

  const limitsUrl = `${BASE_URL}/console/api-keys/${KEY_ID}/limits`;
  const response = await page.goto(limitsUrl, { waitUntil: "networkidle" });
  log(`GET /console/api-keys/${KEY_ID}/limits -> HTTP ${response.status()}`);

  const form = page.locator('[data-testid="rate-limit-form"]');
  const formPresent = (await form.count()) > 0;
  log(`rate-limit-form present: ${formPresent}`);

  if (formPresent) {
    const rpmInput = page.getByLabel(/requests per minute/i);
    const tpmInput = page.getByLabel(/tokens per minute/i);
    log(`rpm input value: ${await rpmInput.inputValue()}`);
    log(`tpm input value: ${await tpmInput.inputValue()}`);

    await page.locator('[data-testid="rl-submit"]').click();
    const savedText = page.getByText("Saved.");
    const errorText = page.getByText(/could not save/i);
    let saved = false;
    let sawError = false;
    try {
      await savedText.waitFor({ timeout: 5000 });
      saved = true;
    } catch {
      sawError = (await errorText.count()) > 0;
    }
    log(`save result: saved=${saved} error=${sawError}`);
  }

  await page.screenshot({ path: process.argv[3] || "limits-after-prod-container.png", fullPage: true });

  log("--- rendered body text (truncated) ---");
  const bodyText = await page.locator("body").innerText();
  log(bodyText.slice(0, 2000));

  await browser.close();
}

main().catch((err) => {
  console.error(err);
  process.exitCode = 1;
});
