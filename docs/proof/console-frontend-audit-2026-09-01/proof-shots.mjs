import { mkdirSync, writeFileSync } from "node:fs";
import { createRequire } from "node:module";

// Screenshot harness for the console frontend audit fixes (issues 1652, 1647,
// 1649, 1653). Runs against a console started from a production build of the
// branch, with proof-stub.mjs standing in for Supabase Auth and the control
// plane.
//
// Every claim below is asserted, not merely recorded: a capture run that would
// print a false line exits non-zero and takes no screenshot, so a green run is
// the evidence rather than the log text being the evidence.
//
// Configuration is environment only, and every variable is required at startup
// so this runs in any worktree:
//   PROOF_APP_URL       origin of the console under test
//   PROOF_STUB_URL      origin of proof-stub.mjs
//   PROOF_ANON_KEY      the NEXT_PUBLIC_SUPABASE_ANON_KEY the console was
//                       built with (any value the stub accepts)
//   PROOF_OUT_DIR       directory to write the screenshots and capture log to
//   PROOF_APP_DIR       path to apps/web-console, whose node_modules carries
//                       Playwright and @supabase/ssr

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

// Playwright and @supabase/ssr are dependencies of apps/web-console, not of
// this directory, and ESM resolves a bare specifier from the file location
// rather than the working directory. PROOF_APP_DIR points at the console
// package so this file can live beside its capture log and still run.
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
        [...jar.entries()].map(([name, entry]) => ({
          name,
          value: entry.value,
        })),
      setAll: (cookies) => {
        for (const cookie of cookies) {
          jar.set(cookie.name, {
            value: cookie.value,
            options: cookie.options ?? {},
          });
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

// A claim that is checked. The observed value goes in the log either way, and a
// false claim ends the run before any screenshot is taken.
function claim(label, actual, expected) {
  note(`  ${label}: ${actual}`);
  if (actual !== expected) {
    throw new Error(
      `claim failed: ${label} was ${actual}, expected ${expected}`,
    );
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

async function shot(name, path, describe) {
  const response = await page.goto(`${APP}${path}`, {
    waitUntil: "networkidle",
  });
  await describe(response ? response.status() : 0);
  await page.screenshot({ path: `${OUT}/${name}.png`, fullPage: false });
  note(`  screenshot: ${name}.png`);
}

note("Capture run: console frontend audit fixes (issues 1652, 1647, 1649, 1653)");
note(`Console under test: ${APP} (next start, production build of this branch)`);
note(`Upstreams: local proof stub on ${SUPABASE_URL} for Supabase Auth and the`);
note("control plane. No live tenant data and no live credential is involved;");
note("the session bearer is an unsigned local token the stub accepts.");
note("");

note("1. Issue 1652: notFound on a data miss renders inside the console shell");
await shot("01-not-found-in-shell", "/console/catalog/does-not-exist-model", async (status) => {
  claim("GET /console/catalog/does-not-exist-model status", status, 200);
  const html = await page.content();
  claim("server HTML carries the not-found heading", /Model not found/.test(html), true);
  claim("bare Next error shell present", /__next_error__/.test(html), false);
});
note("");

note("2. Issue 1647: Try in chat is gated on the chat capability badge");
await shot("02-catalog-gated-links", "/console/catalog", async (status) => {
  claim("GET /console/catalog status", status, 200);
  note("  fixture rows: 5 (2 chat, 1 embeddings, 1 stt, 1 tts)");
  const links = await page.getByRole("link", { name: /try in chat/i }).count();
  claim("try-in-chat links rendered", links, 2);
  const body = await page.textContent("body");
  claim("the word Hidden reaches the customer", /\\bHidden\\b/.test(body), false);
  claim("hive-fast status badge reads Deprecated", /Deprecated/.test(body), true);
});
note("");

note("3. Issue 1649: unknown invoice id answers 404, not 500");
await shot("03-invoice-404", "/api/invoices/00000000-0000-0000-0000-000000000001/pdf", async (status) => {
  claim("GET /api/invoices/00000000-0000-0000-0000-000000000001/pdf status", status, 404);
  const body = (await page.textContent("body")).trim();
  claim("response body", body, JSON.stringify({ error: "Invoice not found" }));
});
note("");

note("4. Issue 1653: a stored bidi override renders inert");
await shot("04-logs-bidi-inert", "/console/logs", async (status) => {
  claim("GET /console/logs status", status, 200);
  await page.selectOption("#logs-key-filter", "key-1");
  const rendered = await page
    .locator("#logs-key-filter option[value=key-1]")
    .textContent();
  note("  stored nickname (escaped): prod\\u202Egnp.txt");
  claim("rendered option text", rendered, "prodgnp.txt");
  claim("rendered text still carries U+202E", rendered.includes("\u202E"), false);
});

writeFileSync(`${OUT}/capture.log`, log.join("\n") + "\n");
await browser.close();
