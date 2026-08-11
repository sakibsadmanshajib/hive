import { expect, test, type Page } from "@playwright/test";

import controls from "./agent-workspace-controls.json";
import { reauthenticate } from "../support/live-auth";

/*
 * Live interaction-coverage probe for the agent-console sidecar
 * (apps/agent-console), also known as "Cowork" or the agent workspace. It is
 * served under a path prefix on the chat host rather than its own subdomain
 * (see deploy/docker/Caddyfile.owui). Endpoints and the identity come from
 * env so the same spec runs against the demo box, staging, or a local stack.
 *
 * Every test title carries a stable [C#] control id. scripts/build-agent-
 * workspace-coverage.mjs maps those ids against
 * agent-workspace-controls.json to produce the coverage ledger: proven over
 * total, with the unproven ones named. Do not renumber an existing id, add a
 * new one instead, or the coverage history for that control breaks.
 *
 * AUTHENTICATION. Sessions come from tests/e2e/support/live-auth.ts, which
 * mints one through the admin one-time-token flow. It needs no password and
 * changes none. Setting, resetting or rotating a shared account's password to
 * obtain a session is forbidden permanently (docs/live-test-auth.md); it
 * invalidated three concurrent runs on 2026-08-08.
 *
 * TASK LIFECYCLE. Since PR #870 the demo box runs real agent tasks through an
 * unprivileged host launcher, so a submitted task moves queued, then running,
 * then succeeded over roughly sixteen minutes. It no longer resolves straight
 * to a terminal "Blocked" state, which is what the previous version of this
 * file asserted. POST /v1/agent/tasks is synchronous over the launch
 * (control-plane's CreateTask calls Engine.Launch inline, bounded at five
 * minutes), so a create can take tens of seconds before it answers with a
 * running task. The timeouts below are sized for that, not for a stub.
 */
const CHAT = process.env.HIVE_CHAT_BASE_URL ?? "https://chat-hive.scubed.co";
const WORKSPACE = `${CHAT}/agent-workspace`;
const TASKS_URL = `${WORKSPACE}/tasks`;

/*
 * Must be a user with tenant role OWNER on a tenant whose ENABLE_COWORK
 * feature gate is on, otherwise the console fails closed and renders the
 * "not enabled for your organization" notice instead of the task panel
 * (apps/agent-console/lib/edge-api/gate.ts).
 *
 * Defaulted rather than left empty on purpose. The account name is already
 * public in docs/proof/live-auth-helper-2026-08-08/README.md and it is not a
 * credential, so a default keeps this suite runnable by hand with no setup,
 * and makes a renamed account fail loudly instead of skipping the whole
 * authenticated half into silence.
 */
const AGENT_EMAIL = process.env.HIVE_QA_AGENT_EMAIL ?? "demo@hive-demo.invalid";
const AGENT_PASSWORD = process.env.HIVE_QA_AGENT_PASSWORD ?? "";

// live-auth.mjs mints against these three. Without them there is no session
// and no honest way to get one.
const MINT_ENV = ["SUPABASE_URL", "SUPABASE_SERVICE_ROLE_KEY", "SUPABASE_ANON_KEY"];
const MISSING_MINT_ENV = MINT_ENV.filter((name) => !(process.env[name] ?? "").trim());
const MINT_SKIP_REASON =
  `cannot mint a live session: ${MISSING_MINT_ENV.join(", ")} not set. ` +
  "See docs/live-test-auth.md. There is no credential-rotating fallback and there must never be one.";

const PASSWORD_SKIP_REASON =
  "HIVE_QA_AGENT_PASSWORD not set. Every other authenticated control here is proven from a " +
  "session minted by tests/e2e/support/live-auth.mjs, which needs no password. This one is the " +
  "password submit path itself, so it can only be proven by typing a real password into the real " +
  "form. Supplying an existing password is fine; rotating the shared account to invent one is " +
  "forbidden (docs/live-test-auth.md).";

/** Installs a freshly minted session on the context. No password involved. */
async function signIn(page: Page): Promise<void> {
  await reauthenticate(page.context(), { email: AGENT_EMAIL, targetUrl: WORKSPACE });
}

/**
 * Opens the task console and waits for the panel itself, not just the page.
 *
 * The h1 renders whether or not Cowork is enabled for the tenant, so
 * asserting on it would pass against the "turned off for your organization"
 * notice. The composer only exists when the gate is on.
 */
