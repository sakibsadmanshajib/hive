import { mkdirSync, writeFileSync } from "node:fs";
import { createRequire } from "node:module";

// Screenshot harness for issue #1745, the invitation send cap.
//
// It drives the real console members page on a production build (next build,
// next start) with proof-stub.mjs standing in for Supabase Auth and the control
// plane. Everything above the HTTP boundary is product code: the route handler
// this change touches, the invite panel, the page.
//
// Every claim is asserted rather than merely recorded. A run that would print a
// false line exits non-zero and takes no screenshot, so a green run is the
// evidence and the log is the record of it.
//
// Environment only, all required, so two stacks can run at once:
//   PROOF_APP_URL   origin of the console under test
//   PROOF_STUB_URL  origin of proof-stub.mjs (as the browser reaches it)
//   PROOF_ANON_KEY  the NEXT_PUBLIC_SUPABASE_ANON_KEY the console was built with
//   PROOF_OUT_DIR   directory for screenshots and the capture log
//   PROOF_APP_DIR   path to an apps/web-console whose node_modules carries
//                   Playwright and @supabase/ssr
//   PROOF_MODE      "after" (three states on this branch) or "before"
//                   (the same cap refusal against unmodified main)

function required(name) {
  const value = process.env[name];
  if (!value) {
    throw new Error(`${name} is required; set it before running this harness`);
  }
  return value;
}

const APP = required("PROOF_APP_URL");
const SUPABASE_URL = required("PROOF_STUB_URL");
const ANON = required("PROOF_ANON_KEY");
const OUT = required("PROOF_OUT_DIR");
const MODE = required("PROOF_MODE");
if (MODE !== "before" && MODE !== "after") {
  throw new Error(`PROOF_MODE must be "before" or "after", got ${MODE}`);
}

const nodeRequire = createRequire(`${required("PROOF_APP_DIR")}/package.json`);
const { chromium } = nodeRequire("@playwright/test");
const { createBrowserClient } = nodeRequire("@supabase/ssr");

const enc = (obj) => Buffer.from(JSON.stringify(obj)).toString("base64url");
const now = Math.floor(Date.now() / 1000);
const ACCESS_TOKEN = [
  enc({ alg: "HS256", typ: "JWT" }),
  enc({
    sub: "11111111-1111-1111-1111-111111111111",
    email: "proof@example.com",
    role: "authenticated",
    aud: "authenticated",
    tenant_id: "22222222-2222-2222-2222-222222222222",
    iat: now,
    exp: now + 86400,
  }),
  "proof-signature",
].join(".");

async function sessionCookies() {
  const jar = new Map();
  const client = createBrowserClient(SUPABASE_URL, ANON, {
    isSingleton: false,
    cookies: {
      getAll: () =>
        [...jar.entries()].map(([name, entry]) => ({ name, value: entry.value })),
      setAll: (cookies) => {
        for (const cookie of cookies) {
          jar.set(cookie.name, { value: cookie.value, options: cookie.options ?? {} });
        }
      },
    },
  });
  const { error } = await client.auth.setSession({
    access_token: ACCESS_TOKEN,
    refresh_token: "proof-refresh-token",
  });
  if (error) {
    throw new Error(`setSession failed: ${error.message}`);
  }
  if (jar.size === 0) {
    throw new Error("no session cookies persisted, the browser would be signed out");
  }
  const host = new URL(APP).hostname;
  return [...jar.entries()].map(([name, entry]) => ({
    name,
    value: entry.value,
    domain: host,
    path: "/",
    expires: now + 86400,
    httpOnly: false,
    secure: new URL(APP).protocol === "https:",
    sameSite: "Lax",
  }));
}

const log = [];
function note(line) {
  log.push(line);
  console.log(line);
}

function claim(label, actual, expected) {
  note(`  ${label}: ${JSON.stringify(actual)}`);
  if (actual !== expected) {
    throw new Error(`claim failed: ${label} was ${actual}, expected ${expected}`);
  }
}

const browser = await chromium.launch();
const context = await browser.newContext({
  viewport: { width: 1440, height: 900 },
  deviceScaleFactor: 2,
});
await context.addCookies(await sessionCookies());
const page = await context.newPage();
mkdirSync(OUT, { recursive: true });

