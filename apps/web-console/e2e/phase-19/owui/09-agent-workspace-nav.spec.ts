import { test, expect } from "@playwright/test";

test.use({ storageState: "e2e/phase-19/owui/.auth/owui-user.json" });

/*
 * The agent workspace as a destination in the shell, rendered natively.
 *
 * This file replaced 09-agent-workspace-launcher.spec.ts, which measured a
 * body-level overlay that loader.js pinned into Open WebUI's header band by
 * viewport measurement. That overlay went when the frontend started building
 * from source (vendor/open-webui) and the agent became a labelled sidebar row.
 *
 * It then asserted, for one release, that /agents rendered an
 * `iframe[title="Agent workspace"]` and reached into it with a frameLocator.
 * Those assertions are inverted here, deliberately and by owner directive: the
 * frame booted apps/agent-console, a second whole application, inside the page,
 * which is why the agent surface never looked like the rest of the product.
 * What is asserted now is that there is no frame at all and that the composer
 * and the task list are this application's own DOM.
 *
 * The list assertion is deliberately about the credential path rather than
 * about rows. The browser holds no credential edge-api accepts, so every call
 * goes through the chat container's own proxy, which resolves the signed-in
 * user's Supabase token server side. A 401 there is exactly the failure mode
 * that design has, so a 401 must fail this test. A 403 must not: it is the
 * Cowork feature gate answering, which can only happen after the principal and
 * its tenant resolved, and the seeded fixture tenant does not hold that gate.
 */

const NAV_ROW = (id: string) => `[data-hive-nav="${id}"]`;
const SIDEBAR = "#sidebar";
const REMOVED_LAUNCHER = "#hive-agent-launcher";
const TASKS_ENDPOINT = "/api/v1/hive/agent/tasks";
const PROOF_DIR = "playwright-report-owui/proof";

test("the sidebar carries labelled Chats, Agents and Knowledge destinations", async ({
  page,
}) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  await page.goto("/");

  await expect(page.locator(SIDEBAR)).toBeVisible();

  for (const [id, label, href] of [
    ["chats", "Chats", "/"],
    ["agents", "Agents", "/agents"],
    // The full path. /knowledge alone is a 404 on this deployment.
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

test("the agent workspace opens inside the shell and frames nothing", async ({
  page,
}) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  await page.goto("/");

  let documentLoads = 0;
  page.on("load", () => {
    documentLoads += 1;
  });

  // Recorded rather than asserted inline so the failure message can name the
  // status, which is the whole diagnostic value of this check.
  let tasksStatus: number | null = null;
  page.on("response", (response) => {
    if (new URL(response.url()).pathname === TASKS_ENDPOINT) {
      tasksStatus = response.status();
    }
  });

  const row = page.locator(`${SIDEBAR} ${NAV_ROW("agents")}`).first();
  await expect(row).toBeVisible();
  await row.click();

  await page.waitForURL((url) => url.pathname === "/agents", { timeout: 15_000 });

  // The shell survives the hop.
  await expect(
    page.locator(SIDEBAR),
    "the sidebar disappeared, so this is still a separate page",
  ).toBeVisible();
  await expect(row).toHaveAttribute("aria-current", "page");

  // The claim this whole change exists to make.
  await expect(
    page.locator("iframe"),
    "the agent surface must not frame anything, on this route or anywhere in it",
  ).toHaveCount(0);

  // The composer is this application's own DOM, and it is the chat composer's
  // container rather than a form that resembles one.
  const composer = page.locator("#hive-agent-instructions");
  await expect(composer).toBeVisible();
  await expect(page.locator("#hive-agent-send")).toBeVisible();
  for (const label of ["Knowledge work", "Coding"]) {
    await expect(
      page.getByText(label, { exact: true }),
      `the ${label} toggle is missing from the composer`,
    ).toBeVisible();
  }

  // The list region renders, whether or not this fixture tenant has any tasks.
  await expect(page.getByRole("heading", { name: "Your tasks" })).toBeAttached();

  await expect
    .poll(() => tasksStatus, {
      message: "the agent surface never called its own backend",
      timeout: 20_000,
    })
    .not.toBeNull();
  expect(
    tasksStatus,
    `GET ${TASKS_ENDPOINT} answered ${tasksStatus}. 401 means the server-side ` +
      "proxy could not resolve the signed-in user's token, which is the one " +
      "failure this design has; 403 is the Cowork gate, which can only answer " +
      "after the principal and its tenant already resolved.",
  ).not.toBe(401);
  expect([200, 403]).toContain(tasksStatus);

  expect(documentLoads, "the hop into the agent must be client-side").toBe(0);
});

test("proof capture: the agent surface, natively, in both palettes", async ({
  page,
}) => {
  await page.setViewportSize({ width: 1440, height: 900 });

  for (const scheme of ["light", "dark"] as const) {
    await page.emulateMedia({ colorScheme: scheme });
    await page.goto("/agents");
    await expect(page.locator("#hive-agent-instructions")).toBeVisible();

    // The DOM evidence travels with the image rather than being asserted only
    // in a log: a screenshot cannot show the absence of a frame on its own.
    const frames = await page.locator("iframe").count();
    expect(frames, "iframe count must be zero in the captured DOM").toBe(0);

    await page.screenshot({
      path: `${PROOF_DIR}/agents-native-${scheme}.png`,
      fullPage: true,
    });
  }

  // The chat composer, from the same build, because its container and send
  // button were extracted into the components the agent composer renders and
  // the extraction has to be provably invisible on this surface.
  await page.emulateMedia({ colorScheme: "light" });
  await page.goto("/");
  await expect(page.locator("#message-input-container")).toBeVisible();
  await page.screenshot({
    path: `${PROOF_DIR}/chat-composer-after.png`,
    fullPage: false,
  });
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
