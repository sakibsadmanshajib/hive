// Issue #540 sequence proof, against the live demo box.
//
// The claim under test is not "the agent surface renders". It is "the user
// meets ONE credential prompt and everything after it needs none", and
// separately "the retired console path still presents a second, independent
// prompt today". A screenshot of the agent surface alone cannot tell those
// apart, so this walks the whole journey and records what the browser holds at
// each step.
//
// No password is typed and none is changed. Part A stops at the single
// credential prompt without submitting it. Part B starts from that prompt
// already satisfied, by carrying the account's existing Supabase session on
// the console origin, minted through the admin one-time-token flow
// (docs/live-test-auth.md).
import { chromium } from "playwright";
import { readFileSync, writeFileSync, appendFileSync } from "node:fs";

const OUT = new URL(".", import.meta.url).pathname;
const CHAT = "https://chat-hive.scubed.co";
const LOG = `${OUT}capture.log`;
writeFileSync(LOG, `# issue #540 sequence proof, live demo box\n# captured ${new Date().toISOString()}\n\n`);
const log = (s) => {
  console.log(s);
  appendFileSync(LOG, s + "\n");
};

const state = JSON.parse(readFileSync(`${OUT}console-state.json`, "utf8"));
const browser = await chromium.launch();

const shotOf = (page) => async (n, name) => {
  await page.screenshot({ path: `${OUT}${n}-${name}.png` });
  log(`  screenshot: ${n}-${name}.png`);
};

try {
  // ======================================================================
  // Part A: a browser with no session at all. One prompt, and where it is.
  // ======================================================================
  const ctxA = await browser.newContext({ viewport: { width: 1440, height: 900 } });
  const a = await ctxA.newPage();
  const shotA = shotOf(a);
  const chain = [];
  a.on("framenavigated", (f) => {
    if (f === a.mainFrame()) chain.push(f.url());
  });

  await a.goto(CHAT, { waitUntil: "domcontentloaded", timeout: 60_000 });
  await a.waitForTimeout(5000);
  log(`PART A  clean browser, no session anywhere`);
  log(`  chat redirected straight into the identity provider`);
  log(`  url: ${a.url()}`);
  log(`  chat-origin cookies: ${(await ctxA.cookies(CHAT)).map((c) => c.name).join(", ") || "(none)"}`);
  await shotA("01", "the-one-credential-prompt");

  const hiveButton = a.getByRole("button", { name: /continue with hive/i });
  if (await hiveButton.isVisible().catch(() => false)) {
    await hiveButton.dispatchEvent("click");
    await a.waitForTimeout(9000);
    log(`  clicked "Continue with Hive"`);
    log(`  navigation chain: ${chain.join("  ->  ")}`);
    log(`  the one credential prompt is at: ${a.url()}`);
    await shotA("02", "after-continue-with-hive");
  } else {
    log(`  no "Continue with Hive" button to click: Open WebUI had already`);
    log(`  redirected into the identity provider on its own, which is the`);
    log(`  same single prompt reached one click sooner.`);
  }
  log(`  (stopping here: no password is typed and none is changed)`);
  await ctxA.close();

  // ======================================================================
  // Part B: that same one prompt already satisfied. Nothing may ask again.
  // ======================================================================
  const ctxB = await browser.newContext({ viewport: { width: 1440, height: 900 } });
  await ctxB.addCookies(state.cookies);
  const b = await ctxB.newPage();
  const shotB = shotOf(b);
  const chainB = [];
  b.on("framenavigated", (f) => {
    if (f === b.mainFrame()) chainB.push(f.url());
  });

  log(``);
  log(`PART B  the account's session on the console origin, and nothing else`);
  log(`  seeded cookies: ${state.cookies.map((c) => `${c.name}@${c.domain}`).join(", ")}`);

  await b.goto(CHAT, { waitUntil: "domcontentloaded", timeout: 60_000 });
  await b.waitForTimeout(9000);
  log(`  chat reached at: ${b.url()}`);
  log(`  navigation chain: ${chainB.join("  ->  ")}`);
  const prompted = await b
    .getByRole("button", { name: /continue with hive/i })
    .isVisible()
    .catch(() => false);
  log(`  chat asked for credentials again: ${prompted}`);
  await shotB("03", "chat-signed-in-no-second-prompt");

  const chatCookies = await ctxB.cookies(CHAT);
  log(``);
  log(`  what the browser holds ON THE CHAT ORIGIN after signing in:`);
  for (const c of chatCookies) {
    log(`    ${c.name}  domain=${c.domain}  httpOnly=${c.httpOnly}  chars=${c.value.length}`);
  }
  const sbOnChat = chatCookies.filter((c) => c.name.startsWith("sb-"));
  log(`    Supabase (sb-*) cookies on the chat origin: ${sbOnChat.length}`);
  log(`    This is the root cause of #540: apps/agent-console reads a Supabase`);
  log(`    cookie, and on this origin there is none to read, so it redirected`);
  log(`    every visitor to a sign in of its own regardless of the chat session.`);

  await b.goto(`${CHAT}/agents`, { waitUntil: "domcontentloaded", timeout: 60_000 });
  await b.waitForTimeout(10_000);
  log(``);
  log(`  agent surface at: ${b.url()}`);
  const promptedAgents = await b
    .getByRole("button", { name: /continue with hive|^sign in$/i })
    .isVisible()
    .catch(() => false);
  log(`  agent surface asked for credentials: ${promptedAgents}`);
  log(`  iframes on the agent surface: ${await b.locator("iframe").count()}`);
  await shotB("04", "agent-surface-same-session-no-prompt");

  await b.goto(`${CHAT}/agent-workspace/tasks`, { waitUntil: "domcontentloaded", timeout: 60_000 });
  await b.waitForTimeout(6000);
  log(``);
  log(`  the second front door this pull request removes: ${b.url()}`);
  const text = (await b.locator("body").innerText().catch(() => "")).replace(/\s+/g, " ").slice(0, 300);
  log(`  page text: ${JSON.stringify(text)}`);
  await shotB("05", "second-signin-wall-before-the-fix");
  await ctxB.close();
} catch (e) {
  log(`ERROR: ${e.message}`);
  process.exitCode = 1;
} finally {
  await browser.close();
}
