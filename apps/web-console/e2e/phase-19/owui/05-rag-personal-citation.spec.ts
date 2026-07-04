import { test, expect } from "@playwright/test";
import { readFileSync, existsSync } from "node:fs";

test.use({ storageState: "e2e/phase-19/owui/.auth/owui-user.json" });

const FIXTURE = "e2e/phase-19/owui/fixtures/expected-citations.json";

test("ask grounded question and receive citation", async ({ page }) => {
  if (!existsSync(FIXTURE)) {
    test.skip(true, "expected-citations.json fixture not present");
  }
  const expected = JSON.parse(readFileSync(FIXTURE, "utf8")) as {
    prompt: string;
    anchor: string;
  };

  await page.goto("/");
  // OWUI 0.9.5 chat input is a contenteditable TipTap/ProseMirror div with
  // id="chat-input" (MessageInput.svelte + RichTextInput.svelte); no
  // "message" placeholder exists (run 28683831193).
  await page.locator("#chat-input").fill(expected.prompt);
  await page.keyboard.press("Enter");
  const reply = page.locator('[data-role="assistant"]').last();
  // Real upstream (free-tier OpenRouter/Groq) latency can run well past
  // 30s once auth/routing actually succeed end-to-end (run 28691819361).
  await expect(reply).toContainText(new RegExp(expected.anchor, "i"), {
    timeout: 45_000,
  });
  await expect(page.getByText(/policy\.pdf/i)).toBeVisible();
});
