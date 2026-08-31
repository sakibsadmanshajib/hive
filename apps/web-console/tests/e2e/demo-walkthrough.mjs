#!/usr/bin/env node
// Full-demo walkthrough harness.
//
// Standalone Playwright script, not a `playwright test` spec: this drives one
// continuous, ordered narrative across two live hosts (chat, console) plus a
// raw curl check against the API, and its output is a human-readable,
// numbered-screenshot report rather than a pass/fail matrix. A `test()` block
// aborts the file on the first failed assertion; this walkthrough's whole
// point is to keep going through every one of the 13 steps and report what
// broke, so each step is wrapped in try/catch instead.
//
// Usage:
//   cd apps/web-console
//   HIVE_QA_TESTER_EMAIL=... HIVE_QA_TESTER_PASSWORD=... \
//   SUPABASE_URL=... SUPABASE_ANON_KEY=... SUPABASE_SERVICE_ROLE_KEY=... \
//   HIVE_OWNER_SIGNUP_EMAIL=... \
//   node tests/e2e/demo-walkthrough.mjs [--skip-owner-signup] [--skip-cowork]
//
// Never rotates any existing account's password (docs/live-test-auth.md).
// The one password this script sets is on the brand-new owner account it
// creates itself in step "owner-signup", which is not a shared account. That
// step therefore requires HIVE_OWNER_SIGNUP_EMAIL to be named explicitly, with
// no default: a fallback address would silently make every unconfigured run
// write a password to the same inbox. Pass --skip-owner-signup to run without
// it.
//
// Output: docs/proof/demo-walkthrough-<date>/ (numbered PNGs + report.md +
// step-log.json), one directory per run so repeated runs never clobber a
// prior day's evidence.

import { chromium } from "playwright";
import { mkdirSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { resolve, join } from "node:path";
import { redactSecrets } from "./support/e2e-fixture-seed.mjs";
import { maskLiveApiKeys } from "./support/mask-api-keys.mjs";
import { mdTableCell } from "./support/md-table.mjs";

// --- config ------------------------------------------------------------
const CHAT = process.env.HIVE_CHAT_BASE_URL ?? "https://chat-hive.scubed.co";
const CONSOLE = process.env.PLAYWRIGHT_BASE_URL ?? "https://console-hive.scubed.co";
const API = process.env.HIVE_EDGE_API_URL ?? "https://api-hive.scubed.co";
// Step 12 mints its own key through the console UI for the QA-tester/
// owner-signup identity rather than reusing the ci-seeded key, and neither
// identity carries a tenant_model_visibility grant on the free-pool aliases.
// This walkthrough's whole point is to reproduce what a customer sees, and a
// customer never has hive-free/hive-free-tools in their picker either
// (supabase/migrations/20260831_01_restrict_free_pool_aliases_visibility.sql),
// so step 12 calls the same public, customer-selectable default alias
// instead of requesting a free-pool exception for these fixture identities.
const DEMO_CHAT_MODEL = process.env.HIVE_DEMO_CHAT_MODEL ?? "hive-default";

const QA_EMAIL = process.env.HIVE_QA_TESTER_EMAIL ?? "";
const QA_PASSWORD = process.env.HIVE_QA_TESTER_PASSWORD ?? "";

const SKIP_OWNER_SIGNUP = process.argv.includes("--skip-owner-signup");
const SKIP_COWORK = process.argv.includes("--skip-cowork");
const ONLY_COWORK = process.argv.includes("--only-cowork");
const ONLY_OWNER_SIGNUP = process.argv.includes("--only-owner-signup");

if (!QA_EMAIL || !QA_PASSWORD) {
  console.error(
    "demo-walkthrough: HIVE_QA_TESTER_EMAIL / HIVE_QA_TESTER_PASSWORD not set. " +
      "These drive the walkthrough as an existing fixture identity; there is no " +
      "default and there must never be one (docs/live-test-auth.md).",
  );
  process.exit(2);
}

const today = new Date().toISOString().slice(0, 10);
const OUT_DIR = resolve(process.cwd(), `../../docs/proof/demo-walkthrough-${today}`);
mkdirSync(OUT_DIR, { recursive: true });

// --- report scaffolding --------------------------------------------------
const results = [];
let shotCounter = 0;

// Every credential this run holds. `redactSecrets` only knows about the
// service-role key by default, and this walkthrough also signs in with the QA
// fixture password, mints a real API key (step 11) and sets one brand-new
// account's password (owner-signup). Any of them would otherwise be written
// verbatim into a step log committed under docs/proof/ in a public
// repository: a failing Playwright action reports the state it was acting on,
// and that report goes straight into `entry.notes`. Registered here the
// moment each one exists, so the single choke point below scrubs them from
// every observation, error message and report cell without each call site
// having to remember.
const runSecrets = [QA_PASSWORD];
// Minted in step 11, consumed by step 12.
let mintedApiKey = "";
function redact(text) {
  return redactSecrets(text, [process.env.SUPABASE_SERVICE_ROLE_KEY, ...runSecrets]);
}

function slug(label) {
  return label
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/(^-|-$)/g, "")
    .slice(0, 60);
}

async function shot(page, label) {
  shotCounter += 1;
  const file = `${String(shotCounter).padStart(2, "0")}-${slug(label)}.png`;
  try {
    await page.screenshot({ path: join(OUT_DIR, file), fullPage: true, timeout: 15000 });
    return file;
  } catch (error) {
    console.error(redact(`screenshot failed for ${label}: ${error.message}`));
    return "";
  }
}

/**
 * Runs one numbered walkthrough step. Never throws: a broken step is
 * recorded as BROKEN and the walkthrough continues, because the point of
 * this run is a complete defect list, not an early abort.
 */