async function openTaskConsole(page: Page): Promise<void> {
  await page.goto(TASKS_URL);
  await expect(page).toHaveURL(TASKS_URL);
  await expect(page.locator("#task-instructions")).toBeVisible({ timeout: 20000 });
}

const TASK_LIST_GLOB = "**/v1/agent/tasks";

/*
 * Every focusable control on a screen, as a stable descriptor.
 *
 * This is what stops the coverage denominator from being author controlled.
 * The ledger's `dom` fields are compared against what the deployed page
 * actually renders, so a control that ships without a ledger entry fails the
 * run rather than quietly shrinking the total.
 */
const FOCUSABLE_SELECTOR =
  'a[href], button, input:not([type="hidden"]), textarea, select, [tabindex]:not([tabindex="-1"])';

async function focusableInventory(page: Page): Promise<string[]> {
  return page.evaluate((selector) => {
    const clean = (value: string) =>
      value
        .replace(/[^\p{L}\p{N}\s]/gu, " ")
        .replace(/\s+/g, " ")
        .trim()
        .slice(0, 40);
    const found = new Set<string>();
    for (const node of Array.from(document.querySelectorAll(selector))) {
      const tag = node.tagName.toLowerCase();
      let type = "";
      let radioValue = "";
      let label = "";
      if (node instanceof HTMLInputElement) {
        type = node.type;
        if (node.type === "radio") radioValue = `=${node.value}`;
        label = clean(node.getAttribute("aria-label") ?? "");
      } else if (node instanceof HTMLButtonElement) {
        type = node.type;
        label = clean(node.textContent ?? "");
      } else if (node instanceof HTMLAnchorElement) {
        label = clean(node.textContent ?? "");
      } else {
        label = clean(node.getAttribute("aria-label") ?? "");
      }
      const id = node.id ? `#${node.id}` : "";
      found.add(
        `${tag}${type ? `[${type}]` : ""}${id}${radioValue}${label ? `:${label}` : ""}`,
      );
    }
    return [...found].sort();
  }, FOCUSABLE_SELECTOR);
}

function ledgerInventory(screen: string): string[] {
  return controls.controls
    .filter((control) => control.screen === screen && control.dom)
    .map((control) => control.dom)
    .sort();
}

