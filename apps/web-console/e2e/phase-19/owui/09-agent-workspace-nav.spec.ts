import { test, expect, type Page } from "@playwright/test";

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
 * Those assertions were inverted, deliberately and by owner directive: the
 * frame booted apps/agent-console, a second whole application, inside the page,
 * which is why the agent surface never looked like the rest of the product.
 *
 * Issue #1501 then retired /agents itself. The file keeps its name and its
 * subject, and the assertions moved with the surface rather than dying with
 * the route: the agent surface is a MODE of the chat composer (D-045), so what
 * is asserted now is that there is no frame at all, that the composer is this
 * application's own DOM, and that switching to Cowork changes what the next
 * message does without navigating.
 *
 * The task-list assertions are gone, and their absence is deliberate rather
 * than an oversight. /agents listed tasks on load and those assertions checked
 * the painted rows against the proxy's response. The composer lists nothing on
 * load, because a run IS a conversation and the conversation list is the task
 * list, so there is no load-time call left to assert. Re-adding an equivalent
 * would mean submitting a real run into a real sandbox on every nightly.
 *
 * `/agent-workspace` is a different surface and is NOT what #1501 removed: it
 * already 404'd before this change.
 */

const NAV_ROW = (id: string) => `[data-hive-nav="${id}"]`;
const SIDEBAR = "#sidebar";
const REMOVED_LAUNCHER = "#hive-agent-launcher";
/*
 * Deliberately NOT inside playwright-report-owui. The HTML reporter owns that
 * folder and clears it before writing the report at the end of the run, which
 * would delete these images after they were captured and leave an absence that
 * looks exactly like a test that never ran. The workflow uploads this
 * directory separately.
 */
const PROOF_DIR = "playwright-report-owui-proof";


/*
 * Put the sidebar in the state the assertion is about, rather than assuming it.
 *
 * Both nav variants are always in the DOM: an icon-only rail whose name lives
 * on `aria-label`, and an expanded row that renders the same string as text.
 * Only one is visible at a time, and `.first()` resolves to the rail either
 * way, so a text assertion written against `.first()` reads the icon and finds
 * "".
 *
 * This fixture's sidebar starts collapsed, which is the other half: the
 * expanded-state assertions never had their precondition, and the collapsed
 * test waited forever for a "Close Sidebar" control that is only present when
 * the sidebar is open. Neither had ever actually run before the sign-in
 * readiness fix in this branch, so neither had ever been observed failing.
 */
async function setSidebar(page: Page, state: "expanded" | "collapsed") {
  const opener = page.getByLabel("Open Sidebar").first();
  const closer = page.getByLabel("Close Sidebar").first();
  const toPress = state === "expanded" ? opener : closer;
  if (await toPress.isVisible().catch(() => false)) {
    await toPress.click();
  }
  // The opposite control is what proves the new state, through Open WebUI's
  // own chrome rather than through its store.
  await expect(state === "expanded" ? closer : opener).toBeVisible();
}

/** The nav row a person can actually see, of the two the DOM always holds. */
const visibleNavRow = (page: Page, id: string) =>
  page.locator(`${NAV_ROW(id)}:visible`).first();

