// Verifies the one regression that matters for the web-console self-hosted
// migration (issue: does a plain-Node `next start` server survive the
// Cloudflare Pages Functions Set-Cookie-under-Workerd bug class, unlike the
// old Workers deploy). Signs in against a live web-console-prod container
// with a real account, screenshots the authenticated console, reloads, and
// screenshots again to prove the session survived -- middleware.ts rewrites
// the session cookie on every request, which is exactly the step Pages
// Functions used to drop.
//
// Not part of the Playwright e2e suite (apps/web-console/e2e): this targets
// an in-compose-network hostname (web-console-prod:3000) and a throwaway
// account provisioned out-of-band, not the public deployed app.
//
// Usage (from a container attached to the compose network, e.g. run inside
// mcr.microsoft.com/playwright:v1.51.1-jammy with `npm install playwright`):
//   CONSOLE_EMAIL=... CONSOLE_PASSWORD=... CONSOLE_TARGET=http://web-console-prod:3000 \
//     node verify-console-session.mjs
//
// Provision the throwaway account first with a uniquely-slugged copy of
// scripts/seed-demo-owner.py (see that script's TENANT_SLUG/USER_EMAIL/
// ACCOUNT_SLUG constants) and delete it again afterward -- this script does
// not manage account lifecycle.
//
// Writes screenshots to OUT_DIR (default ./out): 01-authenticated.png,
// 02-after-reload.png. Writes cookie evidence to stdout AND
// OUT_DIR/cookie-evidence.txt.
//
// Cookie evidence comes from the browser's own cookie jar
// (BrowserContext.cookies()), not from sniffing request/response headers:
// Chromium redacts the real Cookie header from Playwright's basic Network
// events (documented Playwright/Chromium behaviour), so page.on("request")
// never sees it even though the browser sends it. The cookie-jar API is the
// reliable, authoritative source and additionally exposes the security flags
// (httpOnly/secure/sameSite) a raw header line would not.
import { chromium } from "playwright";
import { mkdirSync, writeFileSync } from "node:fs";

const EMAIL = process.env.CONSOLE_EMAIL;
const PASSWORD = process.env.CONSOLE_PASSWORD;
const TARGET = process.env.CONSOLE_TARGET || "http://web-console-prod:3000";
const OUT_DIR = process.env.OUT_DIR || "./out";

if (!EMAIL || !PASSWORD) {
  console.error("RESULT: FAIL missing CONSOLE_EMAIL/CONSOLE_PASSWORD");
  process.exit(1);
}

mkdirSync(OUT_DIR, { recursive: true });
const evidenceLines = [];
function record(line) {
  console.log(line);
  evidenceLines.push(line);
}

function fingerprint(cookie) {
  if (!cookie) return null;
  const v = cookie.value;
  return {
    name: cookie.name,
    domain: cookie.domain,
    path: cookie.path,
    httpOnly: cookie.httpOnly,
    secure: cookie.secure,
    sameSite: cookie.sameSite,
    expires: cookie.expires,
    value_length: v.length,
    value_prefix: v.slice(0, 16),
    value_suffix: v.slice(-16),
  };
}

async function main() {
  const browser = await chromium.launch();
  const context = await browser.newContext();
  const page = await context.newPage();

  let signInOk = false;
  let survivedReload = false;

  try {
    await page.goto(`${TARGET}/auth/sign-in`, { waitUntil: "networkidle" });
    await page.fill("#email", EMAIL);
    await page.fill("#password", PASSWORD);
    await Promise.all([
      page.waitForURL(/\/console(\/.*)?$/, { timeout: 15000 }).catch(() => {}),
      page.click('button[type="submit"]'),
    ]);
    await page.waitForTimeout(1000);

    const urlAfterSignIn = page.url();
    signInOk = /\/console(\/.*)?$/.test(urlAfterSignIn);
    record(`url_after_sign_in: ${urlAfterSignIn}`);
    if (!signInOk) {
      console.error("RESULT: FAIL sign-in did not reach /console");
      await page.screenshot({ path: `${OUT_DIR}/01-authenticated.png` });
      process.exit(1);
    }

    const cookiesAfterSignIn = await context.cookies();
    const authCookieAfterSignIn = cookiesAfterSignIn.find((c) => c.name.startsWith("sb-"));
    record(`cookie_after_sign_in: ${JSON.stringify(fingerprint(authCookieAfterSignIn), null, 2)}`);

    // Proof #1: authenticated dashboard, not the sign-in form. Timestamped
    // (not just the pixels) so two genuinely separate capture calls are
    // provable even if the dashboard itself renders pixel-identical both
    // times (it is static: zero counters, no per-request content) --
    // otherwise identical PNG bytes before/after reload are indistinguishable
    // from a copy-paste bug from the outside.
    record(`screenshot_01_taken_at: ${new Date().toISOString()}`);
    await page.screenshot({ path: `${OUT_DIR}/01-authenticated.png`, fullPage: true });

    // The actual regression test: middleware.ts re-validates the cookie (and
    // can rotate it) on every request. A reload is the step that silently
    // broke under Cloudflare Pages Functions' Set-Cookie drop.
    await page.reload({ waitUntil: "networkidle" });
    await page.waitForTimeout(500);
    const urlAfterReload = page.url();
    survivedReload = /\/console(\/.*)?$/.test(urlAfterReload);
    record(`url_after_reload: ${urlAfterReload}`);

    const cookiesAfterReload = await context.cookies();
    const authCookieAfterReload = cookiesAfterReload.find((c) => c.name.startsWith("sb-"));
    record(`cookie_after_reload: ${JSON.stringify(fingerprint(authCookieAfterReload), null, 2)}`);
    record(`cookie_survived_reload: ${Boolean(authCookieAfterReload)}`);

    // Proof #2: still authenticated after reload, not bounced to /auth/sign-in.
    record(`screenshot_02_taken_at: ${new Date().toISOString()}`);
    await page.screenshot({ path: `${OUT_DIR}/02-after-reload.png`, fullPage: true });

    writeFileSync(`${OUT_DIR}/cookie-evidence.txt`, evidenceLines.join("\n\n") + "\n");

    record(`RESULT: ${signInOk && survivedReload ? "PASS" : "FAIL"} sign_in_ok=${signInOk} survived_reload=${survivedReload}`);
    process.exit(signInOk && survivedReload ? 0 : 1);
  } finally {
    await browser.close();
  }
}

main().catch((err) => {
  console.error("RESULT: FAIL unhandled", err.message);
  process.exit(1);
});
