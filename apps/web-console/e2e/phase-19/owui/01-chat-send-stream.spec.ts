import { test, expect } from "@playwright/test";

test.use({ storageState: "e2e/phase-19/owui/.auth/owui-user.json" });

test("chat message streams a response", async ({ page }) => {
  // Run 28693277109: the 45s Copy-button wait timed out outright (no
  // listitem at all) against the project's default 60s test timeout, which
  // left no headroom once real free-tier latency ran long. This test needs
  // its own budget, same pattern as 02/05.
  test.setTimeout(120_000);
  await page.goto("/");
  // OWUI 0.9.5 chat input is a contenteditable TipTap/ProseMirror div with
  // id="chat-input" (MessageInput.svelte + RichTextInput.svelte); no
  // "message" placeholder exists (run 28683831193).
  await page.locator("#chat-input").fill("Say hello.");
  await page.keyboard.press("Enter");
  // `[data-role="assistant"]` never matches anything: OWUI's Messages.svelte
  // renders the conversation as role="log" > listitem, with no data-role
  // attribute anywhere in its component tree (confirmed against source;
  // every run showed "element(s) not found", never a text mismatch, even
  // once LiteLLM was confirmed returning real 200s). A completed assistant
  // turn is the only one that grows a "Copy" action button, so its
  // visibility is a structural proof the pipeline delivered a response --
  // free-tier model output content is not asserted (#269).
  await expect(
    page.getByRole("listitem").last().getByRole("button", { name: "Copy" }),
  ).toBeVisible({ timeout: 90_000 });
});