// The POST the console makes to its own route handler, which is the status this
// capture is about. Recorded per submission so the log carries the wire result
// beside the rendered one.
let lastInviteStatus = 0;
page.on("response", (response) => {
  if (response.url().endsWith("/api/console/members") && response.request().method() === "POST") {
    lastInviteStatus = response.status();
  }
});

async function invite(address) {
  lastInviteStatus = 0;
  await page.fill("#invite-email", address);
  await page.click('button[type="submit"]:has-text("Create invitation")');
  await page.waitForFunction(
    () =>
      document.querySelector('[role="alert"]') !== null ||
      document.querySelector('[role="status"]') !== null,
    undefined,
    { timeout: 15000 },
  );
  await page.waitForTimeout(300);
}

async function shot(name) {
  await page.screenshot({ path: `${OUT}/${name}.png`, fullPage: false });
  note(`  screenshot: ${name}.png`);
}

// The page carries an empty role="alert" region of its own, so an alert with no
// text is not a failure message. Only a non-empty one is.
async function alertText() {
  const texts = (await page.locator('[role="alert"]').allTextContents())
    .map((text) => text.trim())
    .filter((text) => text !== "");
  return texts.length === 0 ? null : texts[0];
}

// Start the stub's status sequence from the top, so a rerun captures the same
// three states rather than continuing where the previous run left off.
const reset = await fetch(`${SUPABASE_URL}/proof/reset`, { method: "POST" });
if (!reset.ok) {
  throw new Error(`proof stub reset failed: HTTP ${reset.status}`);
}

note(`Capture run: issue #1745 invitation send cap (${MODE})`);
note(`Console under test: ${APP} (next build + next start, ${MODE === "before" ? "unmodified origin/main" : "this branch"})`);
note(`Upstream: local proof stub on ${SUPABASE_URL} for Supabase Auth and the`);
note("control plane. No live tenant, no live credential, and no email is sent:");
note("the stub answers the invitation POST 201, then 429, then 503, with the");
note("two refusal bodies the control-plane handler produces.");
note("");

const response = await page.goto(`${APP}/console/members`, { waitUntil: "networkidle" });
claim("GET /console/members status", response ? response.status() : 0, 200);
claim("invite form present", await page.locator("#invite-email").count(), 1);
note("");

if (MODE === "after") {
  note("1. Control: an invitation inside the cap is created and sent");
  await invite("teammate@example.com");
  claim("POST /api/console/members status", lastInviteStatus, 201);
  claim("failure alert rendered", await alertText(), null);
  const status = (await page.locator('[role="status"]').first().textContent()).trim();
  note(`  outcome notice: ${JSON.stringify(status)}`);
  claim("outcome names the address", status.includes("teammate@example.com"), true);
  claim("outcome claims a delivered email", /we emailed an invitation/i.test(status), true);
  await shot("01-invitation-sent");
  note("");

  note("2. The cap refuses, in the same words whichever dimension tripped");
  await invite("second@example.com");
  claim("POST /api/console/members status", lastInviteStatus, 429);
  claim(
    "alert text",
    await alertText(),
    "invitation limit reached, try again later",
  );
  claim("success notice cleared", await page.locator('[role="status"]').count(), 0);
  await shot("02-cap-refusal");
  note("");

  note("3. The counter is unreachable, which is not the caller's quota");
  await invite("third@example.com");
  claim("POST /api/console/members status", lastInviteStatus, 503);
  claim(
    "alert text",
    await alertText(),
    "Invitations are temporarily unavailable. Please try again shortly.",
  );
  await shot("03-counter-unavailable");
} else {
  note("1. Unmodified main, same cap refusal from the same stub");
  // main's route maps every failure to 400 and a generic message, so the first
  // POST is spent on the 201 the sequence starts with, exactly as above.
  await invite("teammate@example.com");
  claim("POST /api/console/members status", lastInviteStatus, 201);
  await invite("second@example.com");
  claim("POST /api/console/members status (upstream answered 429)", lastInviteStatus, 400);
  claim(
    "alert text",
    await alertText(),
    "Could not create the invitation. Please try again.",
  );
  await shot("00-before-generic-retry-advice");
}

writeFileSync(`${OUT}/capture-${MODE}.log`, log.join("\n") + "\n");
await browser.close();
