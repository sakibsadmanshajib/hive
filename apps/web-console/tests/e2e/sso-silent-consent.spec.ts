/**
 * SSO wave 1 acceptance journeys (spec 2026-08-23).
 *
 * Needs an opt-in live stack: self-hosted GoTrue, web-console serving
 * /oauth/consent, and Open WebUI chat. Runs ONLY when SSO_E2E_CHAT_ORIGIN is
 * set so plain CI reports it skipped rather than red.
 */
import { readFileSync } from "node:fs";

import { expect, test, type Page } from "@playwright/test";

const CHAT_ORIGIN = process.env.SSO_E2E_CHAT_ORIGIN ?? "";
const CONSOLE_ORIGIN =
  process.env.SSO_E2E_CONSOLE_ORIGIN ?? "http://console.hive.test";
const STATE_PATH = process.env.SSO_E2E_STATE_PATH ?? "";

test.skip(
  !CHAT_ORIGIN || !STATE_PATH,
  "opt-in: set SSO_E2E_CHAT_ORIGIN and SSO_E2E_STATE_PATH to run against a live stack",
);

/**
 * Narrows the fixture state file into the cookie fields Playwright needs,
 * structurally, with no casts.
 */
function readCookieArray(state: unknown): Array<{
  name: string;
  value: string;
  domain?: string;
  path?: string;
}> {
  if (typeof state !== "object" || state === null) return [];
  if (!("cookies" in state) || !Array.isArray(state.cookies)) return [];
  const out: Array<{ name: string; value: string; domain?: string; path?: string }> = [];
  for (const entry of state.cookies) {
    if (typeof entry !== "object" || entry === null) continue;
    if (!("name" in entry) || !("value" in entry)) continue;
    const name = Reflect.get(entry, "name");
    const value = Reflect.get(entry, "value");
    if (typeof name !== "string" || typeof value !== "string") continue;
    const rawDomain = Reflect.get(entry, "domain");
    const rawPath = Reflect.get(entry, "path");
    out.push({
      name,
      value,
      domain: typeof rawDomain === "string" ? rawDomain : undefined,
      path: typeof rawPath === "string" ? rawPath : undefined,
    });
  }
  return out;
}

test.use({
  launchOptions: {
    args: [
      `--host-resolver-rules=MAP ${new URL(CONSOLE_ORIGIN).host} 127.0.0.1`,
    ],
  },
});

test.describe("SSO wave 1: silent consent landing", () => {
  interface JourneyTrace {
    consentDocuments: Array<{ url: string; status: number }>;
    authorizeCount: number;
  }

  function attachTrace(page: Page): JourneyTrace {
    const trace: JourneyTrace = {
      consentDocuments: [],
      authorizeCount: 0,
    };
    page.on("response", (response) => {
      const request = response.request();
      if (!request.isNavigationRequest()) return;
      if (request.frame() !== page.mainFrame()) return;
      const url = response.url();
      if (url.includes("/oauth/consent")) {
        trace.consentDocuments.push({ url, status: response.status() });
      }
      if (url.includes("/auth/v1/oauth/authorize")) {
        trace.authorizeCount += 1;
        trace.consentDocuments.push({ url: `authorize:${url}`, status: response.status() });
      }
    });
    return trace;
  }

  test("first entry paints the panel once, every repeat entry is silent", async ({ page, context }) => {
    if (!CHAT_ORIGIN) return;
    // Real GoTrue session, minted by tests/e2e/support/sso-wave1-fixture.mjs
    // through the audited one-time-token flow; its ssr envelope rides as
    // cookies scoped to the console origin.
    const state: unknown = JSON.parse(readFileSync(STATE_PATH, "utf8"));
    const rawCookies = readCookieArray(state);
    await context.addCookies(
      rawCookies.map((cookie) => ({
        name: cookie.name,
        value: cookie.value,
        domain: cookie.domain ?? new URL(CONSOLE_ORIGIN).hostname,
        path: cookie.path ?? "/",
      })),
    );


    const trace = attachTrace(page);

    // PASS 1: first-ever consent for this user paints the interactive panel
    // exactly once; clicking Approve completes the OAuth round trip into chat.
    await page.goto(`${CHAT_ORIGIN}/`);
    const approve = page.getByRole("button", { name: "Approve" });
    await approve.waitFor({ state: "visible", timeout: 20000 });
    await approve.click();
    // The chat fork settles the OAuth hand-back through its /auth pass-through
    // (token cookie pickup); wait for the app's own signed-in marker instead
    // of a specific URL.
    await page.waitForFunction(
      () => window.localStorage.getItem("token") !== null,
      null,
      { timeout: 30000 },
    );
    await expect(
      page.locator('input[type="password"]'),
    ).toHaveCount(0);

    // PASS 2: same console session, chat session wiped (fresh context state
    // for the chat origin only). The landing must hand off server-side: every
    // /oauth/consent response in the chain is a 3xx, never a painted page.
    // Wipe everything (the OWUI session cookie included) and re-add only the
    // console ssr envelope, so pass 2 is a genuine repeat entry from a device
    // that has never seen a chat session.
    const consoleCookies = await context.cookies(CONSOLE_ORIGIN);
    await context.clearCookies();
    await context.addCookies(consoleCookies);
    // The signed-in marker also lives in localStorage; wipe it so pass 2 is
    // not silently short-circuited by the previous session.
    await page.evaluate(() => window.localStorage.clear());

    trace.consentDocuments.length = 0;
    trace.authorizeCount = 0;

    await page.goto(`${CHAT_ORIGIN}/`);
    await page.waitForFunction(
      () => window.localStorage.getItem("token") !== null,
      null,
      { timeout: 30000 },
    );
    expect(trace.authorizeCount).toBeGreaterThanOrEqual(1);
    const painted = trace.consentDocuments.filter(
      (doc) =>
        doc.url.startsWith("authorize") === false &&
        doc.status >= 200 &&
        doc.status < 300,
    );
    expect(painted).toEqual([]);
    const consentRedirects = trace.consentDocuments.filter(
      (doc) => doc.url.startsWith("authorize") === false,
    );
    // One-hop bound: the only redirect beyond the authorize round trip is the
    // landing's own 302 to the registered callback.
    expect(consentRedirects.length).toBeLessThanOrEqual(1);
    await expect(
      page.locator('input[type="password"]'),
    ).toHaveCount(0);
    await expect(page.getByRole("button", { name: "Approve" })).toHaveCount(0);
    await expect(page.getByRole("button", { name: "Deny" })).toHaveCount(0);
  });
});
