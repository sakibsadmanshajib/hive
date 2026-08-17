import { test, expect } from "@playwright/test";

test.use({ storageState: "e2e/phase-19/owui/.auth/owui-user.json" });

/*
 * The agent workspace as a destination in the shell.
 *
 * This file replaces 09-agent-workspace-launcher.spec.ts, which measured a
 * body-level overlay that loader.js pinned into Open WebUI's header band by
 * viewport measurement. That overlay existed because the pinned image shipped a
 * compiled bundle with no navigation slot. The frontend is built from source
 * now (vendor/open-webui), so the agent workspace is a labelled row in the
 * sidebar and the overlay is deleted.
 *
 * What is asserted here is the property the owner asked for and the launcher
 * could never have: reaching the agent does not leave the shell. The sidebar
 * stays, the origin stays, and no second page opens.
 */

const NAV_ROW = (id: string) => `[data-hive-nav="${id}"]`;
const SIDEBAR = "#sidebar";
const REMOVED_LAUNCHER = "#hive-agent-launcher";

test("the sidebar carries labelled Chats, Agents and Knowledge destinations", async ({
  page,
}) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  await page.goto("/");

  await expect(page.locator(SIDEBAR)).toBeVisible();

  for (const [id, label, href] of [
    ["chats", "Chats", "/"],
    ["agents", "Agents", "/agents"],
    ["knowledge", "Knowledge", "/workspace/knowledge"],
  ] as const) {
    // `.first()` because the same list renders twice, once expanded and once on
    // the collapsed rail, and only one of the two is on screen at a time.
    const row = page.locator(`${SIDEBAR} ${NAV_ROW(id)}`).first();
    await expect(row, `${id} row missing from the sidebar`).toBeVisible();
    await expect(row).toHaveAttribute("href", href);
    // A label, not an unlabelled icon. This is the specific complaint.
    await expect(row).toContainText(label);
  }
});

test("the injected launcher overlay is gone", async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  await page.goto("/");

  await expect(page.locator(SIDEBAR)).toBeVisible();
  await expect(
    page.locator(REMOVED_LAUNCHER),
    "the floating launcher is replaced by the sidebar row, not kept beside it",
  ).toHaveCount(0);
});

test("the agent workspace opens inside the shell, with the sidebar still there", async ({
  page,
}) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  await page.goto("/");

  let documentLoads = 0;
  page.on("load", () => {
    documentLoads += 1;
  });

  const row = page.locator(`${SIDEBAR} ${NAV_ROW("agents")}`).first();
  await expect(row).toBeVisible();
  await row.click();

  await page.waitForURL((url) => url.pathname === "/agents", { timeout: 15_000 });

  // The whole point: the shell survives the hop.
  await expect(
    page.locator(SIDEBAR),
    "the sidebar disappeared, so this is still a separate page",
  ).toBeVisible();
  await expect(row).toHaveAttribute("aria-current", "page");

  const panel = page.locator('iframe[title="Agent workspace"]');
  await expect(panel).toBeVisible();

  // The panel is the real agent workspace and it is running embedded, so it
  // draws no second brand row over the shell's own chrome.
  const frame = page.frameLocator('iframe[title="Agent workspace"]');
  await expect(
    frame.getByRole("heading", { name: "Give the agent a task" }),
  ).toBeVisible({ timeout: 30_000 });
  await expect(
    frame.locator('[data-hv-embedded="1"]'),
    "the embedded panel should know it is embedded",
  ).toHaveCount(1);

  expect(documentLoads, "the hop into the agent must be client-side").toBe(0);
});

test("the collapsed rail keeps every destination", async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  await page.goto("/");

  await expect(page.locator(SIDEBAR)).toBeVisible();

  // Collapse through Open WebUI's own control rather than by writing its store:
  // the assertion is about what a person sees after pressing it.
  await page.getByLabel("Close Sidebar").first().click();
  await expect(page.getByLabel("Open Sidebar").first()).toBeVisible();

  for (const id of ["chats", "agents", "knowledge"]) {
    const row = page.locator(NAV_ROW(id)).first();
    await expect(row, `${id} vanished when the sidebar collapsed`).toBeVisible();
    // Collapsed rows carry no visible label, so the accessible name is the only
    // thing naming them and it must not be empty.
    await expect(row).toHaveAttribute("aria-label", /\S/);
  }
});
