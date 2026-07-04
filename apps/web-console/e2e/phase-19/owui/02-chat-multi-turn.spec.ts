import { test, expect } from "@playwright/test";

test.use({ storageState: "e2e/phase-19/owui/.auth/owui-user.json" });

test("second turn references first turn context", async ({ page }) => {
  // Two sequential real-model waits (45s each) can exceed the project's
  // default 60s test timeout in the worst case; this test needs its own
  // budget rather than shrinking either wait.
  test.setTimeout(120_000);
  await page.goto("/");
  // OWUI 0.9.5 chat input is a contenteditable TipTap/ProseMirror div with
  // id="chat-input" (MessageInput.svelte + RichTextInput.svelte); no
  // "message" placeholder exists (run 28683831193).
  await page.locator("#chat-input").fill("My favourite colour is purple.");
  await page.keyboard.press("Enter");
  // The browser calls OWUI's OWN backend route ("/api/chat/completions"),
  // never edge-api's "/v1/chat/completions" directly -- OWUI's backend
  // proxies to edge-api server-side. Matching "/v1/..." here always timed
  // out regardless of backend health (run 28691819361). Real upstream
  // (free-tier OpenRouter/Groq) latency can also run well past 20s, so the
  // wait is generous.
  await page.waitForResponse(
    (r) => r.url().includes("/api/chat/completions") && r.ok(),
    { timeout: 45_000 },
  );

  await page.locator("#chat-input").fill("What is my favourite colour?");
  await page.keyboard.press("Enter");
  await expect(page.locator('[data-role="assistant"]').last()).toContainText(
    /purple/i,
    { timeout: 45_000 },
  );
});