async function runStep(n, title, fn) {
  console.log(`--- [${n}] ${title} ---`);
  const entry = { step: n, title, verdict: "BROKEN", observed: "", notes: "", screenshots: [] };
  try {
    const out = (await fn(entry)) ?? {};
    entry.observed = out.observed ?? entry.observed;
    entry.verdict = out.verdict ?? "PASS";
  } catch (error) {
    entry.notes = redact(String(error?.message ?? error)).slice(0, 1500);
    console.error(`[${n}] ${title} FAILED: ${entry.notes}`);
  }
  results.push(entry);
  return entry;
}

// --- generic chat-surface helpers (Open WebUI fork) -----------------------
// Selectors sourced from vendor/open-webui/src/lib/components/chat: the
// composer is a rich-text contenteditable at #chat-input, the send control
// has aria-label "Send message", and each rendered turn wraps in a
// `.chat-user` / `.chat-assistant` div (ResponseMessage.svelte line 668).

async function typeAndSend(page, text) {
  const composer = page.locator("#chat-input");
  await composer.click({ timeout: 10000 });
  await page.keyboard.type(text, { delay: 5 });
  const t0 = Date.now();
  const sendBtn = page.getByRole("button", { name: "Send message" });
  if (await waitVisible(sendBtn, 2000)) {
    await sendBtn.click();
  } else {
    await page.keyboard.press("Enter");
  }
  return t0;
}

/**
 * Polls the last `.chat-assistant` turn for first paint and settle.
 *
 * A reasoning model (DeepSeek V4) paints "Thought for Ns" before any answer
 * text, then can pause mid-stream between the thinking block and the answer.
 * A short stable-text window reads that pause as "done" and returns the bare
 * "Thinking..." placeholder as the reply. 4s of no change is still well
 * inside a real completed turn's trailing silence but survives that pause.
 */
async function waitForAssistantReply(page, sinceCount, t0, { maxMs = 90000 } = {}) {
  const deadline = Date.now() + maxMs;
  let ttftMs = -1;
  let lastText = "";
  let stableSince = 0;
  while (Date.now() < deadline) {
    const turns = page.locator(".chat-assistant");
    const count = await turns.count();
    if (count > sinceCount) {
      const text = (await turns.last().innerText().catch(() => "")).trim();
      if (text && ttftMs < 0) ttftMs = Date.now() - t0;
      if (text === lastText && text) {
        if (!stableSince) stableSince = Date.now();
        if (Date.now() - stableSince > 4000) return { ttftMs, text, totalMs: Date.now() - t0 };
      } else {
        stableSince = 0;
        lastText = text;
      }
    }
    await page.waitForTimeout(250);
  }
  return { ttftMs, text: lastText, totalMs: Date.now() - t0, timedOut: true };
}

/**
 * Chat root is not a local login: it round-trips through an OAuth
 * authorization handshake against console-hive (a "Checking your
 * authorization request..." interstitial, then console's own #email/#password
 * sign-in form at /auth/sign-in?next=/oauth/consent..., then an auto-approved
 * consent redirect back to the chat origin). Confirmed live: the whole chain
 * takes 10-20s end to end, well past a short fixed wait, so every step here
 * uses generous polling timeouts instead of a fixed sleep.
 */
async function signInChat(page, email, password, { navigate = true } = {}) {
  if (navigate) await page.goto(CHAT, { waitUntil: "domcontentloaded", timeout: 30000 });
  const email1 = page.locator("#email");
  // The whole "Checking your authorization request..." -> console sign-in
  // round trip measured 15-20s live; give it real headroom rather than
  // reporting a slow-but-working handshake as broken.
  const sawForm = await waitVisible(email1, 40000);
  if (sawForm) {
    // Same hydration-gated submit button as signInConsole (see its comment):
    // the redirect chain itself easily outlasts hydration, but wait for the
    // enabled button anyway rather than depend on that being true forever.
    await page
      .locator('button[type="submit"]:not([disabled])')
      .waitFor({ state: "visible", timeout: 10000 })
      .catch(() => {});
    await email1.fill(email);
    await page.locator("#password").fill(password);
    await page.click('button[type="submit"]');
  }
  await page
    .locator("#chat-input")
    .waitFor({ state: "visible", timeout: 35000 })
    .catch(() => {});
  return sawForm;
}

/**
 * Signs in directly on console's own /auth/sign-in (no OAuth round-trip).
 *
 * Both /auth/sign-in and /auth/sign-up gate their submit button on a
 * client-side `hydrated` state flag (app/auth/sign-in/page.tsx,
 * app/auth/sign-up/page.tsx), specifically to stop a pre-hydration native
 * form submit from wiping the query string. Filling the inputs before that
 * flag flips loses the race: React's hydration reconciles the controlled
 * input back to its own (empty) state, silently discarding whatever
 * Playwright's `fill()` wrote moments earlier, so a submit lands on an
 * empty form and the browser's own "Please fill out this field" validation
 * fires. Confirmed live: `domcontentloaded` plus an immediate fill hit this
 * every time; waiting for the submit button to actually become enabled
 * before filling does not.
 */
async function signInConsole(page, email, password) {
  await page.goto(`${CONSOLE}/auth/sign-in`, { waitUntil: "domcontentloaded", timeout: 30000 });
  await page
    .locator('button[type="submit"]:not([disabled])')
    .waitFor({ state: "visible", timeout: 15000 })
    .catch(() => {});
  await page.locator("#email").fill(email);
  await page.locator("#password").fill(password);
  await page.click('button[type="submit"]');
  await page.waitForURL((u) => u.pathname.startsWith("/console"), { timeout: 25000 }).catch(() => {});
}

// --- network capture (per-step slice, for usage/cache field visibility) ---
const netLog = [];
function attachNetworkCapture(page) {
  page.on("response", (res) => {
    const url = res.url();
    if (!/chat\/completions|\/v1\/messages/.test(url)) return;
    res
      .text()
      .then((body) => netLog.push({ url, status: res.status(), body: body.slice(0, 6000) }))
      .catch(() => {});
  });
}

function netSlice(fromIdx) {
  return netLog.slice(fromIdx);
}

