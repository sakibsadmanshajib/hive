import { test, expect } from "@playwright/test";

test.use({ storageState: "e2e/phase-19/owui/.auth/owui-user.json" });

test("switch model and confirm subsequent request goes to it", async ({
  page,
}) => {
  await page.goto("/");
  // OWUI 0.9.5 has no data-testid on the model trigger; the real control is
  // the "Select a model" button (run 28684729556 failure snapshot).
  await page.getByRole("button", { name: /select a model/i }).click();
  // route-groq-fast is one of the routes configured in
  // deploy/litellm/config.yaml / .env.ci for the nightly run.
  await page.getByRole("option", { name: /route-groq-fast/i }).click();
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
  expect(body.model).toMatch(/route-groq-fast/i);
});
