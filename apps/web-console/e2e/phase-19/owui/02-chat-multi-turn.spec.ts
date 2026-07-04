import { test, expect } from "@playwright/test";

test.use({ storageState: "e2e/phase-19/owui/.auth/owui-user.json" });

test("second turn references first turn context", async ({ page }) => {
  // Two sequential real-model waits (45s each) can exceed the project's
  // default 60s test timeout in the worst case; this test needs its own
  // budget rather than shrinking either wait.
  test.setTimeout(120_000);
  await page.goto("/");
  // OWUI 0.9.5 chat input is a contenteditable TipTap/ProseMirror div with
  // id="chat-input" (MessageInput.svelte + RichTextInput.svelte); no
  // "message" placeholder exists (run 28683831193).
  await page.locator("#chat-input").fill("My favourite colour is purple.");
  await page.keyboard.press("Enter");
  // The browser calls OWUI's OWN backend route ("/api/chat/completions"),
  // never edge-api's "/v1/chat/completions" directly -- OWUI's backend
  // proxies to edge-api server-side. Matching "/v1/..." here always timed
  // out regardless of backend health (run 28691819361). Real upstream
  // (free-tier OpenRouter/Groq) latency can also run well past 20s, so the
  // wait is generous.
  await page.waitForResponse(
    (r) => r.url().includes("/api/chat/completions") && r.ok(),
    { timeout: 60_000 },
  );
  // `[data-role="assistant"]` never matches anything real (see 01); a
  // completed assistant turn is the only one that grows a "Copy" button.
  await expect(
    page.getByRole("listitem").last().getByRole("button", { name: "Copy" }),
  ).toBeVisible({ timeout: 45_000 });

  // OWUI also fires background /api/chat/completions calls (title/tag
  // generation) around the same time as a real turn, with an empty or
  // unrelated `messages` array -- a plain URL+method predicate can catch
  // one of those instead of the real second-turn request (run 28692939239:
  // a 14s retry captured `messages: []`). Requiring the just-typed text to
  // appear in the payload is what actually identifies the real request.
  const secondTurnText = "What is my favourite colour?";
  const [secondReq] = await Promise.all([
    page.waitForRequest((r) => {
      if (!r.url().includes("/api/chat/completions") || r.method() !== "POST") {
        return false;
      }
      try {
        return JSON.stringify(r.postDataJSON()).includes(secondTurnText);
      } catch {
        return false;
      }
    }),
    (async () => {
      await page.locator("#chat-input").fill(secondTurnText);
      await page.keyboard.press("Enter");
    })(),
  ]);
  // Real signal per #269: assert the second turn's OUTGOING request
  // carries the first turn's message, proving multi-turn context is wired
  // end-to-end. Free-tier models cannot reliably recall/repeat "purple" in
  // their own reply, so that is no longer asserted -- only that a second
  // assistant turn completes at all.
  const body = secondReq.postDataJSON() as { messages?: { content?: unknown }[] };
  expect(JSON.stringify(body.messages ?? [])).toMatch(/purple/i);
  await expect(
    page.getByRole("listitem").last().getByRole("button", { name: "Copy" }),
  ).toBeVisible({ timeout: 45_000 });
});
