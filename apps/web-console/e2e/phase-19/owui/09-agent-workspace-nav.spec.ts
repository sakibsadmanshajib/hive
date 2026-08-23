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

test("the sidebar carries labelled Chats, Agents and Knowledge destinations", async ({
  page,
}) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  await page.goto("/");

  await expect(page.locator(SIDEBAR)).toBeVisible();
  await setSidebar(page, "expanded");

  for (const [id, label, href] of [
    ["chats", "Chats", "/"],
    ["agents", "Agents", "/agents"],
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
  let tasksBody: { tasks?: unknown } | null = null;
  page.on("response", async (response) => {
    if (new URL(response.url()).pathname !== TASKS_ENDPOINT) {
      return;
    }
    tasksStatus = response.status();
    if (response.status() === 200) {
      try {
        tasksBody = await response.json();
      } catch {
        tasksBody = null;
      }
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

  // An iframe count of zero is also true of a page that rendered nothing at
  // all, so every assertion below is a positive one: the surface has to paint
  // and work, not merely fail to contain a frame.
  //
  // The composer is this application's own DOM, it is the chat composer's
  // container rather than a form that resembles one, and it takes input.
  const composer = page.locator("#hive-agent-instructions");
  await expect(composer).toBeVisible();
  await expect(page.locator("#hive-agent-send")).toBeVisible();
  await composer.fill("Audit the webhook handlers for unvalidated input");
  await expect(composer).toHaveValue(
    "Audit the webhook handlers for unvalidated input",
  );
  // The container is the shared one, not a lookalike: this id belongs to the
  // component MessageInput.svelte renders too.
  await expect(page.locator("#hive-agent-instructions").locator("xpath=ancestor::*[contains(@class,'rounded-3xl')]").first()).toBeVisible();
  for (const label of ["Knowledge work", "Coding"]) {
    const option = page.getByText(label, { exact: true });
    await expect(
      option,
      `the ${label} toggle is missing from the composer`,
    ).toBeVisible();
    await option.click();
  }

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

  // The list has to paint what the API actually returned. Without this, a
  // surface that mounted and then rendered nothing would still satisfy every
  // assertion above, which is the shape of green that cannot go red.
  if (tasksStatus === 200) {
    const rows = Array.isArray(tasksBody?.tasks) ? (tasksBody!.tasks as Array<Record<string, unknown>>) : [];
    if (rows.length > 0) {
      await expect(
        page.locator("[data-hive-task-row]"),
        "the API returned tasks and the list painted a different number of rows",
      ).toHaveCount(rows.length);
      // One row's own text, taken from the response rather than from a fixture.
      // Rows that predate the goal field carry no instructions and must say so
      // deliberately rather than rendering an empty line.
      const first = rows[0];
      const expected =
        typeof first.instructions === "string" && first.instructions.trim() !== ""
          ? first.instructions
          : "No description was recorded for this task.";
      await expect(
        page.locator(`[data-hive-task-row="${first.id}"]`),
      ).toContainText(expected);
    } else {
      await expect(
        page.getByText("Nothing submitted yet"),
        "no tasks came back, so the genuine empty state must be on screen",
      ).toBeVisible();
    }
  } else {
    // 403 is the Cowork gate. The surface must say so in the server's own
    // words rather than showing a blank list that implies zero tasks.
    await expect(
      page.getByRole("alert"),
      "a refused list must be stated, not rendered as an empty one",
    ).toBeVisible();
  }

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

  // The two composers, cropped to the control itself, from one build and one
  // browser, so they can be put side by side and judged as one product or not.
  // This is the owner's actual acceptance criterion: sharing a component is the
  // means, looking like one control is the requirement.
  await page.emulateMedia({ colorScheme: "light" });

  await page.goto("/agents");
  const agentComposer = page
    .locator("#hive-agent-instructions")
    .locator("xpath=ancestor::*[contains(@class,'rounded-3xl')]")
    .first();
  await expect(agentComposer).toBeVisible();
  // Typed so the send button is in its enabled state for the capture, which is
  // the state a reader is judging, and so the geometry below is asserted on a
  // live control rather than a disabled one.
  await page.locator("#hive-agent-instructions").fill("Rename the payslips in ascending order");
  for (const cls of ["rounded-full", "p-1.5", "self-center"]) {
    await expect(
      page.locator("#hive-agent-send"),
      `the shared send button lost ${cls}`,
    ).toHaveClass(new RegExp(`(^|\\s)${cls}(\\s|$)`));
  }
  await expect(page.locator("#hive-agent-send")).toHaveAttribute(
    "aria-label",
    /\S/,
  );
  await agentComposer.screenshot({ path: `${PROOF_DIR}/composer-agent.png` });

  await page.goto("/");
  const chatComposer = page.locator("#message-input-container");
  await expect(chatComposer).toBeVisible();
  await chatComposer.screenshot({ path: `${PROOF_DIR}/composer-chat.png` });
  await page.screenshot({ path: `${PROOF_DIR}/chat-after.png`, fullPage: false });

  /*
   * The same two composers again at 1280 by 720.
   *
   * This is Playwright's default viewport, which is what owui.setup.ts runs at,
   * and therefore the width of the only "before" capture of chat's composer
   * that exists: the signed-in page in main's own nightly failure screenshot.
   * A before at 1280 and an after at 1440 is not a comparison, because a reader
   * cannot tell a layout change from a reflow, and that is exactly the
   * judgement these images are for. Same width or it proves nothing.
   */
  await page.setViewportSize({ width: 1280, height: 720 });

  await page.goto("/agents");
  const agentComposer1280 = page
    .locator("#hive-agent-instructions")
    .locator("xpath=ancestor::*[contains(@class,'rounded-3xl')]")
    .first();
  await expect(agentComposer1280).toBeVisible();
  await agentComposer1280.screenshot({ path: `${PROOF_DIR}/composer-agent-1280.png` });

  await page.goto("/");
  await expect(chatComposer).toBeVisible();
  await chatComposer.screenshot({ path: `${PROOF_DIR}/composer-chat-1280.png` });

  /*
   * The extraction has to be invisible on the chat surface, and two images a
   * human eyeballs is a weak way to prove that. These are the classes that
   * carry the composer's shape, asserted on chat's own container, so drift in
   * the extracted component fails here in words rather than in a screenshot
   * comparison nobody can diff.
   *
   * Asserted class by class rather than as one pinned string: the class
   * attribute is assembled from a conditional expression whose branches carry
   * their own leading and trailing spaces, so an exact-string match would fail
   * on whitespace and teach the next person to delete the assertion.
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
      `chat's composer container lost ${cls}, so the extraction was not invisible`,
    ).toHaveClass(new RegExp(`(^|\\s)${cls}(\\s|$)`));
  }
});

test("the collapsed rail keeps every destination", async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  await page.goto("/");

  await expect(page.locator(SIDEBAR)).toBeVisible();

  await setSidebar(page, "collapsed");

  for (const id of ["chats", "agents", "knowledge"]) {
    const row = visibleNavRow(page, id);
    await expect(row, `${id} vanished when the sidebar collapsed`).toBeVisible();
    // Collapsed rows carry no visible label, so the accessible name is the only
    // thing naming them and it must not be empty.
    await expect(row).toHaveAttribute("aria-label", /\S/);
  }
});
