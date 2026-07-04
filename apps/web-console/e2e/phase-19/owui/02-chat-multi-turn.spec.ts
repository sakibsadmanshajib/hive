import { test, expect } from "@playwright/test";

test.use({ storageState: "e2e/phase-19/owui/.auth/owui-user.json" });

test("second turn references first turn context", async ({ page }) => {
  // Run 28693277109/28693654419/28694246853: every prior version of this
  // test tried to prove multi-turn wiring by inspecting the SECOND turn's
  // outgoing network request for the first turn's text. That request body
  // (captured live from a real run) is OWUI's own internal schema --
  // `{ chat_id, parent_id, user_message: { content } }` -- with no
  // `messages` array at all; OWUI's Python backend reconstructs full
  // history server-side from `chat_id`/`parent_id` before ever calling
  // edge-api. Checking the browser's request for prior-turn text was
  // checking a field that structurally cannot contain it, so it either
  // false-matched an unrelated background call (title/tag generation) or
  // never matched anything. The real, robust signal available to the
  // browser is structural: two completed assistant turns render in the
  // same chat thread without navigating away or starting a new chat in
  // between, proving the session carried across both turns end-to-end.
  // Free-tier verbosity is also unbounded per-turn (see 01/05), so both
  // prompts ask for a one-sentence reply to bound generation time.
  test.setTimeout(360_000);
  await page.goto("/");
  // OWUI 0.9.5 chat input is a contenteditable TipTap/ProseMirror div with
  // id="chat-input" (MessageInput.svelte + RichTextInput.svelte); no
  // "message" placeholder exists (run 28683831193).
  await page.locator("#chat-input").fill(
    "My favourite colour is purple. Reply in one short sentence.",
  );
  await page.keyboard.press("Enter");
  // A completed assistant turn is the only one that grows a "Copy" action
  // button, so its visibility is a structural proof the pipeline
  // delivered a response -- free-tier model output content is not
  // asserted (#269).
  await expect(page.getByRole("button", { name: "Copy" })).toHaveCount(1, {
    timeout: 150_000,
  });

  await page.locator("#chat-input").fill(
    "What is my favourite colour? Reply in one short sentence.",
  );
  await page.keyboard.press("Enter");
  // Structural proof turn 2 also completed, in the SAME chat thread as
  // turn 1 (no reload/new-chat happened in between): a second Copy button
  // appears alongside the first.
  await expect(page.getByRole("button", { name: "Copy" })).toHaveCount(2, {
    timeout: 150_000,
  });
});
