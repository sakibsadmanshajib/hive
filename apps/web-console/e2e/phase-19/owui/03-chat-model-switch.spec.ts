import { test, expect } from "@playwright/test";

test.use({ storageState: "e2e/phase-19/owui/.auth/owui-user.json" });

test("switch model and confirm subsequent request goes to it", async ({
  page,
}) => {
  await page.goto("/");
  await page.getByTestId("model-selector").click();
  await page.getByRole("option", { name: /claude-3-haiku/i }).click();
  // OWUI 0.9.5 chat input is a contenteditable TipTap/ProseMirror div with
  // id="chat-input" (MessageInput.svelte + RichTextInput.svelte); no
  // "message" placeholder exists (run 28683831193).
  await page.locator("#chat-input").fill("hi");
  const [req] = await Promise.all([
    page.waitForRequest(
      (r) =>
        r.url().includes("/v1/chat/completions") && r.method() === "POST",
    ),
    page.keyboard.press("Enter"),
  ]);
  const body = req.postDataJSON() as { model?: string };
  expect(body.model).toMatch(/claude/i);
});
