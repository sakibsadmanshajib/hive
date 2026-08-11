// Check, and optionally repair, the Open WebUI interface settings of a shared
// Hive chat account.
//
// WHY THIS EXISTS (issue #855)
// ---------------------------
// Open WebUI keeps "Enter Key Behavior" as a per-user setting,
// `settings.ui.ctrlEnterToSend`. With it on, Enter alone inserts a newline and
// only Ctrl+Enter sends; the send arrow keeps working either way. That is a
// legitimate user preference, so nothing in the product may force it off
// globally, and no amount of reading the patch layer or the upstream bundle
// shows anything wrong when it is set.
//
// The demo account is shared mutable state. On 2026-08-11 it was found with
// that preference on, so a presenter's Enter did nothing and the failure looked
// like a hung backend rather than a keybinding. Nothing in the product sets it;
// an automated sweep over the settings surface did, and left it set. The
// account had been in that state for days with no signal.
//
// This is the signal. Run it before a demo, or after anything that walks the
// settings surface of a shared account:
//
//   node scripts/demo-chat-settings.mjs demo@example.invalid            # check
//   node scripts/demo-chat-settings.mjs demo@example.invalid --repair   # fix
//
// Exit code is 1 when the account has drifted and --repair was not passed, so
// this can gate a demo rehearsal instead of being read by eye.
//
// Sessions come from tests/e2e/support/live-auth.mjs, the one audited way to
// sign an automated run in. It mints a magic link with the service-role key
// and never touches the account's password. Read docs/live-test-auth.md before
// changing anything here.
//
// Requires: SUPABASE_URL, SUPABASE_SERVICE_ROLE_KEY, SUPABASE_ANON_KEY.
// Optional: CHAT_URL, CONSOLE_URL.

import { mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { pathToFileURL } from "node:url";

import { writeStorageState } from "../tests/e2e/support/live-auth.mjs";

/**
 * The settings a shared demo account must be in. Only keys whose wrong value
 * silently breaks a demo belong here: every entry costs an operator the
 * freedom to set that preference on this account, so the bar is "a presenter
 * hits a dead end with no error message", not "we prefer it off".
 *
 * ctrlEnterToSend: on, Enter stops sending and nothing on screen says so.
 */
export const DEMO_SAFE_UI_SETTINGS = Object.freeze({
  ctrlEnterToSend: false,
});

/**
 * Keys of `expected` whose value in `ui` differs. An absent key is not drift:
 * Open WebUI reads every one of these through `?? <default>`, so absent and
 * default-valued behave identically, and reporting absence would make a fresh
 * account look broken.
 *
 * @param {Record<string, unknown> | null | undefined} ui
 * @param {Record<string, unknown>} expected
 * @returns {{ key: string, expected: unknown, actual: unknown }[]}
 */
export function driftedKeys(ui, expected = DEMO_SAFE_UI_SETTINGS) {
  const actual = ui ?? {};
  return Object.entries(expected)
    .filter(([key, want]) => key in actual && actual[key] !== want)
    .map(([key, want]) => ({ key, expected: want, actual: actual[key] }));
}

async function signIn(page, chatUrl) {
  await page.goto(chatUrl, { waitUntil: "domcontentloaded" });
  await page.waitForTimeout(5000);
  const signInButton = page
    .getByRole("button", { name: /continue with hive|sign in/i })
    .first();
  if (await signInButton.count()) {
    await signInButton.click().catch(() => {});
    // The consent round trip is chat -> Supabase authorize -> console consent
    // -> back. It auto-approves because the storage state above carries a
    // Supabase session on the console origin.
    await page.waitForURL((url) => new URL(chatUrl).origin === url.origin, {
      timeout: 90_000,
    });
    await page.waitForTimeout(5000);
  }
  const token = await page.evaluate(() => localStorage.getItem("token"));
  if (!token) {
    throw new Error(
      "signed in but Open WebUI stored no token, so its API cannot be called",
    );
  }
}

async function readSettings(page) {
  const result = await page.evaluate(async () => {
    const res = await fetch("/api/v1/users/user/settings", {
      headers: { Authorization: `Bearer ${localStorage.getItem("token")}` },
    });
    return { status: res.status, body: await res.json().catch(() => null) };
  });
  if (result.status !== 200) {
    throw new Error(`GET /api/v1/users/user/settings answered ${result.status}`);
  }
  return result.body ?? {};
}

async function writeSettings(page, settings) {
  const result = await page.evaluate(async (payload) => {
    const res = await fetch("/api/v1/users/user/settings/update", {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
        Authorization: `Bearer ${localStorage.getItem("token")}`,
      },
      body: JSON.stringify(payload),
    });
    return { status: res.status, body: await res.json().catch(() => null) };
  }, settings);
  if (result.status !== 200) {
    throw new Error(
      `POST /api/v1/users/user/settings/update answered ${result.status}`,
    );
  }
  return result.body ?? {};
}