test.describe("sign-in entry (no session required)", () => {
  test("[C6] unauthenticated workspace entry redirects to the prefixed sign-in page", async ({
    page,
  }) => {
    await page.goto(WORKSPACE);
    // The whole redirect chain must stay inside /agent-workspace. A target that
    // drops the basePath leaves Caddy's route for this app and is answered by
    // Open WebUI's catch-all instead.
    await expect(page).toHaveURL(`${WORKSPACE}/auth/sign-in`);
    // "Agent workspace" is the eyebrow label (a span), not the page heading.
    // The actual h1 is the sign-in title. Confirmed live 2026-08-08.
    await expect(page.getByRole("heading", { name: "Sign in to run agent tasks" })).toBeVisible();
    await expect(page.getByText("Agent workspace", { exact: true })).toBeVisible();
  });

  test("[C7] unauthenticated direct /tasks visit also redirects to sign-in", async ({ page }) => {
    // Separate middleware branch from C6 (pathname.startsWith("/tasks") vs
    // pathname === "/"): proves the guard is not just on the bare root.
    await page.goto(`${WORKSPACE}/tasks`);
    await expect(page).toHaveURL(`${WORKSPACE}/auth/sign-in`);
  });

  test("[C1][C2] email and password inputs accept and reflect input", async ({ page }) => {
    await page.goto(`${WORKSPACE}/auth/sign-in`);
    const email = page.locator("#email");
    const password = page.locator("#password");
    await expect(email).toHaveAttribute("type", "email");
    await expect(password).toHaveAttribute("type", "password");
    await email.fill("proof-of-effect@example.com");
    await password.fill("whatever-proof-1");
    await expect(email).toHaveValue("proof-of-effect@example.com");
    // Masked field: the value is set, but never rendered as plaintext.
    await expect(password).toHaveValue("whatever-proof-1");
  });

  test("[C5] required fields are advisory only, because the form is noValidate", async ({
    page,
  }) => {
    // Confirmed live 2026-08-08: the sign-in form carries `noValidate`, so the
    // `required` attributes on #email and #password are an accessibility
    // signal only and the browser never blocks the click. The real guard is
    // server side. An empty-credentials submit still fires the token request
    // and still lands on the same visible error as a wrong password.
    await page.goto(`${WORKSPACE}/auth/sign-in`);
    await expect(page.locator("#email")).toHaveAttribute("required", "");
    await expect(page.locator("#password")).toHaveAttribute("required", "");
    const tokenResponse = page.waitForResponse(
      (res) => res.url().includes("/auth/v1/token") && res.request().method() === "POST",
    );
    await page.click('button[type="submit"]');
    const response = await tokenResponse;
    // Exactly 400. A range assertion here also passes on a 500 from a broken
    // auth service, which is the opposite of what this control claims.
    expect(response.status()).toBe(400);
    await expect(page.locator('p[role="alert"]')).toBeVisible();
  });

  test("[C3][C4] submit with wrong credentials fires the token request and renders the error alert", async ({
    page,
  }) => {
    await page.goto(`${WORKSPACE}/auth/sign-in`);
    const tokenResponse = page.waitForResponse(
      (res) => res.url().includes("/auth/v1/token") && res.request().method() === "POST",
    );
    await page.locator("#email").fill("proof-of-effect@example.com");
    await page.locator("#password").fill("definitely-wrong-password-123");
    await page.click('button[type="submit"]');
    const response = await tokenResponse;
    expect(response.status()).toBe(400);
    // Scoped to the app's own alert paragraph. A bare getByRole("alert") also
    // matches Next.js's global route-announcer div (role="alert",
    // id="__next-route-announcer__", present on every Next page for a11y
    // navigation announcements), which is a strict-mode violation. Found live
    // 2026-08-08 and worth remembering for every other alert-role assertion in
    // this codebase's Next apps.
    await expect(page.locator('p[role="alert"]')).toBeVisible();
    await expect(page.locator('p[role="alert"]')).toContainText("Invalid login credentials");
    // Still on sign-in: a failed grant must not navigate.
    await expect(page).toHaveURL(`${WORKSPACE}/auth/sign-in`);
  });

  test("[C22] agent-task API is reachable on the chat origin and still requires auth", async ({
    request,
  }) => {
    // The task console calls this path same-origin. It must be routed here (so
    // not a 404) and must reject an unauthenticated caller (so not a 200).
    const response = await request.get(`${CHAT}/v1/agent/tasks`);
    expect(response.status()).toBe(401);
  });
});

