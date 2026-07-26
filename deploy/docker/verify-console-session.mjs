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
// 02-after-reload.png. Writes cookie/header evidence to stdout AND
// OUT_DIR/cookie-evidence.txt.
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

async function main() {
  const browser = await chromium.launch();
  const context = await browser.newContext();
  const page = await context.newPage();

  // Every request/response's Cookie and Set-Cookie headers, in order. This is
  // the actual corroborating evidence a screenshot cannot show: proof that
  // the browser sent the session cookie back and the server's response
  // headers round-tripped it, on plain Node -- not just that the page looked
  // authenticated.
  page.on("request", (req) => {
    const cookie = req.headers()["cookie"];
    if (cookie && cookie.includes("sb-")) {
      record(`>> ${req.method()} ${req.url()}\n   Cookie: ${cookie}`);
    }
  });
  page.on("response", async (resp) => {
    const headers = resp.headers();
    if (headers["set-cookie"]) {
      record(`<< ${resp.status()} ${resp.url()}\n   Set-Cookie: ${headers["set-cookie"]}`);
    }
  });

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

    const cookies = await context.cookies();
    const authCookie = cookies.find((c) => c.name.startsWith("sb-"));
    record(`auth_cookie_name: ${authCookie?.name}`);
    record(`auth_cookie_value_length: ${authCookie?.value?.length ?? 0}`);

    // Proof #1: authenticated dashboard, not the sign-in form.
    await page.screenshot({ path: `${OUT_DIR}/01-authenticated.png`, fullPage: true });

    // The actual regression test: middleware.ts re-validates and can rotate
    // the session cookie on every request. A reload is the step that
    // silently broke under Cloudflare Pages Functions' Set-Cookie drop.
    await page.reload({ waitUntil: "networkidle" });
    await page.waitForTimeout(500);
    const urlAfterReload = page.url();
    survivedReload = /\/console(\/.*)?$/.test(urlAfterReload);
    record(`url_after_reload: ${urlAfterReload}`);

    // Proof #2: still authenticated after reload, not bounced to /auth/sign-in.
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
