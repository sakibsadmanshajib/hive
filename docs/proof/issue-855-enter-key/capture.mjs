// Proof capture for issue #855: does Enter send in the deployed chat composer?
//
// usage (from apps/web-console, where Playwright is installed):
//   node ../../docs/proof/issue-855-enter-key/capture.mjs <label> <outdir> <email>
//
// Runs three checks against the live chat surface and screenshots each:
//   1. Enter sends, and a real assistant completion comes back. The assertion
//      is on the answer text, not on the presence of a Copy button: an error
//      bubble renders a Copy button too, so a Copy button proves nothing.
//   2. Shift+Enter inserts a newline and does NOT send.
//   3. Whatever chat this run created is deleted again (issue #848: the demo
//      account is already full of test litter).
//
// Sessions come from tests/e2e/support/live-auth.mjs, which mints a magic link
// with the service-role key and never touches the account's password. No URL in
// this journey carries a credential, so no redaction is needed on the captures.
// Resolved by path rather than by name: ESM resolves bare specifiers from the
// importing file, and this file does not live under apps/web-console.
import { chromium } from "../../../apps/web-console/node_modules/playwright/index.mjs";
import fs from "node:fs";
import path from "node:path";

import { writeStorageState } from "../../../apps/web-console/tests/e2e/support/live-auth.mjs";

const CHAT = process.env.CHAT_URL ?? "https://chat-hive.scubed.co";
const CONSOLE = process.env.CONSOLE_URL ?? "https://console-hive.scubed.co";
const [, , label = "run", outdir = ".", email] = process.argv;
if (!email) {
  console.error("usage: node capture.mjs <label> <outdir> <email>");
  process.exit(2);
}
fs.mkdirSync(outdir, { recursive: true });

const PROMPT = "Reply with exactly: HIVE ENTER PROBE";
const lines = [];
const say = (s) => {
  lines.push(s);
  console.log(s);
};

const statePath = path.join(outdir, `.auth-${label}.json`);
await writeStorageState({ email, targetUrl: CONSOLE, statePath });

const browser = await chromium.launch();
const context = await browser.newContext({ viewport: { width: 1440, height: 900 } , storageState: statePath });
const page = await context.newPage();
const chatPosts = [];
page.on("request", (r) => {
  if (r.method() === "POST" && r.url().includes("/api/chat/completions")) chatPosts.push(r.url());
});

await page.goto(CHAT, { waitUntil: "domcontentloaded" });
await page.waitForTimeout(5000);
const signIn = page.getByRole("button", { name: /continue with hive|sign in/i }).first();
if (await signIn.count()) {
  await signIn.click().catch(() => {});
  await page.waitForURL((url) => new URL(CHAT).origin === url.origin, { timeout: 90_000 });
  await page.waitForTimeout(5000);
}
say(`label: ${label}`);
say(`url: ${page.url()}`);

const settings = await page.evaluate(async () => {
  const res = await fetch("/api/v1/users/user/settings", {
    headers: { Authorization: `Bearer ${localStorage.getItem("token")}` },
  });
  return res.json();
});
say(`settings.ui.ctrlEnterToSend: ${JSON.stringify(settings?.ui?.ctrlEnterToSend)}`);

for (let i = 0; i < 5; i++) {
  if ((await page.locator('[role="dialog"]').count()) === 0) break;
  await page.keyboard.press("Escape");
  await page.waitForTimeout(400);
  const okish = page.getByRole("button", { name: /okay|got it|close|let's go/i }).first();
  if (await okish.count()) await okish.click({ timeout: 2000 }).catch(() => {});
  await page.waitForTimeout(400);
}

// ---- 1. Enter --------------------------------------------------------------
const input = page.locator("#chat-input");
await input.click();
await page.keyboard.type(PROMPT);
await page.waitForTimeout(600);
await page.screenshot({ path: path.join(outdir, `${label}-1-typed.png`) });

chatPosts.length = 0;
await page.keyboard.press("Enter");
// Read the rendered assistant turn directly. A locator would block on its own
// timeout for every poll in the failing case, where no assistant turn ever
// appears, and turn a 60 second wait into a 20 minute one.
let answer = "";
for (let i = 0; i < 40; i++) {
  await page.waitForTimeout(1500);
  answer = await page.evaluate(() => {
    const turns = document.querySelectorAll(".chat-assistant");
    return turns.length ? (turns[turns.length - 1].innerText ?? "") : "";
  });
  if (answer.includes("HIVE ENTER PROBE")) break;
}
say(`POST /api/chat/completions after Enter: ${chatPosts.length}`);
say(`composer after Enter: ${JSON.stringify((await input.innerText()).slice(0, 80))}`);
say(`assistant answer after Enter: ${JSON.stringify(answer.slice(0, 200))}`);
say(`ENTER SENT AND ANSWERED: ${answer.includes("HIVE ENTER PROBE")}`);
await page.screenshot({ path: path.join(outdir, `${label}-2-after-enter.png`) });

// ---- 2. Shift+Enter --------------------------------------------------------
chatPosts.length = 0;
await input.click();
await page.keyboard.type("first line");
await page.keyboard.press("Shift+Enter");
await page.keyboard.type("second line");
await page.waitForTimeout(1500);
const composer = await input.innerText();
say(`POST /api/chat/completions after Shift+Enter: ${chatPosts.length}`);
say(`composer after Shift+Enter: ${JSON.stringify(composer)}`);
say(
  `SHIFT+ENTER MADE A NEWLINE AND DID NOT SEND: ${
    composer.includes("first line") && composer.includes("second line") && chatPosts.length === 0
  }`,
);
await page.screenshot({ path: path.join(outdir, `${label}-3-shift-enter.png`) });

// ---- 3. Clean up whatever this run created ---------------------------------
const removed = await page.evaluate(async (prompt) => {
  const token = localStorage.getItem("token");
  const headers = { Authorization: `Bearer ${token}` };
  const list = await (await fetch("/api/v1/chats/?page=1", { headers })).json().catch(() => []);
  const mine = (Array.isArray(list) ? list : []).filter((c) =>
    (c.title ?? "").includes("HIVE ENTER PROBE") || (c.title ?? "").includes(prompt.slice(0, 20)),
  );
  const gone = [];
  for (const chat of mine) {
    const res = await fetch(`/api/v1/chats/${chat.id}`, { method: "DELETE", headers });
    gone.push(`${chat.id.slice(0, 8)}=${res.status}`);
  }
  return gone;
}, PROMPT);
say(`cleanup: deleted ${removed.length} chat(s) [${removed.join(", ")}]`);

fs.writeFileSync(path.join(outdir, `${label}.log`), lines.join("\n") + "\n");
fs.rmSync(statePath, { force: true });
await browser.close();
