import { mkdirSync, writeFileSync } from "node:fs";
import { chromium } from "@playwright/test";
import { createBrowserClient } from "@supabase/ssr";

const APP = "http://127.0.0.1:3199";
const SUPABASE_URL = "http://127.0.0.1:4599";
const ANON = "proof-anon-key";
const OUT = "/tmp/claude-1001/-home-sakib-hive/6bd5d8d0-e038-4d0f-93c4-4955cdb2d8fc/scratchpad/shots";

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
    throw new Error("no cookies persisted");
  }
  return [...jar.entries()].map(([name, entry]) => ({
    name,
    value: entry.value,
    domain: "127.0.0.1",
    path: "/",
    expires: now + 86400,
    httpOnly: false,
    secure: false,
    sameSite: "Lax",
  }));
}

const log = [];
function note(line) {
  log.push(line);
  console.log(line);
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
  const status = response ? response.status() : 0;
  await describe(status);
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
  note(`  GET /console/catalog/does-not-exist-model -> HTTP ${status}`);
  const heading = await page.getByRole("heading", { name: "Model not found" }).textContent();
  note(`  heading: ${heading}`);
  const html = await page.content();
  note(`  server HTML carries the heading: ${/Model not found/.test(html)}`);
  note(`  bare Next error shell present: ${/__next_error__/.test(html)}`);
});
note("");

note("2. Issue 1647: Try in chat is gated on the chat capability badge");
await shot("02-catalog-gated-links", "/console/catalog", async (status) => {
  note(`  GET /console/catalog -> HTTP ${status}`);
  const links = await page.getByRole("link", { name: /try in chat/i }).count();
  note(`  rows in the fixture catalog: 5 (2 chat, 1 embeddings, 1 stt, 1 tts)`);
  note(`  try-in-chat links rendered: ${links}`);
  const body = await page.textContent("body");
  note(`  the word Hidden reaches the customer: ${/\\bHidden\\b/.test(body)}`);
  note(`  hive-fast status badge reads Deprecated: ${/Deprecated/.test(body)}`);
});
note("");

note("3. Issue 1649: unknown invoice id answers 404, not 500");
await shot("03-invoice-404", "/api/invoices/00000000-0000-0000-0000-000000000001/pdf", async (status) => {
  note(`  GET /api/invoices/00000000-0000-0000-0000-000000000001/pdf -> HTTP ${status}`);
  note(`  body: ${(await page.textContent("body")).trim()}`);
});
note("");

note("4. Issue 1653: a stored bidi override renders inert");
await shot("04-logs-bidi-inert", "/console/logs", async (status) => {
  note(`  GET /console/logs -> HTTP ${status}`);
  await page.selectOption("#logs-key-filter", "key-1");
  const rendered = await page.locator("#logs-key-filter option[value=key-1]").textContent();
  note(`  stored nickname (escaped): prod\\\\u202Egnp.txt`);
  note(`  rendered option text: ${rendered}`);
  note(`  rendered text still carries U+202E: ${rendered.includes("\\u202E")}`);
});

writeFileSync(`${OUT}/capture.log`, log.join("\\n") + "\\n");
await browser.close();
