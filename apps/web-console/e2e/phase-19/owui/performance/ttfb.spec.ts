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
  const budget = budgetMs("OWUI_TTFB_BUDGET_MS", 8000);
  await page.goto("/");
  // OWUI 0.9.5 chat input is a contenteditable TipTap/ProseMirror div with
  // id="chat-input" (MessageInput.svelte + RichTextInput.svelte); no
  // "message" placeholder exists (run 28683831193).
  await page.locator("#chat-input").fill("one word.");

  // Bind the response wait to the specific request triggered by this
  // Enter keystroke. The previous URL+ok() predicate could latch onto
  // an unrelated /v1/chat/completions completion from a different
  // workspace tab and report a falsely low TTFB.
  const reqPromise = page.waitForRequest(
    (r) =>
      r.url().includes("/v1/chat/completions") && r.method() === "POST",
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