async function main() {
  const [, , email, ...flags] = process.argv;
  const repair = flags.includes("--repair");
  if (!email) {
    console.error(
      "usage: node scripts/demo-chat-settings.mjs <email> [--repair]",
    );
    process.exit(2);
  }

  const chatUrl = process.env.CHAT_URL ?? "https://chat-hive.scubed.co";
  const consoleUrl = process.env.CONSOLE_URL ?? "https://console-hive.scubed.co";

  const stateDir = mkdtempSync(join(tmpdir(), "demo-chat-settings-"));
  const statePath = join(stateDir, "state.json");
  // The Supabase session has to sit on the console origin: that is where the
  // /oauth/consent page runs, and it completes the grant silently when a
  // session is already there.
  await writeStorageState({ email, targetUrl: consoleUrl, statePath });

  // Imported here rather than at module scope so the pure helpers above stay
  // importable (and unit-testable) without pulling in a browser driver.
  const { chromium } = await import("playwright");
  const browser = await chromium.launch();
  try {
    const context = await browser.newContext({ storageState: statePath });
    const page = await context.newPage();
    await signIn(page, chatUrl);

    const settings = await readSettings(page);
    let drift = driftedKeys(settings.ui);

    if (drift.length === 0) {
      console.log(`demo-chat-settings: ${email} is demo-safe`);
      return 0;
    }

    for (const { key, expected, actual } of drift) {
      console.log(
        `demo-chat-settings: ${email} ui.${key} is ${JSON.stringify(actual)}, ` +
          `demo-safe is ${JSON.stringify(expected)}`,
      );
    }

    if (!repair) {
      console.error("demo-chat-settings: re-run with --repair to correct it");
      return 1;
    }

    // Open WebUI merges the posted object into the stored settings one level
    // deep, so `ui` is replaced wholesale. Send the account's own ui block with
    // only the drifted keys changed, or every other preference is wiped.
    const repaired = {
      ...settings,
      ui: { ...(settings.ui ?? {}) },
    };
    for (const { key, expected } of drift) {
      repaired.ui[key] = expected;
    }
    await writeSettings(page, repaired);

    const after = await readSettings(page);
    drift = driftedKeys(after.ui);
    if (drift.length > 0) {
      console.error(
        "demo-chat-settings: the repair did not stick: " +
          drift.map((d) => `ui.${d.key}=${JSON.stringify(d.actual)}`).join(", "),
      );
      return 1;
    }
    console.log(`demo-chat-settings: repaired ${email}`);
    return 0;
  } finally {
    await browser.close();
    rmSync(stateDir, { recursive: true, force: true });
  }
}

if (import.meta.url === pathToFileURL(process.argv[1] ?? "").href) {
  main()
    .then((code) => process.exit(code))
    .catch((error) => {
      console.error(error instanceof Error ? error.message : String(error));
      process.exit(1);
    });
}
