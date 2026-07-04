import { test, expect } from "@playwright/test";

test.use({ storageState: "e2e/phase-19/owui/.auth/owui-user.json" });

function budgetMs(envName: string, fallback: number): number {
  const raw = process.env[envName];
  if (raw === undefined || raw === "") return fallback;
  const parsed = Number(raw);
  if (!Number.isFinite(parsed) || parsed <= 0) {
    throw new Error(`${envName} must be a positive finite number, got "${raw}"`);
  }
  return parsed;
}

test("first-token latency under budget", async ({ page }) => {
  // Run 28695213565: both attempts timed out at 60s inside
  // `page.waitForRequest` itself -- never even a slow match, no match at
  // all. The browser calls OWUI's OWN backend route ("/api/chat/
  // completions"), never edge-api's "/v1/chat/completions" directly (see
  // 02-chat-multi-turn.spec.ts); matching "/v1/..." here can never
  // observe a real request. Free-tier OpenRouter/Groq queueing also makes
  // a strict budget meaningless in CI, so OWUI_TTFB_BUDGET_MS lets the
  // nightly workflow set a generous ceiling while keeping a strict
  // default for local/dev runs.
  test.setTimeout(90_000);
  const budget = budgetMs("OWUI_TTFB_BUDGET_MS", 8000);
  await page.goto("/");
  // OWUI 0.9.5 chat input is a contenteditable TipTap/ProseMirror div with
  // id="chat-input" (MessageInput.svelte + RichTextInput.svelte); no
  // "message" placeholder exists (run 28683831193).
  await page.locator("#chat-input").fill("one word.");

  // Bind the response wait to the specific request triggered by this
  // Enter keystroke. A plain URL+ok() predicate could latch onto an
  // unrelated /api/chat/completions completion (title/tag generation)
  // from the same page and report a falsely low TTFB.
  const reqPromise = page.waitForRequest(
    (r) =>
      r.url().includes("/api/chat/completions") && r.method() === "POST",
    { timeout: 60_000 },
  );
  const start = Date.now();
  await page.keyboard.press("Enter");
  const req = await reqPromise;
  const resp = await page.waitForResponse((r) => r.request() === req);
  expect(resp.ok()).toBe(true);
  const ttfb = Date.now() - start;
  // eslint-disable-next-line no-console
  console.log(`ttfb_ms=${ttfb}`);
  expect(ttfb).toBeLessThan(budget);
});
