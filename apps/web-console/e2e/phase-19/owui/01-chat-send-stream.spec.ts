import { test, expect } from "@playwright/test";

test.use({ storageState: "e2e/phase-19/owui/.auth/owui-user.json" });

test("chat message streams a response", async ({ page }) => {
  await page.goto("/");
  // OWUI 0.9.5 chat input is a contenteditable TipTap/ProseMirror div with
  // id="chat-input" (MessageInput.svelte + RichTextInput.svelte); no
  // "message" placeholder exists (run 28683831193).
  await page.locator("#chat-input").fill("Say hello.");
  await page.keyboard.press("Enter");
  const reply = page.locator('[data-role="assistant"]').last();
  // Real upstream (free-tier OpenRouter/Groq) latency can run well past
  // 20s once auth/routing actually succeed end-to-end (run 28691819361:
  // no assistant bubble rendered within 20s even though edge-api and
  // LiteLLM both logged a real 200 for the request).
  await expect(reply).toContainText(/.+/, { timeout: 45_000 });
});
