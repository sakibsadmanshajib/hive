import { test, expect } from "@playwright/test";

test.use({ storageState: "e2e/phase-19/owui/.auth/owui-user.json" });

test("second turn references first turn context", async ({ page }) => {
  await page.goto("/");
  // OWUI 0.9.5 chat input is a contenteditable TipTap/ProseMirror div with
  // id="chat-input" (MessageInput.svelte + RichTextInput.svelte); no
  // "message" placeholder exists (run 28683831193).
  await page.locator("#chat-input").fill("My favourite colour is purple.");
  await page.keyboard.press("Enter");
  await page.waitForResponse(
    (r) => r.url().includes("/v1/chat/completions") && r.ok(),
    { timeout: 20_000 },
  );

  await page.locator("#chat-input").fill("What is my favourite colour?");
  await page.keyboard.press("Enter");
  await expect(page.locator('[data-role="assistant"]').last()).toContainText(
    /purple/i,
    { timeout: 20_000 },
  );
});