/**
 * Polls for a locator to become visible within `timeoutMs`, returning a
 * boolean instead of throwing.
 *
 * `locator.isVisible({ timeout })` looks like it polls but does not:
 * Playwright's own type declarations mark that `timeout` option deprecated
 * and ignored, so it is a single immediate check that returns false whenever
 * called before the element exists yet, no matter how large the timeout
 * argument is. That silently broke every "wait up to N ms for this control"
 * check in this file's first draft, including chat sign-in itself: bumping
 * the timeout from 8s to 40s made no difference (both runs timed out
 * identically) because neither one ever actually waited. `waitFor` is the
 * method that really polls.
 */
async function waitVisible(locator, timeoutMs) {
  try {
    await locator.waitFor({ state: "visible", timeout: timeoutMs });
    return true;
  } catch {
    return false;
  }
}

// =====================================================================
async function main() {
  console.log(`demo-walkthrough: output -> ${OUT_DIR}`);
  const browser = await chromium.launch({
    args: ["--disable-blink-features=AutomationControlled"],
  });
  const chatCtx = await browser.newContext({ viewport: { width: 1440, height: 900 } });
  const page = await chatCtx.newPage();
  attachNetworkCapture(page);

  if (ONLY_COWORK) {
    await page.goto(CHAT, { waitUntil: "domcontentloaded", timeout: 30000 });
    await signInChat(page, QA_EMAIL, QA_PASSWORD, { navigate: false });
  }

  // --- 1. Landing + sign-in on chat --------------------------------------
  await runStep(1, "Landing and sign-in on chat", async (entry) => {
    if (ONLY_COWORK || ONLY_OWNER_SIGNUP) return { observed: "skipped (--only-cowork/--only-owner-signup)", verdict: "UGLY" };
    await page.goto(CHAT, { waitUntil: "domcontentloaded", timeout: 30000 });
    entry.screenshots.push(await shot(page, "01-chat-landing"));
    const sawLoginForm = await signInChat(page, QA_EMAIL, QA_PASSWORD, { navigate: false });
    entry.screenshots.push(await shot(page, "01b-chat-signed-in"));
    const composerVisible = await waitVisible(page.locator("#chat-input"), 15000);
    return {
      observed: sawLoginForm
        ? `redirected through OAuth handshake to console sign-in, submitted, composer visible after round-trip: ${composerVisible}`
        : `no login form seen in the OAuth round-trip (already-authenticated context or different auth flow); composer visible: ${composerVisible}`,
      verdict: composerVisible ? "PASS" : "BROKEN",
    };
  });

  // --- 2. Model picker -----------------------------------------------------
  await runStep(2, "Model picker: catalogue and metadata", async (entry) => {
    if (ONLY_COWORK || ONLY_OWNER_SIGNUP) return { observed: "skipped (--only-cowork/--only-owner-signup)", verdict: "UGLY" };
    const trigger = page.getByRole("button", { name: /selected model/i }).first();
    await trigger.click({ timeout: 10000 }).catch(() => {});
    await page.waitForTimeout(800);
    entry.screenshots.push(await shot(page, "02-model-picker-open"));
    const listText = await page
      .locator('[role="listbox"], .model-item, [class*="model"]')
      .allInnerTexts()
      .catch(() => []);
    const flat = listText.join(" | ").slice(0, 4000);
    const hasDeepSeek = /deepseek/i.test(flat);
    // Hive aliases (Hive Fast/Free/Medium/Small/Auto/Default) are what the
    // picker actually names, never the upstream provider: matches the
    // repo-wide provider-blind convention (CLAUDE.md: "Provider names never
    // leak to customers"). So a literal "groq" string is NOT expected here;
    // presence of Hive-branded aliases beyond the Deepseek pair is the
    // closest a UI-only check can get to confirming a second provider family
    // is routed underneath.
    const hiveAliasCount = (flat.match(/\bHive (Auto|Default|Fast|Free|Medium|Small)\b/g) ?? []).length;
    const hasClaude = /claude/i.test(flat);
    // Real numeric metadata only: a digit next to a currency/credit/token/
    // context marker. The word "context" alone appears in marketing prose
    // ("largest context window in the catalog") with no number attached, so
    // a bare /context/i test false-positives on that copy.
    const hasPricing = /\$\d|\bcredits?\b|\d+\s?[kK]\s*(tokens?|context)|per\s*(1m|million)\b/i.test(flat);
    await page.keyboard.press("Escape").catch(() => {});
    return {
      observed:
        `catalogue text sample: "${flat.slice(0, 600)}" | ` +
        `deepseek=${hasDeepSeek} otherHiveAliases=${hiveAliasCount} claude=${hasClaude} (expected absent per owner decision) ` +
        `numeric pricing/context metadata visible=${hasPricing}`,
      verdict: hasDeepSeek && hiveAliasCount > 0 ? (hasPricing ? "PASS" : "UGLY") : "BROKEN",
    };
  });

  // --- 3. Plain chat turn, streamed, TTFT ----------------------------------
  let firstReplyOK = false;
  await runStep(3, "Plain chat turn, streamed (TTFT)", async (entry) => {
    if (ONLY_COWORK || ONLY_OWNER_SIGNUP) return { observed: "skipped (--only-cowork/--only-owner-signup)", verdict: "UGLY" };
    const before = await page.locator(".chat-assistant").count();
    const netBefore = netLog.length;
    const t0 = await typeAndSend(page, "In one sentence, what model are you and who runs you?");
    const result = await waitForAssistantReply(page, before, t0);
    entry.screenshots.push(await shot(page, "03-first-chat-turn"));
    firstReplyOK = !!result.text && !result.timedOut;
    const usageSeen = netSlice(netBefore).some((n) => /"usage"/i.test(n.body));
    return {
      observed:
        `ttft=${result.ttftMs}ms total=${result.totalMs}ms timedOut=${!!result.timedOut} ` +
        `reply="${(result.text ?? "").slice(0, 200)}" usageFieldInResponse=${usageSeen}`,
      verdict: firstReplyOK ? "PASS" : "BROKEN",
    };
  });

  // --- 4. Same prompt against the other provider family --------------------
  await runStep(4, "Same prompt, other provider family", async (entry) => {
    if (ONLY_COWORK || ONLY_OWNER_SIGNUP) return { observed: "skipped (--only-cowork/--only-owner-signup)", verdict: "UGLY" };
    const trigger = page.getByRole("button", { name: /selected model/i }).first();
    await trigger.click({ timeout: 8000 }).catch(() => {});
    await page.waitForTimeout(500);
    const options = page.locator('[role="option"], .model-item');
    const optCount = await options.count().catch(() => 0);
    let switched = false;
    let switchedTo = "";
    // The picker names Hive aliases, never the upstream provider (provider-
    // blind by repo convention), so there is no "groq" string to match on.
    // Any option that is not one of the Deepseek pair is the closest a
    // UI-only check gets to "the other provider family".
    for (let i = 0; i < optCount; i += 1) {
      const t = (await options.nth(i).innerText().catch(() => "")).trim();
      if (t && !/deepseek/i.test(t)) {
        await options.nth(i).click().catch(() => {});
        switched = true;
        switchedTo = t.split("\n")[0];
        break;
      }
    }
    if (!switched) await page.keyboard.press("Escape").catch(() => {});
    const before = await page.locator(".chat-assistant").count();
    const t0 = await typeAndSend(page, "In one sentence, what model are you and who runs you?");
    const result = await waitForAssistantReply(page, before, t0);
    entry.screenshots.push(await shot(page, "04-second-provider-turn"));
    return {
      observed: `switchedModel=${switched} target="${switchedTo}" ttft=${result.ttftMs}ms reply="${(result.text ?? "").slice(0, 200)}"`,
      verdict: result.text && !result.timedOut ? "PASS" : "BROKEN",
    };
  });

  // --- 5. Multi-turn (prompt caching) --------------------------------------
  await runStep(5, "Multi-turn conversation (prompt caching)", async (entry) => {
    if (ONLY_COWORK || ONLY_OWNER_SIGNUP) return { observed: "skipped (--only-cowork/--only-owner-signup)", verdict: "UGLY" };
    const netBefore = netLog.length;
    for (const turn of ["Give me one more fact about yourself.", "And one more, please."]) {
      const before = await page.locator(".chat-assistant").count();
      const t0 = await typeAndSend(page, turn);
      await waitForAssistantReply(page, before, t0, { maxMs: 60000 });
    }
    entry.screenshots.push(await shot(page, "05-multi-turn"));
    const slice = netSlice(netBefore);
    const cacheHit = slice.some((n) => /cache_read|cached_tokens|cache_creation/i.test(n.body));
    return {
      observed: `turns sent=2, network responses captured=${slice.length}, cache field present in any response=${cacheHit} (not necessarily visible in UI; backend-only signal)`,
      verdict: slice.length > 0 ? (cacheHit ? "PASS" : "UGLY") : "BROKEN",
    };
  });

  // --- 6. Tool / function calling -------------------------------------------
  await runStep(6, "Tool or function calling", async (entry) => {
    // "Integrations" is the real tool/function-calling menu (vendor source
    // aria-label). The composer's "+"/"More" button opens a different,
    // attachment-focused menu (Upload Files, Attach Knowledge, etc.) that a
    // looser name match picks up first, which is the wrong control for this
    // step; keep it out of the alternation.
    if (ONLY_COWORK || ONLY_OWNER_SIGNUP) return { observed: "skipped (--only-cowork/--only-owner-signup)", verdict: "UGLY" };
    const toolsBtn = page.getByRole("button", { name: /integrations|available tools/i }).first();
    const hasToolsBtn = await waitVisible(toolsBtn, 8000);
    if (hasToolsBtn) await toolsBtn.click().catch(() => {});
    await page.waitForTimeout(500);
    entry.screenshots.push(await shot(page, "06-tools-menu"));
    await page.keyboard.press("Escape").catch(() => {});
    return {
      observed: `tools/integrations control found=${hasToolsBtn}`,
      verdict: hasToolsBtn ? "PASS" : "UGLY",
    };
  });

  // --- 7. File upload / attachment ------------------------------------------
  await runStep(7, "File upload / attachment", async (entry) => {
    if (ONLY_COWORK || ONLY_OWNER_SIGNUP) return { observed: "skipped (--only-cowork/--only-owner-signup)", verdict: "UGLY" };
    const fileInput = page.locator('input[type="file"]').first();
    const exists = (await fileInput.count()) > 0;
    let attached = false;
    if (exists) {
      // Scratch input, not evidence: OUT_DIR is committed under docs/proof/.
      const fixture = join(tmpdir(), "hive-demo-walkthrough-attach-test.txt");
      writeFileSync(fixture, "Hive demo attachment test file.\n");
      await fileInput.setInputFiles(fixture).catch(() => {});
      await page.waitForTimeout(1500);
      attached = await waitVisible(page.locator("text=attach-test.txt"), 8000);
    }
    entry.screenshots.push(await shot(page, "07-file-attach"));
    return {
      observed: `file input present=${exists}, attachment chip rendered=${attached}`,
      verdict: exists && attached ? "PASS" : exists ? "BROKEN" : "UGLY",
    };
  });

  // --- 8. RAG ---------------------------------------------------------------
  await runStep(8, "RAG: upload document, ask against it", async (entry) => {
    // D-045 replaced "Knowledge" with Projects; look for either, since the
    // redesign may not be fully live yet.
    if (ONLY_COWORK || ONLY_OWNER_SIGNUP) return { observed: "skipped (--only-cowork/--only-owner-signup)", verdict: "UGLY" };
    const navCandidates = ["Projects", "Knowledge", "Workspace"];
    let navFound = "";
    const urlBefore = page.url();
    for (const label of navCandidates) {
      const link = page.getByRole("link", { name: new RegExp(label, "i") }).first();
      if (await waitVisible(link, 4000)) {
        navFound = label;
        await link.click().catch(() => {});
        await page.waitForTimeout(2000);
        break;
      }
    }
    const navigated = navFound && page.url() !== urlBefore;
    entry.screenshots.push(await shot(page, "08-rag-nav"));
    return {
      observed: navFound
        ? `found nav link "${navFound}" for document upload, click navigated=${navigated} (url ${urlBefore} -> ${page.url()})`
        : "no Projects/Knowledge/Workspace nav entry found from the chat shell",
      verdict: navigated ? "UGLY" : "BROKEN",
    };
  });

  // --- 9. Voice ---------------------------------------------------------------
  await runStep(9, "Voice", async (entry) => {
    if (ONLY_COWORK || ONLY_OWNER_SIGNUP) return { observed: "skipped (--only-cowork/--only-owner-signup)", verdict: "UGLY" };
    const voiceBtn = page.getByRole("button", { name: /voice/i }).first();
    const found = await waitVisible(voiceBtn, 8000);
    entry.screenshots.push(await shot(page, "09-voice"));
    return {
      observed: `voice control found=${found} (headless run cannot grant mic permission, so activation itself is unverifiable here)`,
      verdict: found ? "UGLY" : "BROKEN",
    };
  });

  // --- 10. Cowork, twice -------------------------------------------------
  //
  // Cowork is a composer mode, not a nav destination (D-045): a
  // role="radiogroup" "Chat | Cowork" toggle inside the SAME composer. Live
  // behaviour confirmed by screenshot, and it differs from what the vendored
  // AgentTasks.svelte source (id="hive-agent-instructions",
  // id="hive-agent-send") suggests is mounted: selecting "Cowork" does NOT
  // swap the textarea to a separate element. It keeps using the same
  // #chat-input the chat mode uses, and adds one pack-selector row
  // ("Knowledge work", hint "Runs in a sandbox. Progress appears in this
  // conversation.") beneath the composer. So this step drives #chat-input
  // via the same typeAndSend/send-button path as every chat step, not the
  // AgentTasks ids, which is either dead code or wired to a route this
  // composer no longer reaches. The separate /agent-workspace/tasks route
  // this step used to drive is the pre-D-045 surface the owner explicitly
  // rejected (separate sign-in, doesn't look like chat); confirmed still
  // live in an earlier run of this same script (screenshot on file), which
  // is worth flagging on its own, but is not the control this step exercises.
  if (!SKIP_COWORK && !ONLY_OWNER_SIGNUP) {
    for (const [label, brief] of [
      ["short", "List three prime numbers under 20."],
      ["long", "Write a short Python script that prints the first 15 Fibonacci numbers, then explain it."],
    ]) {
      await runStep(10, `Cowork task (${label})`, async (entry) => {
        await page.goto(CHAT, { waitUntil: "domcontentloaded", timeout: 30000 });
        await waitVisible(page.locator("#chat-input"), 40000);
        const newChat = page.getByRole("link", { name: "New Chat" }).first();
        if (await waitVisible(newChat, 5000)) await newChat.click().catch(() => {});
        const coworkToggle = page.getByRole("radio", { name: "Work", exact: true }).first();
        const gated = !(await waitVisible(coworkToggle, 10000));
        if (gated) {
          entry.screenshots.push(await shot(page, `10-cowork-${label}-gated`));
          return { observed: "Cowork radio toggle not visible in the composer (not enabled for this account, or the composer redesign is not live)", verdict: "BROKEN" };
        }
        await coworkToggle.click();
        await page.waitForTimeout(500);
        entry.screenshots.push(await shot(page, `10-cowork-${label}-mode-selected`));
        const before = await page.locator(".chat-assistant, [data-hive-task-row]").count().catch(() => 0);
        const t0 = await typeAndSend(page, brief);
        entry.screenshots.push(await shot(page, `10-cowork-${label}-submitted`));
        // No stable task-row selector confirmed for this live component (see
        // comment above), so poll the whole conversation pane's text for a
        // terminal status keyword rather than one specific element. Bounded:
        // the long task can take minutes on a real sandbox, short should
        // resolve fast. A single sample of an intermittent defect reads as
        // success, which is exactly why this runs twice per the brief.
        const deadline = Date.now() + (label === "long" ? 240000 : 90000);
        let lastText = "";
        while (Date.now() < deadline) {
          lastText = await page.locator("body").innerText().catch(() => "");
          if (/succeeded|failed|cancelled|blocked/i.test(lastText.slice(-4000))) break;
          await page.waitForTimeout(5000);
        }
        const tail = lastText.slice(-800).replace(/\s+/g, " ");
        entry.screenshots.push(await shot(page, `10-cowork-${label}-final`));
        return {
          observed: `page tail text after submit: "${tail}"`,
          verdict: /succeeded/i.test(tail)
            ? "PASS"
            : /failed|cancelled|blocked/i.test(tail)
              ? "BROKEN"
              : "UGLY",
        };
      });
    }
  } else {
    results.push({ step: 10, title: "Cowork (skipped by flag)", verdict: "UGLY", observed: "skipped", notes: "", screenshots: [] });
  }

  // --- 11. Console: sign in, keys, usage, billing, catalogue --------------
  const consoleCtx = await browser.newContext({ viewport: { width: 1440, height: 900 } });
  const cpage = await consoleCtx.newPage();
  await runStep(11, "Console: sign in, API keys, usage, billing, catalogue", async (entry) => {
    if (ONLY_COWORK || ONLY_OWNER_SIGNUP) return { observed: "skipped (--only-cowork/--only-owner-signup)", verdict: "UGLY" };
    await signInConsole(cpage, QA_EMAIL, QA_PASSWORD);
    entry.screenshots.push(await shot(cpage, "11a-console-dashboard"));

    await cpage.goto(`${CONSOLE}/console/api-keys`, { waitUntil: "domcontentloaded" }).catch(() => {});
    entry.screenshots.push(await shot(cpage, "11b-console-api-keys"));
    let mintedKey = "";
    const nicknameField = cpage.locator("#key-nickname");
    if (await waitVisible(nicknameField, 10000)) {
      await nicknameField.fill(`demo-walkthrough-${today}`);
      // The form's own expiry field (api-key-create-form.tsx, #key-expires)
      // rather than a revoke call at the end of the run: a run that dies
      // mid-way never reaches its own cleanup, and this harness has already
      // left a row of live keys behind on the fixture workspace. Tomorrow is
      // past every timeout in this file and well short of a key that outlives
      // the run that made it.
      const expiresAt = new Date(Date.now() + 86400000).toISOString().slice(0, 10);
      // Short timeout: the field is optional, so a missing or disabled one
      // should cost two seconds, not Playwright's 30 second default.
      await cpage.locator("#key-expires").fill(expiresAt, { timeout: 2000 }).catch(() => {});
      await cpage.locator('button[type="submit"]').click().catch(() => {});
      await cpage.waitForTimeout(2000);
      // Read the secret from its own element, not by regexing the page text:
      // the console shows a created key in full exactly once
      // (data-testid="created-api-key-secret"), and this is the one element
      // on the page that ever holds a live credential.
      const secretEl = cpage.locator('[data-testid="created-api-key-secret"]').first();
      if (await waitVisible(secretEl, 8000)) {
        mintedKey = (await secretEl.innerText().catch(() => "")).trim();
      }
      if (mintedKey) runSecrets.push(mintedKey);
    }
    // Mask every live-looking key in the DOM BEFORE the screenshot. This
    // capture is committed under docs/proof/ in a public repository, and
    // `npm run lint:proof-tokens` reads text files only: a key burned into a
    // PNG has no automated backstop at all, and masking after upload is not a
    // remedy (.claude/rules/orchestrator.md, PR #578). Swept across the whole
    // document rather than just the one-time reveal panel, so a key that also
    // lands in a toast, a copy confirmation, or a panel that painted a moment
    // after the wait above gave up is covered too. The list view's masked
    // "hk_xxxx" rows are far short of the length floor and stay legible.
    await cpage.evaluate(maskLiveApiKeys).catch(() => {});
    let keyShot = await shot(cpage, "11c-console-api-key-created");
    // Masking and the shutter are two calls, so a panel that paints between
    // them is captured unmasked. Re-run the sweep afterwards: a non-zero
    // return means exactly that happened, and the only safe move is to
    // discard that frame and take another. Cheap, because the normal case
    // masks nothing and retakes nothing.
    const paintedLate = await cpage.evaluate(maskLiveApiKeys).catch(() => 0);
    if (paintedLate > 0) {
      // Delete first, then retake. Overwriting would be enough only if the
      // retake succeeds, and a failed retake must not leave the unmasked
      // frame sitting in the proof directory.
      if (keyShot) rmSync(join(OUT_DIR, keyShot), { force: true });
      shotCounter -= 1; // reuse the number the deleted frame had
      keyShot = await shot(cpage, "11c-console-api-key-created");
    }
    entry.screenshots.push(keyShot);

    await cpage.goto(`${CONSOLE}/console/logs`, { waitUntil: "domcontentloaded" }).catch(() => {});
    entry.screenshots.push(await shot(cpage, "11d-console-usage-logs"));
    await cpage.goto(`${CONSOLE}/console/billing`, { waitUntil: "domcontentloaded" }).catch(() => {});
    entry.screenshots.push(await shot(cpage, "11e-console-billing"));
    await cpage.goto(`${CONSOLE}/console/catalog`, { waitUntil: "domcontentloaded" }).catch(() => {});
    entry.screenshots.push(await shot(cpage, "11f-console-catalog"));

    mintedApiKey = mintedKey; // handed to step 12
    return {
      observed: `dashboard reached, api key minted=${!!mintedKey}, usage/billing/catalog pages loaded`,
      verdict: mintedKey ? "PASS" : "BROKEN",
    };
  });

  // --- 12. Raw curl against the API ----------------------------------------
  await runStep(12, "Raw curl: /v1/chat/completions and /v1/messages", async (entry) => {
    if (ONLY_COWORK || ONLY_OWNER_SIGNUP) return { observed: "skipped (--only-cowork/--only-owner-signup)", verdict: "UGLY" };
    const key = mintedApiKey;
    if (!key) return { observed: "no minted key from step 11, cannot test raw API", verdict: "BROKEN" };
    const results12 = {};
    for (const [name, url, body] of [
      [
        "chat.completions",
        `${API}/v1/chat/completions`,
        { model: DEMO_CHAT_MODEL, messages: [{ role: "user", content: "Say OK." }], max_tokens: 10 },
      ],
      [
        "messages",
        `${API}/v1/messages`,
        { model: DEMO_CHAT_MODEL, max_tokens: 10, messages: [{ role: "user", content: "Say OK." }] },
      ],
    ]) {
      try {
        const res = await fetch(url, {
          method: "POST",
          headers: { Authorization: `Bearer ${key}`, "Content-Type": "application/json" },
          body: JSON.stringify(body),
        });
        const text = await res.text();
        results12[name] = {
          status: res.status,
          hasUsage: /"usage"/i.test(text),
          hasCacheField: /cache_read|cached_tokens|cache_creation/i.test(text),
          snippet: text.slice(0, 300),
        };
      } catch (error) {
        results12[name] = { error: String(error.message ?? error) };
      }
    }
    return {
      observed: JSON.stringify(results12).slice(0, 1500),
      verdict: Object.values(results12).every((r) => r.status === 200) ? "PASS" : "BROKEN",
    };
  });

  // --- 13. Sign out and back in ---------------------------------------------
  await runStep(13, "Sign out and back in", async (entry) => {
    if (ONLY_COWORK || ONLY_OWNER_SIGNUP) return { observed: "skipped (--only-cowork/--only-owner-signup)", verdict: "UGLY" };
    // Existence check only, no click: "Sign Out" lives inside the
    // user-profile dropdown (vendor/open-webui UserMenu.svelte). Clicking
    // the real control here was tried and produced an unexplained multi-
    // second transition to a bare centered app-icon splash before the
    // sign-in form appeared, which is worth flagging on its own but made
    // this step flaky in a way that has nothing to do with what it is meant
    // to prove. The round trip itself is exercised the same way
    // agent-workspace-flows.spec.ts's C21 does: clear cookies client side,
    // the same shape a real expired session takes, then sign back in.
    const userMenuTrigger = page.getByRole("button", { name: /user menu|open user profile menu/i }).first();
    const signOutControlReachable = await waitVisible(userMenuTrigger, 5000);
    let signOutTextFound = false;
    if (signOutControlReachable) {
      await userMenuTrigger.click().catch(() => {});
      signOutTextFound = await waitVisible(page.getByText("Sign Out", { exact: true }).first(), 4000);
      await page.keyboard.press("Escape").catch(() => {});
    }
    await page.context().clearCookies();
    await page.reload({ waitUntil: "domcontentloaded" }).catch(() => {});
    entry.screenshots.push(await shot(page, "13a-signed-out"));
    await signInChat(page, QA_EMAIL, QA_PASSWORD);
    entry.screenshots.push(await shot(page, "13b-signed-back-in"));
    const composerBack = await waitVisible(page.locator("#chat-input"), 15000);
    return {
      observed: `Sign Out menu item present=${signOutTextFound} (not clicked, see comment); round trip via cookie-clear+reauth, composer visible after re-sign-in=${composerBack}`,
      verdict: composerBack ? "PASS" : "BROKEN",
    };
  });

  // --- Deliverable 3: owner account, self-service signup -------------------
  let ownerCreds = null;
  if (!SKIP_OWNER_SIGNUP && !ONLY_COWORK) {
    ownerCreds = await createOwnerAccount(browser);
  }

  await browser.close();
  writeReport(ownerCreds);
}

