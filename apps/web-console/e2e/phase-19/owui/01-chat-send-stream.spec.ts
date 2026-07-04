import { test, expect } from "@playwright/test";

test.use({ storageState: "e2e/phase-19/owui/.auth/owui-user.json" });

test("chat message streams a response", async ({ page }) => {
  // Run 28693277109: the 45s Copy-button wait timed out outright (no
  // listitem at all) against the project's default 60s test timeout, which
  // left no headroom once real free-tier latency ran long. Run 28693654419
  // and 28694246853: even 90s and 150s waits timed out outright with a
  // trace showing zero assistant content -- an unconstrained prompt lets
  // the model ramble for an unbounded number of tokens, so generation time
  // isn't just "real latency", it's real latency times however verbose
  // free-tier routing decides to be today. Constraining the prompt bounds
  // that (see 05, where a genuinely long multi-table answer took over 150s
  // to finish).
  test.setTimeout(180_000);
  await page.goto("/");
  // OWUI 0.9.5 chat input is a contenteditable TipTap/ProseMirror div with
  // id="chat-input" (MessageInput.svelte + RichTextInput.svelte); no
  // "message" placeholder exists (run 28683831193).
  await page.locator("#chat-input").fill("Reply with only the single word: hello.");
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
  ).toBeVisible({ timeout: 150_000 });
});
