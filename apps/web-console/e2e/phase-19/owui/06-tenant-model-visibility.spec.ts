import { test, expect } from "@playwright/test";

test.use({ storageState: "e2e/phase-19/owui/.auth/owui-user.json" });

test("user sees only models granted to tenant group", async ({ page }) => {
  await page.goto("/");
  // OWUI 0.9.5 has no data-testid on the model trigger; the real control is
  // a button that reads "Select a model" only when no model is picked yet.
  // The e2e tenant has a default model configured, so the button instead
  // reads "Selected model: <name>" (run 28688154897 failure snapshot) --
  // match both forms without also matching the neighbouring "Add Model"
  // button.
  await page.getByRole("button", { name: /^select(ed)? .*model/i }).click();
  const options = await page.getByRole("option").all();
  // Guard against an empty option list (e.g. catalog not seeded) — that
  // would vacuously pass the loop below and hide a real visibility bug.
  expect(options.length).toBeGreaterThan(0);
  for (const opt of options) {
    const text = (await opt.textContent()) ?? "";
    // grok is intentionally NOT granted to the test tenant group
    expect(text).not.toMatch(/grok/i);
  }
});