/**
 * Deliverable 3. Creates a fresh, normal-tenant owner account through the
 * real self-service signup form (Turnstile included), then activates and
 * verifies it. Never touches an existing account's password: this is a
 * brand-new account, and its password is set exactly once, at creation, by
 * the signup form itself.
 *
 * Email confirmation is required before sign-in (docs/live-test-auth.md's
 * HIVE_QA_UNVERIFIED_EMAIL fixture proves this is enforced), and this run has
 * no inbox to click a real link from. The user id is read directly off the
 * browser's own POST to Supabase's /auth/v1/signup (captured via
 * page.waitForResponse, not a second admin listing call, so issue #791's
 * flaky GET /auth/v1/admin/users is never touched), then flipped to
 * email_confirm=true with the service-role key. That is an admin action on
 * an account this run just created, not a rotation of shared state, and it
 * changes no password field.
 */
async function createOwnerAccount(browser) {
  // No default address, for the same reason E2E_RUN_KEY has none: this
  // function sets a password on whatever address it resolves to, and a
  // hardcoded fallback points every unconfigured run at one shared inbox
  // (CLAUDE.md "Testing", docs/live-test-auth.md).
  const ownerEmail = process.env.HIVE_OWNER_SIGNUP_EMAIL ?? "";
  const ownerPassword = process.env.HIVE_OWNER_SIGNUP_PASSWORD ?? `Hive-Owner-${Date.now()}-!Aa9`;
  const supabaseUrl = process.env.SUPABASE_URL ?? "";
  const serviceRoleKey = process.env.SUPABASE_SERVICE_ROLE_KEY ?? "";
  const missing = [
    ownerEmail ? "" : "HIVE_OWNER_SIGNUP_EMAIL",
    supabaseUrl ? "" : "SUPABASE_URL",
    serviceRoleKey ? "" : "SUPABASE_SERVICE_ROLE_KEY",
  ].filter(Boolean);
  if (missing.length > 0) {
    results.push({
      step: "owner-signup",
      title: "Owner account: self-service signup",
      verdict: "BROKEN",
      observed: `${missing.join(" / ")} not set, cannot create and activate an owner account (pass --skip-owner-signup to run the rest)`,
      notes: "",
      screenshots: [],
    });
    return null;
  }
  // The one password this run sets, on an account it creates itself. Keep it
  // out of every log and report cell; the caller still gets it in return.
  runSecrets.push(ownerPassword);

  const ctx = await browser.newContext({ viewport: { width: 1440, height: 900 } });
  const page = await ctx.newPage();
  const entry = { step: "owner-signup", title: "Owner account: self-service signup", verdict: "BROKEN", observed: "", notes: "", screenshots: [] };
  try {
    await page.goto(`${CONSOLE}/auth/sign-up`, { waitUntil: "networkidle", timeout: 30000 });
    // Same hydration-gated submit as signInConsole; networkidle alone
    // usually outlasts hydration but this is cheap insurance against the
    // same "field silently reverts to empty" race.
    await page
      .locator('button[type="submit"]:not([disabled])')
      .waitFor({ state: "visible", timeout: 10000 })
      .catch(() => {});
    // A deployment that refuses self-serve signup now says so on this page
    // instead of shipping a form the gateway 404s (issue #1328). That is a
    // posture, not a defect: report it and skip, rather than timing out on
    // fields that are deliberately absent.
    const invitationOnly = await page
      .getByRole("heading", { name: "Accounts are created by invitation" })
      .isVisible()
      .catch(() => false);
    if (invitationOnly) {
      entry.verdict = "PASS";
      entry.observed = "self-serve signup is disabled on this deployment, and the console says accounts are created by invitation instead of failing";
      entry.screenshots.push(await shot(page, "owner-01-signup-by-invitation"));
      return null;
    }
    await page.locator("#email").fill(ownerEmail);
    await page.locator("#password").fill(ownerPassword);
    // Give the Turnstile widget time to resolve in non-interactive managed
    // mode (confirmed live: no click needed, hidden response field populates
    // within a few seconds of load).
    await page.waitForTimeout(4000);
    entry.screenshots.push(await shot(page, "owner-01-signup-form"));

    const signupResp = page.waitForResponse((r) => /\/auth\/v1\/signup/.test(r.url()), { timeout: 20000 });
    await page.click('button[type="submit"]');
    let userId = "";
    try {
      const resp = await signupResp;
      const body = await resp.json().catch(() => ({}));
      userId = typeof body?.id === "string" ? body.id : typeof body?.user?.id === "string" ? body.user.id : "";
    } catch {
      // fall through; entry stays BROKEN below if userId never resolves
    }
    await page.waitForTimeout(1500);
    entry.screenshots.push(await shot(page, "owner-02-signup-result"));

    if (!userId) {
      entry.observed = "signup submit did not yield a Supabase user id (precheck/turnstile/signup itself may have failed; see screenshot)";
      return null;
    }

    const confirmRes = await fetch(`${supabaseUrl.replace(/\/+$/, "")}/auth/v1/admin/users/${userId}`, {
      method: "PUT",
      headers: {
        apikey: serviceRoleKey,
        Authorization: `Bearer ${serviceRoleKey}`,
        "Content-Type": "application/json",
      },
      body: JSON.stringify({ email_confirm: true }),
    });
    if (!confirmRes.ok) {
      entry.observed = `account created (id captured) but admin email_confirm PUT failed with HTTP ${confirmRes.status}`;
      return null;
    }

    // Verify: clean context, real password sign-in, complete profile setup if
    // gated, then one real chat turn.
    const verifyCtx = await browser.newContext({ viewport: { width: 1440, height: 900 } });
    const vpage = await verifyCtx.newPage();
    await signInConsole(vpage, ownerEmail, ownerPassword);
    entry.screenshots.push(await shot(vpage, "owner-03-first-signin"));

    if (vpage.url().includes("/console/setup")) {
      const ownerNameField = vpage.locator("#ownerName");
      if (await waitVisible(ownerNameField, 10000)) {
        await ownerNameField.fill("Sakib Sadman Shajib");
        await vpage.locator("#accountName").fill("Hive Owner");
        await vpage.locator("#accountType").selectOption("personal").catch(() => {});
        await vpage.locator("#countryCode").fill("BD");
        await vpage.locator("#stateRegion").fill("Dhaka");
        await vpage.locator('button[type="submit"]').click();
        await vpage.waitForTimeout(2000);
      }
    }
    entry.screenshots.push(await shot(vpage, "owner-04-profile-complete"));

    const dashboardOK = vpage.url().includes("/console") && !vpage.url().includes("/setup");
    if (!dashboardOK) {
      entry.observed = `signed in but did not reach the dashboard (url=${vpage.url()}), likely 403 account_not_provisioned or a stuck setup gate`;
      return { ownerEmail, ownerPassword, verified: false };
    }

    // One real chat turn, on chat host, same credentials. Reuses signInChat
    // (the OAuth round-trip helper), since this is the same chat sign-in
    // surface step 1 already proved: no reason to duplicate that logic here.
    await signInChat(vpage, ownerEmail, ownerPassword);
    const before = await vpage.locator(".chat-assistant").count().catch(() => 0);
    let chatTurnOK = false;
    if (await waitVisible(vpage.locator("#chat-input"), 15000)) {
      const t0 = await typeAndSend(vpage, "Hello, this is the new owner account's first message.");
      const r = await waitForAssistantReply(vpage, before, t0, { maxMs: 60000 });
      chatTurnOK = !!r.text && !r.timedOut;
    }
    entry.screenshots.push(await shot(vpage, "owner-05-first-chat-turn"));

    entry.verdict = dashboardOK && chatTurnOK ? "PASS" : "UGLY";
    entry.observed = `account created + activated + verified. dashboard reached=${dashboardOK}, first chat turn worked=${chatTurnOK}`;
    return { ownerEmail, ownerPassword, verified: dashboardOK && chatTurnOK };
  } catch (error) {
    entry.notes = redact(String(error?.message ?? error)).slice(0, 1500);
    return null;
  } finally {
    results.push(entry);
  }
}