test.describe("authenticated task console", () => {
  test.beforeEach(() => {
    test.skip(MISSING_MINT_ENV.length > 0, MINT_SKIP_REASON);
  });

  test("[C8] sign-in with valid credentials lands on the task console, not the chat app", async ({
    page,
  }) => {
    test.skip(!AGENT_PASSWORD, PASSWORD_SKIP_REASON);
    await page.goto(`${WORKSPACE}/auth/sign-in`);
    await page.locator("#email").fill(AGENT_EMAIL);
    await page.locator("#password").fill(AGENT_PASSWORD);
    await page.click('button[type="submit"]');
    // Regression guard: this used to resolve to a bare /tasks, which Open WebUI
    // answered by redirecting to its own /auth login page.
    await page.waitForURL(TASKS_URL, { timeout: 25000 });
    await expect(page.getByRole("heading", { name: "Give the agent a task" })).toBeVisible();
  });

  test("[C9] the Back to chat link returns to the chat origin", async ({ page }) => {
    await signIn(page);
    await openTaskConsole(page);
    const link = page.getByRole("link", { name: /Back to chat/i });
    await expect(link).toHaveAttribute("href", "/");
    await link.click();
    await page.waitForURL((url) => url.pathname === "/" || url.pathname === "");
    expect(new URL(page.url()).origin).toBe(new URL(CHAT).origin);
  });

  test("[C10][C11][C12] composer textarea and pack radios reflect selection", async ({ page }) => {
    await signIn(page);
    await openTaskConsole(page);
    const textarea = page.locator("#task-instructions");
    await textarea.fill("Prove the composer records what I type.");
    await expect(textarea).toHaveValue("Prove the composer records what I type.");

    const coding = page.getByRole("radio", { name: "Coding" });
    const knowledge = page.getByRole("radio", { name: "Knowledge work" });
    await expect(coding).toBeChecked();
    // Clicking the label, not .check() on the input. The input is `peer
    // sr-only` and the visible control is the span inside its label
    // (apps/agent-console/components/task-console.tsx), so .check() times out
    // with "label intercepts pointer events". Clicking the label is both what
    // makes this pass and what a real user does.
    await page.locator('label:has(input[value="knowledge-work-pack"])').click();
    await expect(knowledge).toBeChecked();
    await expect(coding).not.toBeChecked();
    // Selecting the pack swaps the hint copy under the toggle.
    await expect(
      page.getByText("Researches, reads documents, and writes up an answer."),
    ).toBeVisible();
  });

  test("[C13] submitting an empty task shows the inline validation message, fires no request", async ({
    page,
  }) => {
    await signIn(page);
    await openTaskConsole(page);
    let createFired = false;
    await page.route(TASK_LIST_GLOB, (route) => {
      if (route.request().method() === "POST") createFired = true;
      return route.continue();
    });
    await page.getByRole("button", { name: "Start task" }).click();
    await expect(
      page.getByText("Describe the task first. The agent needs a goal to work from."),
    ).toBeVisible();
    expect(createFired).toBe(false);
  });

  test("[C14][C15][C16][C17] a real task is created, is cancellable while it runs, and a second cancel is refused", async ({
    page,
    request,
  }) => {
    /*
     * One create per run, on purpose. control-plane's CreateTask launches the
     * sandbox inline and only answers once the launch returns, which is tens
     * of seconds on a warm box and is bounded at five minutes. Every control
     * this test carries is downstream of that one create, so splitting them
     * would mean launching several real sandboxes on a shared demo box for no
     * extra coverage. The task is cancelled at the end rather than left to run
     * its full course.
     *
     * Do not read a second run inside the same quarter hour as a flake. Cancel
     * is a database transition only: it never reaches the engine, so the
     * sandbox keeps running and keeps its concurrency slot until it ends on its
     * own (issue #886). Two runs in quick succession exhaust
     * HIVE_QUOTA_USER_CONCURRENCY, and the third create then genuinely fails
     * with "agent engine could not start the task", which the row renders as
     * Blocked. That is the deployment answering honestly, not this test.
     */
    test.setTimeout(360_000);
    await signIn(page);
    await openTaskConsole(page);

    const brief = `interaction-coverage proof ${Date.now()}`;
    const createRequest = page.waitForRequest(
      (req) => req.url().includes("/v1/agent/tasks") && req.method() === "POST",
      { timeout: 30_000 },
    );
    const createResponse = page.waitForResponse(
      (res) => res.url().includes("/v1/agent/tasks") && res.request().method() === "POST",
      { timeout: 330_000 },
    );
    await page.locator("#task-instructions").fill(brief);
    // C14: Ctrl+Enter is the submit path being proven here. C13 above proves
    // the Start task button reaches the same handler.
    await page.locator("#task-instructions").press("Control+Enter");

    const authHeader = (await createRequest).headers()["authorization"];
    // Asserted as a boolean rather than with toMatch, so a failure prints
    // "false" instead of echoing a live bearer token into the report, the
    // trace and the CI log.
    expect(
      typeof authHeader === "string" && authHeader.startsWith("Bearer "),
      "the create request must carry a bearer token, otherwise the cancel assertions below " +
        "would be asserting against an unauthenticated 401",
    ).toBe(true);

    /*
     * C15. Soft, and the only soft assertion in this file. It still fails the
     * test, so nothing is hidden, but the body continues and the task this
     * created is still cancelled at the end rather than left holding a sandbox
     * on a shared box.
     *
     * A soft failure makes Playwright append a second, spurious error reading
     * "Test timeout of 30000ms exceeded" even when the body ran to completion
     * well inside the raised timeout above. Confirmed against a throwaway spec
     * that logged testInfo.timeout as 120000 and printed its last line before
     * the runner reported the same 30000. Ignore that line and read the
     * assertion error underneath it.
     *
     * It fails against the demo box today, and the cause is a real defect
     * rather than a flake (issue #881): edge-api's control-plane client has a 15 second
     * timeout (apps/edge-api/internal/agenttask/client.go) while
     * control-plane's CreateTask blocks inline on Engine.Launch with a five
     * minute bound (apps/control-plane/internal/agenttask/service.go). Any
     * launch slower than 15 seconds answers the browser with a 500 while the
     * task is created and runs to completion, so the composer says "Could not
     * start the task" about a task that is running. Measured live on
     * 2026-08-11: 18.0s to a 500, and the task reached `succeeded`.
     */
    const response = await createResponse;
    expect
      .soft(
        response.status(),
        "POST /v1/agent/tasks must answer 201. A 500 here, with the task still appearing in " +
          "the list below, is issue #881, the edge-api create timeout described above.",
      )
      .toBe(201);

    /*
     * The create's proof of effect is read from the server rather than from
     * the create response body, so it holds whatever the status code was: the
     * row is persisted and comes back from the list. That is also a stronger
     * claim than trusting the body the create echoed.
     */
    const listResponse = page.waitForResponse(
      (res) =>
        res.url().includes("/v1/agent/tasks") &&
        res.request().method() === "GET" &&
        res.status() === 200,
      { timeout: 60_000 },
    );
    await page.reload();
    const listed: { tasks?: Array<{ id?: unknown; instructions?: unknown }> } = await (
      await listResponse
    ).json();
    const match = (listed.tasks ?? []).find((task) => task.instructions === brief);
    const createdId = typeof match?.id === "string" ? match.id : "";
    expect(
      createdId,
      "the submitted task must come back from GET /v1/agent/tasks, otherwise nothing was created",
    ).not.toBe("");

    const tasks = page.getByRole("region", { name: "Tasks" });
    const row = tasks.getByRole("listitem").filter({ hasText: brief }).first();
    await expect(row).toBeVisible();
    /*
     * C16. The engine notice is derived from the newest task carrying the
     * engine-unavailable sentinel. Since #870 the demo box has a working
     * runtime, so the correct rendering is its absence, and its presence here
     * means the launcher is down. The previous version of this file asserted
     * the notice was present, which was true only while the runtime was
     * unconfigured.
     */
    await expect(
      page.getByRole("status").filter({ hasText: "The agent runtime is not configured" }),
    ).toHaveCount(0);
    /*
     * Non-terminal, so the row offers Cancel. "Blocked" is what both engine
     * sentinels read as, and there are exactly two ways to get one:
     * the runtime is unconfigured on this deployment, which the notice
     * asserted just above would also show; or the launcher refused this
     * particular launch, which on a repeated run is the held quota slot in
     * issue #886.
     */
    await expect(
      row,
      "the task must reach a non-terminal state so cancel is exercisable. Blocked means the " +
        "launcher refused it: either no runtime is configured, or the user's concurrency slots " +
        "are still held by earlier cancelled tasks (issue #886).",
    ).not.toContainText("Blocked");
    const cancel = row.getByRole("button", { name: "Cancel" });
    await expect(cancel).toBeVisible();

    // C17, UI half: the button actually cancels.
    await cancel.click();
    await expect(row).toContainText("Cancelled", { timeout: 30_000 });
    await expect(row.getByRole("button", { name: "Cancel" })).toHaveCount(0);

    /*
     * C17, server half. Hiding a button proves nothing about the server
     * refusing the call, and this is the assertion the previous version got
     * inverted: it asserted only ">= 400", which stays green if the route is
     * unmounted and 404s, or if the caller is unauthenticated and gets a 401.
     * The contract says 409 on an already-terminal task
     * (apps/control-plane/internal/agenttask/SYNC_CONTRACT.md), so that is
     * what is asserted.
     */
    const cancelAgain = await request.post(`${CHAT}/v1/agent/tasks/${createdId}/cancel`, {
      headers: { Authorization: authHeader },
    });
    expect(cancelAgain.status()).toBe(409);
  });

  test("[C18] the empty state renders when the account has no tasks", async ({ page }) => {
    await signIn(page);
    /*
     * The list is stubbed empty rather than gated on the live account being
     * empty. The previous version skipped itself whenever the account already
     * had history, which every earlier test in this file guarantees by
     * creating one, so the control switched itself off permanently after its
     * first run. Deleting live rows to reach the state is out of scope, and
     * the rendering rule under test is `tasks.length === 0`, which this
     * exercises exactly.
     */
    await page.route(TASK_LIST_GLOB, (route) => {
      if (route.request().method() === "GET") {
        return route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({ tasks: [] }),
        });
      }
      return route.continue();
    });
    await openTaskConsole(page);
    await expect(page.getByText("Nothing submitted yet")).toBeVisible();
  });

  test("[C19] a failing task list load renders the retry banner, not a crash", async ({ page }) => {
    await signIn(page);
    await page.route(TASK_LIST_GLOB, (route) => {
      if (route.request().method() === "GET") {
        return route.fulfill({ status: 503, body: "{}" });
      }
      return route.continue();
    });
    await openTaskConsole(page);
    await expect(
      page.getByRole("alert").filter({ hasText: "Could not load your tasks" }),
    ).toBeVisible({ timeout: 20000 });
  });

  test("[C20] a failing task create renders its own error, keeps the draft text", async ({
    page,
  }) => {
    await signIn(page);
    await openTaskConsole(page);
    await page.route(TASK_LIST_GLOB, (route) => {
      if (route.request().method() === "POST") {
        return route.fulfill({ status: 500, body: "{}" });
      }
      return route.continue();
    });
    const brief = "this text must survive a failed submit";
    await page.locator("#task-instructions").fill(brief);
    await page.getByRole("button", { name: "Start task" }).click();
    await expect(
      page.getByRole("alert").filter({ hasText: "Could not start the task" }),
    ).toBeVisible();
    await expect(page.locator("#task-instructions")).toHaveValue(brief);
  });

  test("[C21] an expired session is reported in place of a silent failure", async ({
    page,
    context,
  }) => {
    await signIn(page);
    await openTaskConsole(page);
    // Drop the Supabase session cookies client side without navigating, then
    // try to act. This is what a real expiry looks like from the page's point
    // of view: getSession() resolves to null.
    await context.clearCookies();
    await page.locator("#task-instructions").fill("should not be sent");
    await page.getByRole("button", { name: "Start task" }).click();
    await expect(page.getByRole("alert").filter({ hasText: "Your session expired" })).toBeVisible({
      timeout: 15000,
    });
  });

  test("[C23] every focusable control on both screens is claimed by the ledger", async ({
    page,
  }) => {
    /*
     * The denominator check. Without this, the coverage ratio is only as
     * honest as the author's enumeration: a control that ships without a
     * ledger entry raises neither the total nor the proven count, and the
     * percentage goes up.
     *
     * The task list is stubbed to a fixed pair (one running row, one terminal
     * row) so both the Cancel-offered and Cancel-withheld renderings are on
     * screen in the same pass, and so the inventory does not change with
     * whatever history the live account happens to hold.
     */
    await signIn(page);
    const now = new Date().toISOString();
    const stubbed = {
      tasks: [
        {
          id: "00000000-0000-4000-8000-000000000001",
          pack: "coding-pack",
          instructions: "enumeration fixture, running",
          status: "running",
          engine_session_ref: "",
          result_summary_ref: "",
          error_message: "",
          created_at: now,
          updated_at: now,
          started_at: now,
          finished_at: null,
        },
        {
          id: "00000000-0000-4000-8000-000000000002",
          pack: "knowledge-work-pack",
          instructions: "enumeration fixture, finished",
          status: "succeeded",
          engine_session_ref: "",
          result_summary_ref: "",
          error_message: "",
          created_at: now,
          updated_at: now,
          started_at: now,
          finished_at: now,
        },
      ],
    };
    await page.route(TASK_LIST_GLOB, (route) => {
      if (route.request().method() === "GET") {
        return route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify(stubbed),
        });
      }
      return route.continue();
    });

    await openTaskConsole(page);
    await expect(page.getByRole("button", { name: "Cancel" })).toHaveCount(1);
    expect(
      await focusableInventory(page),
      "the task console renders a focusable control the ledger does not claim, or claims one it " +
        "no longer renders. Add or remove the matching entry in agent-workspace-controls.json.",
    ).toEqual(ledgerInventory("tasks"));

    /*
     * The one "not present" claim in the ledger, turned into an assertion.
     * The task console has no live transcript surface yet, which is why no
     * control enumerates one. If a transcript pane ships, this fails and
     * forces it into the ledger instead of leaving it silently uncounted.
     */
    await expect(page.getByRole("log")).toHaveCount(0);

    await page.goto(`${WORKSPACE}/auth/sign-in`);
    await expect(page.locator("#email")).toBeVisible();
    expect(
      await focusableInventory(page),
      "the sign-in screen renders a focusable control the ledger does not claim, or claims one " +
        "it no longer renders. Add or remove the matching entry in agent-workspace-controls.json.",
    ).toEqual(ledgerInventory("sign-in"));
  });
});
