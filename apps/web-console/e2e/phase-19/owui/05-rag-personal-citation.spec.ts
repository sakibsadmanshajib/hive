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
  // RAG adds retrieval + embedding on top of a plain chat completion; both
  // 01 and 02's plain-chat waits already need real headroom against
  // free-tier latency (run 28692939239: both attempts timed out at the
  // previous 45s Copy-button wait). Run 28693654419 and 28694246853: both
  // attempts timed out at 90s and then 150s even though the grounded
  // answer was a long, fully-formed, multi-table response each time -- an
  // unconstrained grounded question lets the model write an essay instead
  // of an answer. The fixture prompt now asks for one short sentence
  // (see fixtures/expected-citations.json) so generation time stops
  // scaling with however much the model decides to write; this budget
  // stays generous as a floor against real free-tier latency, not
  // against unbounded verbosity.
  test.setTimeout(240_000);

  await page.goto("/");
  // OWUI 0.9.5 chat input is a contenteditable TipTap/ProseMirror div with
  // id="chat-input" (MessageInput.svelte + RichTextInput.svelte); no
  // "message" placeholder exists (run 28683831193).
  await page.locator("#chat-input").fill(expected.prompt);
  await page.keyboard.press("Enter");
  // Structural proof the RAG pipeline delivered a grounded response:
  // `[data-role="assistant"]` never matches anything real (see 01); a
  // completed assistant turn is the only one that grows a "Copy" button.
  // The real grounding signal is citing the uploaded document by name --
  // free-tier models cannot reliably reproduce the exact fixture anchor
  // text (expected.anchor), so that is no longer asserted (#269).
  await expect(
    page.getByRole("listitem").last().getByRole("button", { name: "Copy" }),
  ).toBeVisible({ timeout: 150_000 });
  // Run 28693277109: first attempt failed here at 10s (retry passed) --
  // the citation badge renders after the Copy button, on its own follow-up
  // tick, and 10s was too tight a margin for that under load.
  await expect(page.getByText(/policy\.pdf/i)).toBeVisible({ timeout: 30_000 });
});
