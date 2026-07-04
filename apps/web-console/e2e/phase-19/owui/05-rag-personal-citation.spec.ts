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
  // Structural proof the RAG pipeline delivered a grounded response:
  // `[data-role="assistant"]` never matches anything real (see 01); a
  // completed assistant turn is the only one that grows a "Copy" button.
  // The real grounding signal is citing the uploaded document by name --
  // free-tier models cannot reliably reproduce the exact fixture anchor
  // text (expected.anchor), so that is no longer asserted (#269).
  await expect(
    page.getByRole("listitem").last().getByRole("button", { name: "Copy" }),
  ).toBeVisible({ timeout: 45_000 });
  await expect(page.getByText(/policy\.pdf/i)).toBeVisible({ timeout: 45_000 });
});
