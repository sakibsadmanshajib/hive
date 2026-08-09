// Signs the coverage sweep in and saves storageState.
//
// Two paths, because the two places this runs have different credentials:
//
//  * password grant, when OWUI_E2E_EMAIL and OWUI_E2E_PASSWORD are set (CI's
//    seeded user), driven through the real sign-in form.
//  * admin session mint, when only a service-role key is available. It calls
//    generate_link + verify, which reads and writes nothing about the user's
//    password, and plants the resulting session as the @supabase/ssr cookie on
//    the console origin. Resetting a shared demo account's password to log in
//    is not an option: it locks out everyone else holding the old one.
//
// Either way the session is then carried through the real "Continue with Hive"
// OIDC hop, so the saved state is one a real user could have produced.
import fs from "node:fs";
import path from "node:path";

import { test as setup, expect, type BrowserContext } from "@playwright/test";

const STATE = path.join(__dirname, ".auth", "state.json");
const CHAT = process.env.CHAT_URL ?? process.env.OWUI_URL ?? "";
const CONSOLE = process.env.CONSOLE_URL ?? "";
const EMAIL = process.env.OWUI_E2E_EMAIL ?? "";
const PASSWORD = process.env.OWUI_E2E_PASSWORD ?? "";
const SUPABASE_URL = process.env.SUPABASE_URL ?? process.env.NEXT_PUBLIC_SUPABASE_URL ?? "";
const SERVICE_KEY = process.env.SUPABASE_SERVICE_ROLE_KEY ?? "";
const ANON_KEY = process.env.SUPABASE_ANON_KEY ?? process.env.NEXT_PUBLIC_SUPABASE_ANON_KEY ?? "";

// @supabase/ssr stores the session as `base64-<base64url(json)>`, split into
// `<name>.0`, `<name>.1` ... once it exceeds this many characters. Our value is
// base64url, so it survives encodeURIComponent unchanged and the library's
// escape-boundary handling never applies. Mis-encoding cannot pass silently:
// the console redirects to /auth and this file throws.
const MAX_CHUNK = 3180;

function base64url(value: string): string {
  return Buffer.from(value, "utf8")
    .toString("base64")
    .replace(/\+/g, "-")
    .replace(/\//g, "_")
    .replace(/=+$/, "");
}

async function mintSession(): Promise<Record<string, unknown>> {
  const link = await fetch(`${SUPABASE_URL}/auth/v1/admin/generate_link`, {
    method: "POST",
    headers: {
      apikey: SERVICE_KEY,
      Authorization: `Bearer ${SERVICE_KEY}`,
      "Content-Type": "application/json",
    },
    body: JSON.stringify({
      type: "magiclink",
      email: EMAIL,
      options: { redirect_to: `${CONSOLE}/console` },
    }),
  });
  expect(link.ok, `generate_link failed with ${link.status}`).toBeTruthy();
  const { hashed_token: hashedToken } = (await link.json()) as { hashed_token?: string };
  expect(hashedToken, "generate_link returned no hashed_token").toBeTruthy();

  const verify = await fetch(`${SUPABASE_URL}/auth/v1/verify`, {
    method: "POST",
    headers: {
      apikey: ANON_KEY,
      Authorization: `Bearer ${ANON_KEY}`,
      "Content-Type": "application/json",
    },
    body: JSON.stringify({ type: "magiclink", token_hash: hashedToken }),
  });
  expect(verify.ok, `verify failed with ${verify.status}`).toBeTruthy();
  return (await verify.json()) as Record<string, unknown>;
}

async function plantSession(context: BrowserContext): Promise<void> {
  const token = await mintSession();
  const ref = new URL(SUPABASE_URL).hostname.split(".")[0];
  const session = {
    access_token: token.access_token,
    refresh_token: token.refresh_token,
    expires_in: token.expires_in,
    expires_at:
      token.expires_at ?? Math.floor(Date.now() / 1000) + Number(token.expires_in ?? 3600),
    token_type: token.token_type ?? "bearer",
    user: token.user,
  };
  const encoded = `base64-${base64url(JSON.stringify(session))}`;
  const name = `sb-${ref}-auth-token`;
  const chunks: Array<{ name: string; value: string }> =
    encoded.length <= MAX_CHUNK
      ? [{ name, value: encoded }]
      : Array.from({ length: Math.ceil(encoded.length / MAX_CHUNK) }, (_, i) => ({
          name: `${name}.${i}`,
          value: encoded.slice(i * MAX_CHUNK, (i + 1) * MAX_CHUNK),
        }));

  await context.addCookies(
    chunks.map((c) => ({
      name: c.name,
      value: c.value,
      domain: new URL(CONSOLE).hostname,
      path: "/",
      httpOnly: false,
      secure: true,
      sameSite: "Lax" as const,
      expires: Math.floor(Date.now() / 1000) + 3600,
    })),
  );
}

setup("authenticate against the chat surface", async ({ browser }) => {
  setup.setTimeout(5 * 60_000);
  const canMint = Boolean(SUPABASE_URL && SERVICE_KEY && ANON_KEY && CONSOLE && EMAIL);
  const canPassword = Boolean(EMAIL && PASSWORD && CHAT);
  if (!CHAT || (!canMint && !canPassword)) {
    setup.skip(true, "no chat target or no credentials for it");
    return;
  }

  const context = await browser.newContext({ viewport: { width: 1440, height: 950 } });
  const page = await context.newPage();

  if (canPassword) {
    await page.goto(`${CHAT}/auth`, { waitUntil: "domcontentloaded" });
    await page.waitForTimeout(4000);
    await page.getByRole("button", { name: /continue with hive/i }).dispatchEvent("click");
    const emailBox = page.getByRole("textbox", { name: /email/i });
    await emailBox.waitFor({ timeout: 45_000 });
    await emailBox.fill(EMAIL);
    await page.getByRole("textbox", { name: /password/i }).fill(PASSWORD);
    await page.getByRole("button", { name: /continue/i }).click();
    // #554: the password must not be sitting in the DOM when a later assertion
    // times out and Playwright serialises the page into the error context.
    await page.getByRole("textbox", { name: /password/i }).fill("").catch(() => {});
  } else {
    await plantSession(context);
    await page.goto(`${CONSOLE}/console`, { waitUntil: "domcontentloaded" });
    expect(
      new URL(page.url()).pathname.startsWith("/auth"),
      "console rejected the minted session",
    ).toBeFalsy();
    await page.goto(CHAT, { waitUntil: "domcontentloaded" });
    // The sign-in page hydrates late; a click dispatched before that lands on a
    // button with no handler bound and silently does nothing.
    await page.waitForTimeout(4000);
    const hive = page.getByRole("button", { name: /continue with hive/i });
    if (await hive.isVisible().catch(() => false)) {
      await hive.dispatchEvent("click");
      await page.waitForTimeout(8000);
    }
  }

  const approve = page.getByRole("button", { name: /approve/i });
  if (await approve.isVisible().catch(() => false)) {
    await approve.click().catch(() => {});
    await page.waitForTimeout(8000);
  }

  await page.waitForURL((u) => u.origin === new URL(CHAT).origin, { timeout: 90_000 });
  await page.getByRole("button", { name: /new chat/i }).first().waitFor({ timeout: 90_000 });

  fs.mkdirSync(path.dirname(STATE), { recursive: true });
  await context.storageState({ path: STATE });
  await context.close();
});
