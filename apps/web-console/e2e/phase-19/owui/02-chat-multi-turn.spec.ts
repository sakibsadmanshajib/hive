import { test, expect } from "@playwright/test";

test.use({ storageState: "e2e/phase-19/owui/.auth/owui-user.json" });

test("second turn references first turn context", async ({ page }) => {
  // Run 28693277109: attempt 1 failed at 1.1m (the 60s response wait for
  // turn 1 timed out outright), attempt 2 failed at exactly the old 120s
  // test-level cap while still waiting on turn 2's request. Two full
  // sequential model round trips do not fit in 120s of real free-tier
  // latency -- this needs a much larger ceiling, and `waitForResponse`
  // resolves at response-header time (not full stream completion), so the
  // Copy-button wait after it still needs a full round-trip-sized budget,
  // not just a short DOM-settle margin. Run 28693654419: even a 90s
  // response wait timed out outright on turn 1 (same free-tier
  // slow/cold-start variance seen in 01), so both round trips need the
  // same 150s-class headroom 01 and 05 needed.
  test.setTimeout(420_000);
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
    { timeout: 150_000 },
  );
  // `[data-role="assistant"]` never matches anything real (see 01); a
  // completed assistant turn is the only one that grows a "Copy" button.
  await expect(
    page.getByRole("listitem").last().getByRole("button", { name: "Copy" }),
  ).toBeVisible({ timeout: 90_000 });

  // OWUI also fires background /api/chat/completions calls (title/tag
  // generation) around the same time as a real turn -- run 28692939239's
  // 14s retry captured one with `messages: []`, and run 28693654419
  // captured one with a non-empty but unrelated `messages` array (a
  // title/tag prompt that embeds the raw conversation text in a field
  // Playwright sees when it stringifies the whole payload, even though
  // `messages` itself carries no such text). A whole-payload substring
  // match catches both false positives. Only a message whose own content
  // contains the just-typed text identifies the real second-turn request.
  const secondTurnText = "What is my favourite colour?";
  const [secondReq] = await Promise.all([
    // This resolves at request-send time, not response time, so 30s is
    // ample even under real latency -- it only has to observe the browser
    // dispatching the fetch, which happens immediately after Enter.
    page.waitForRequest(
      (r) => {
        if (!r.url().includes("/api/chat/completions") || r.method() !== "POST") {
          return false;
        }
        try {
          const data = r.postDataJSON() as { messages?: { content?: unknown }[] };
          return (data.messages ?? []).some(
            (m) => typeof m?.content === "string" && m.content.includes(secondTurnText),
          );
        } catch {
          return false;
        }
      },
      { timeout: 30_000 },
    ),
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
  // `waitForRequest` above resolved when the browser sent turn 2's request,
  // not when the model finished replying -- this still needs the full
  // round-trip budget, same as turn 1's Copy wait.
  await expect(
    page.getByRole("listitem").last().getByRole("button", { name: "Copy" }),
  ).toBeVisible({ timeout: 90_000 });
});