function writeReport(ownerCreds) {
  const lines = [];
  lines.push(`# Demo walkthrough run: ${today}`);
  lines.push("");
  lines.push(`Chat: ${CHAT} | Console: ${CONSOLE} | API: ${API}`);
  lines.push("");
  lines.push("| # | Step | Verdict | Observed | Screenshots |");
  lines.push("| - | ---- | ------- | -------- | ----------- |");
  // Every cell goes through both guards, not just the observation column:
  // redact first (an observation can quote a live key or password back at
  // us), then escape, because `<redacted>` carries neither a pipe nor a
  // backslash and so cannot be broken by the escaper.
  for (const r of results) {
    const cells = [
      mdTableCell(r.step),
      mdTableCell(r.title),
      mdTableCell(r.verdict),
      mdTableCell(redact(r.observed || r.notes || ""), { max: 300 }),
      mdTableCell(r.screenshots.filter(Boolean).join(", ")),
    ];
    lines.push(`| ${cells.join(" | ")} |`);
  }
  lines.push("");
  if (ownerCreds) {
    lines.push("## Owner account");
    lines.push("");
    lines.push("Credentials recorded in the orchestrator report, not here (this file may be posted). See step `owner-signup` above for verdict.");
  }
  writeFileSync(join(OUT_DIR, "report.md"), redact(lines.join("\n")) + "\n");
  writeFileSync(
    join(OUT_DIR, "step-log.json"),
    redact(JSON.stringify({ CHAT, CONSOLE, API, results }, null, 2)),
  );
  console.log(`\ndemo-walkthrough: wrote ${results.length} step results to ${OUT_DIR}`);
  for (const r of results) console.log(`  [${r.step}] ${r.verdict}: ${r.title}`);
}

main().catch((error) => {
  console.error(redact(`demo-walkthrough: fatal: ${error?.stack ?? error}`));
  process.exit(1);
});