test("the sidebar carries its labelled destinations", async ({
  page,
}) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  await page.goto("/");

  await expect(page.locator(SIDEBAR)).toBeVisible();
  await setSidebar(page, "expanded");

  for (const [id, label, href] of [
    ["chats", "Chats", "/"],
    // No Agents row. There never was one after #944, and issue #1501 deleted
    // the unlinked /agents page this tuple pointed at, so asserting it here
    // would assert a destination the product does not have.
    // The full path. /knowledge alone is a 404 on this deployment.
    ["knowledge", "Knowledge", "/workspace/knowledge"],
  ] as const) {
    // The visible one of the two, not `.first()`: see setSidebar above.
    const row = visibleNavRow(page, id);
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

test("the agent surface is a mode of the composer and frames nothing", async ({
  page,
}) => {
  /*
   * This test used to drive /agents. Issue #1501 retired that page, and the
   * gate follows the surface into the composer rather than dying with the
   * route: the claims worth keeping were never about the URL, they were that
   * the agent surface frames nothing and is this application's own DOM.
   *
   * What did NOT survive the move, stated plainly so a thinner test is not
   * mistaken for a passing one. The old version polled
   * GET /api/v1/hive/agent/tasks and asserted the painted rows against the
   * response, because /agents listed tasks on load. The composer lists nothing
   * on load: a run IS a conversation now, so the conversation list is the task
   * list. Asserting that call here would mean submitting a real run into a
   * real sandbox on every nightly, a cost this check does not need to pay to
   * make its point. The submit path is covered by the composer unit suite and
   * by the visual proof on the pull request instead.
   */
  await page.setViewportSize({ width: 1440, height: 900 });
  await page.goto("/");

  let documentLoads = 0;
  page.on("load", () => {
    documentLoads += 1;
  });

  await expect(page.locator(SIDEBAR)).toBeVisible();

  // The composer is this application's own DOM, and switching to Cowork is a
  // mode change rather than a hop to somewhere else (D-045).
  const composer = page.locator("#chat-input");
  await expect(composer).toBeVisible();

  const cowork = page
    .getByRole("radio", { name: "Cowork", exact: true })
    .first();
  await expect(
    cowork,
    "the Cowork mode toggle is missing from the composer",
  ).toBeVisible();
  await cowork.click();

  // The claim this whole change exists to make.
  await expect(
    page.locator("iframe"),
    "the agent surface must not frame anything",
  ).toHaveCount(0);

  // An iframe count of zero is also true of a page that rendered nothing at
  // all, so every assertion below is a positive one: the surface has to paint
  // and work, not merely fail to contain a frame.
  const packs = page.locator("[data-hive-composer-pack]");
  await expect(packs, "Cowork mode paints no pack selector").toBeVisible();
  for (const [pack, label] of [
    ["knowledge-work-pack", "Knowledge work"],
    ["coding-pack", "Coding"],
  ] as const) {
    const segment = packs.locator(`[data-hive-pack="${pack}"]`);
    await expect(segment, `the ${label} segment is missing`).toBeVisible();
    await expect(segment).toContainText(label);
    await segment.click();
    // The selection reaches the store the submit path reads. Without this the
    // segments could be inert and every assertion above would still pass,
    // which is the exact defect issue #1500 was filed for.
    await expect(packs).toHaveAttribute("data-hive-composer-pack", pack);
  }

  // The composer still takes input after the mode change.
  await composer.fill("Audit the webhook handlers for unvalidated input");
  await expect(composer).toHaveValue(
    "Audit the webhook handlers for unvalidated input",
  );

  expect(documentLoads, "switching mode must not navigate").toBe(0);
});

test("proof capture: the agent surface as a composer mode, in both palettes", async ({
  page,
}) => {
  /*
   * Retargeted from /agents by issue #1501.
   *
   * The old version captured two composers side by side, the agent page's and
   * chat's, because the owner's acceptance criterion was that they look like
   * one control rather than merely share a component. That comparison is over,
   * and it ended in the strongest possible way: there is now literally one
   * composer, so there is no second image to put beside the first. What is
   * captured instead is that one composer in both of its modes, which is the
   * claim D-045 actually makes, plus the class assertions that used to guard
   * the extraction and now guard the survivor.
   */
  await page.setViewportSize({ width: 1440, height: 900 });

  for (const scheme of ["light", "dark"] as const) {
    await page.emulateMedia({ colorScheme: scheme });
    await page.goto("/");
    await expect(page.locator("#chat-input")).toBeVisible();

    await page
      .getByRole("radio", { name: "Cowork", exact: true })
      .first()
      .click();
    await expect(page.locator("[data-hive-composer-pack]")).toBeVisible();

    // The DOM evidence travels with the image rather than being asserted only
    // in a log: a screenshot cannot show the absence of a frame on its own.
    const frames = await page.locator("iframe").count();
    expect(frames, "iframe count must be zero in the captured DOM").toBe(0);

    await page.screenshot({
      path: `${PROOF_DIR}/cowork-native-${scheme}.png`,
      fullPage: true,
    });
  }

  await page.emulateMedia({ colorScheme: "light" });
  await page.goto("/");

  const chatComposer = page.locator("#message-input-container");
  await expect(chatComposer).toBeVisible();
  await chatComposer.screenshot({ path: `${PROOF_DIR}/composer-chat.png` });

  // The same control with Cowork selected, cropped identically, so the two can
  // be put side by side and judged as one control in two states.
  await page.getByRole("radio", { name: "Cowork", exact: true }).first().click();
  await expect(page.locator("[data-hive-composer-pack]")).toBeVisible();
  await chatComposer.screenshot({ path: `${PROOF_DIR}/composer-cowork.png` });
  await page.screenshot({ path: `${PROOF_DIR}/chat-after.png`, fullPage: false });

  /*
   * The composer's shape, asserted class by class on chat's own container.
   *
   * These guarded an extraction that has since collapsed into a single
   * component, so they now guard the one composer both modes render. Asserted
   * class by class rather than as one pinned string: the class attribute is
   * assembled from a conditional expression whose branches carry their own
   * leading and trailing spaces, so an exact-string match would fail on
   * whitespace and teach the next person to delete the assertion.
   */
  for (const cls of [
    "rounded-3xl",
    "shadow-lg",
    "backdrop-blur-sm",
    "border",
    "px-1",
  ]) {
    await expect(
      chatComposer,
      `the composer container lost ${cls}`,
    ).toHaveClass(new RegExp(`(^|\\s)${cls}(\\s|$)`));
  }
});

test("the collapsed rail keeps every destination", async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  await page.goto("/");

  await expect(page.locator(SIDEBAR)).toBeVisible();

  await setSidebar(page, "collapsed");

  for (const id of ["chats", "knowledge"]) {
    const row = visibleNavRow(page, id);
    await expect(row, `${id} vanished when the sidebar collapsed`).toBeVisible();
    // Collapsed rows carry no visible label, so the accessible name is the only
    // thing naming them and it must not be empty.
    await expect(row).toHaveAttribute("aria-label", /\S/);
  }
});
