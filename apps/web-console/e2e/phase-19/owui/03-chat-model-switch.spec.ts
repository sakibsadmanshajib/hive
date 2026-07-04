import { test, expect } from "@playwright/test";

test.use({ storageState: "e2e/phase-19/owui/.auth/owui-user.json" });

test("switch model and confirm subsequent request goes to it", async ({
  page,
}) => {
  await page.goto("/");
  // OWUI 0.9.5 has no data-testid on the model trigger; the real control is
  // a button that reads "Select a model" only when no model is picked yet.
  // The e2e tenant has a default model configured, so the button instead
  // reads "Selected model: <name>" (run 28688154897 failure snapshot) --
  // match both forms without also matching the neighbouring "Add Model"
  // button.
  await page.getByRole("button", { name: /^select(ed)? .*model/i }).click();
  // OWUI's model list comes from edge-api's catalog, which exposes Hive
  // model ALIASES (model_aliases.alias_id, e.g. "hive-fast"), never the
  // underlying LiteLLM route name ("route-groq-fast") that the alias maps
  // to (supabase/migrations/20260331_02_routing_policy.sql). The alias id
  // is also exactly what OWUI sends back as `model` in the outgoing
  // request body (run 28688900087 failure snapshot showed the trigger
  // button read "Selected model: hive-auto", not a route name).
  await page.getByRole("option", { name: /hive-fast/i }).click();
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
  expect(body.model).toMatch(/hive-fast/i);
});
