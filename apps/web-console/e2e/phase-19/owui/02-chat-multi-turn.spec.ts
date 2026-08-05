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
  //
  // Run 28694759536: a page-wide Copy-button count never reached 2 --
  // OWUI's message action bar (Edit/Copy/Read Aloud/...) only renders on
  // the LAST rendered listitem, not on every historical turn (confirmed
  // in the failure snapshot: turn 1's older listitem has no action
  // buttons at all once turn 2 lands). `getByRole("listitem").last()` is
  // the correct scope, same as 01 and 05 -- checking it twice, once per
  // turn, is the structural proof both turns completed in one thread.
  test.setTimeout(360_000);
  await page.goto("/");
  // OWUI 0.9.5 chat input is a contenteditable TipTap/ProseMirror div with
  // id="chat-input" (MessageInput.svelte + RichTextInput.svelte); no
  // "message" placeholder exists (run 28683831193).
  await page.locator("#chat-input").fill(
    "My favourite colour is purple. Reply in one short sentence.",
  );
  // Run 31042840516: same silent dropped submit as 01 (see the comment
  // there). Prove the request left the browser so "we never asked" fails in
  // seconds instead of consuming the 150s reply budget.
  const firstTurnRequest = page.waitForRequest(
    (request) =>
      request.method() === "POST" &&
      request.url().includes("/api/chat/completions"),
    { timeout: 15_000 },
  );
  await page.keyboard.press("Enter");
  await firstTurnRequest;
  // A completed assistant turn is the only one that grows a "Copy" action
  // button, so this waits for turn 1 to reach a terminal state. It is only a
  // gate before turn 2, not evidence turn 1 was answered: an error bubble
  // grows the same button. Turn 2 carries the content assertion, and a turn 1
  // that failed cannot satisfy it.
  await expect(
    page.getByRole("listitem").last().getByRole("button", { name: "Copy" }),
  ).toBeVisible({ timeout: 150_000 });

  await page.locator("#chat-input").fill(
    "What is my favourite colour? Reply with only the single colour word.",
  );
  await page.keyboard.press("Enter");
  // The Copy button shows turn 2 completed in the SAME chat thread as turn 1
  // (no reload/new-chat happened in between). On its own that is not proof
  // anything was delivered: an error bubble grows the same button, which is
  // how three consecutive nightlies (31042840516, 30989146214, 30953435583)
  // reported this spec as passing on retry while every chat completion in
  // the run failed with a 404 or a 429. The content assertion is the proof,
  // and it doubles as the real multi-turn signal: the colour word appears
  // nowhere in the turn 2 prompt itself, so the model can only produce it
  // from turn 1 history. Short timeout, because the turn has already finished.
  const secondTurn = page.getByRole("listitem").last();
  await expect(secondTurn.getByRole("button", { name: "Copy" })).toBeVisible({
    timeout: 150_000,
  });
  await expect(secondTurn).toContainText(/purple/i, { timeout: 10_000 });
});
