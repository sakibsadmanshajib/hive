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
  // The expected answer word is deliberately absent from the prompt. The
  // assertion below reads the last listitem, and if the assistant turn never
  // rendered at all that locator resolves to the user's own message, so a
  // prompt carrying the answer would pass on its own echo.
  await page
    .locator("#chat-input")
    .fill("What colour is a banana? Reply with only the single colour word.");
  // Run 31042840516: this first attempt burned 150s and reported as flake
  // while the browser never sent anything. Open WebUI dropped the submit
  // (the trace shows the prompt still sitting in the input at timeout, zero
  // requests after the Enter, and edge-api saw no /v1/chat/completions until
  // the retry two and a half minutes later), so the wait below was waiting
  // for a reply to a message that was never sent. Proving the submit left
  // the browser separates "we never asked" from "the model was slow" in
  // seconds instead of minutes -- deliberately NOT a longer timeout or a
  // resend, both of which would hide it again.
  const chatRequest = page.waitForRequest(
    (request) =>
      request.method() === "POST" &&
      request.url().includes("/api/chat/completions"),
    { timeout: 15_000 },
  );
  await page.keyboard.press("Enter");
  await chatRequest;
  // `[data-role="assistant"]` never matches anything: OWUI's Messages.svelte
  // renders the conversation as role="log" > listitem, with no data-role
  // attribute anywhere in its component tree (confirmed against source;
  // every run showed "element(s) not found", never a text mismatch, even
  // once LiteLLM was confirmed returning real 200s). A completed assistant
  // turn is the only one that grows a "Copy" action button, so waiting on it
  // proves the turn reached a terminal state.
  //
  // It does NOT prove a response was delivered, and the comment that used to
  // claim it was "structural proof the pipeline delivered a response" is why
  // three consecutive nightlies (31042840516, 30989146214, 30953435583)
  // reported this spec as passing on retry while zero chat completions
  // succeeded. An error bubble reaches the same terminal state and grows the
  // same Copy button, so a 404 or a 429 read exactly like a streamed answer.
  // The content assertion is the proof. It gets a short timeout because the
  // turn has already finished by then, so "finished carrying no answer" fails
  // in seconds and the failure snapshot shows what the bubble actually said.
  const assistantTurn = page.getByRole("listitem").last();
  await expect(assistantTurn.getByRole("button", { name: "Copy" })).toBeVisible({
    timeout: 150_000,
  });
  await expect(assistantTurn).toContainText(/yellow/i, { timeout: 10_000 });
});
