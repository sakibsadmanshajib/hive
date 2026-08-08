import { expect, test, type Page } from "@playwright/test";

/*
 * Live interaction-coverage probe for the agent-console sidecar
 * (apps/agent-console), a.k.a. "Cowork" / the agent workspace, served under
 * a path prefix on the chat host rather than its own subdomain (see
 * deploy/docker/Caddyfile.owui). It has no Playwright project of its own, so
 * it rides along in this environment-driven probe suite next to the
 * web-console staging flows. Endpoints and credentials come from env so the
 * same spec runs against the demo box, staging, or a local stack.
 *
 * Every test title carries a stable [C#] control id. scripts/build-agent-
 * workspace-coverage.mjs maps those ids against
 * agent-workspace-controls.json to produce coverage.json: proven-over-total
 * with the unproven ones named. Do not renumber an existing id -- add a new
 * one instead -- or the coverage history for that control breaks.
 *
 * As of PR #763 every task fails immediately with the engine-unavailable
 * sentinel because this deployment has no Apptainer runtime wired
 * (control-plane structural limitation, #780). A control that submits and
 * correctly surfaces that failure is WORKING. Tests below assert the honest
 * "Blocked" treatment, not a live-running agent.
 */
const CHAT = process.env.HIVE_CHAT_BASE_URL ?? "https://chat-hive.scubed.co";
const WORKSPACE = `${CHAT}/agent-workspace`;

// Must be a user with tenant role OWNER on a tenant whose ENABLE_COWORK
// feature gate is on, otherwise the console fails closed and renders the
// "not enabled for your organization" notice instead of the task panel
// (apps/agent-console/lib/edge-api/gate.ts).
const AGENT_EMAIL = process.env.HIVE_QA_AGENT_EMAIL ?? "";
const AGENT_PASSWORD = process.env.HIVE_QA_AGENT_PASSWORD ?? "";
const HAS_AGENT_CREDS = !!(AGENT_EMAIL && AGENT_PASSWORD);
// 2026-08-08: the credential-provisioning script for the shared demo account
// could not run this pass -- Supabase's admin user-listing endpoint returned
// a persistent 500 ("Database error finding users"), confirmed on two
// attempts. General (non-admin) password-grant login was independently
// confirmed healthy at the same time, so this is not a full auth outage.
// Filed as https://github.com/sakibsadmanshajib/hive/issues/791. Every
// authenticated test below skips with this reason until creds are supplied.
const SKIP_REASON =
  "HIVE_QA_AGENT_EMAIL / HIVE_QA_AGENT_PASSWORD not set (blocked live 2026-08-08 by " +
  "issue #791, Supabase admin user-listing 500s; see that issue before re-deriving)";

async function signIn(page: Page) {
  await page.goto(`${WORKSPACE}/auth/sign-in`);
  await page.locator("#email").fill(AGENT_EMAIL);
  await page.locator("#password").fill(AGENT_PASSWORD);
  await page.click('button[type="submit"]');
  await page.waitForURL((url) => url.pathname === "/agent-workspace/tasks", {
    timeout: 25000,
  });
}

test.describe("sign-in entry (no credentials required)", () => {
  test("[C6] unauthenticated workspace entry redirects to the prefixed sign-in page", async ({
    page,
  }) => {
    await page.goto(WORKSPACE);
    // The whole redirect chain must stay inside /agent-workspace. A target that
    // drops the basePath leaves Caddy's route for this app and is answered by
    // Open WebUI's catch-all instead.
    await expect(page).toHaveURL(`${WORKSPACE}/auth/sign-in`);
    // "Agent workspace" is the eyebrow label (a <span>), not the page heading
    // -- the actual h1 is the sign-in title. Confirmed live 2026-08-08.
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
    // Masked field: value is set, but never rendered as plaintext.
    await expect(password).toHaveValue("whatever-proof-1");
  });

  test("[C5] required fields are marked for assistive tech, but the form does not block empty submit (noValidate)", async ({
    page,
  }) => {
    // Confirmed live 2026-08-08: the sign-in <form> carries `noValidate`, so
    // the `required` attributes on #email/#password are an accessibility
    // signal only -- the browser never blocks the click. The real guard is
    // server side: an empty-credentials submit still fires the token request
    // and still lands on the same visible error as a wrong password (C3/C4).
    // Documenting the actual behaviour here rather than the browser-native
    // block this surface does not actually have.
    await page.goto(`${WORKSPACE}/auth/sign-in`);
    await expect(page.locator("#email")).toHaveAttribute("required", "");
    await expect(page.locator("#password")).toHaveAttribute("required", "");
    const tokenResponse = page.waitForResponse(
      (res) => res.url().includes("/auth/v1/token") && res.request().method() === "POST",
    );
    await page.click('button[type="submit"]');
    const response = await tokenResponse;
    expect(response.status()).toBeGreaterThanOrEqual(400);
    await expect(page.locator('p[role="alert"]')).toBeVisible();
  });

  test("[C3][C4] submit with wrong credentials fires the token request and renders the error alert", async ({
    page,
  }) => {
    await page.goto(`${WORKSPACE}/auth/sign-in`);
    const tokenResponse = page.waitForResponse((res) =>
      res.url().includes("/auth/v1/token") && res.request().method() === "POST",
    );
    await page.locator("#email").fill("proof-of-effect@example.com");
    await page.locator("#password").fill("definitely-wrong-password-123");
    await page.click('button[type="submit"]');
    const response = await tokenResponse;
    expect(response.status()).toBeGreaterThanOrEqual(400);
    // Scoped to the app's own alert <p>: a bare getByRole("alert") also
    // matches Next.js's global route-announcer div (role="alert",
    // id="__next-route-announcer__", present on every Next page for a11y
    // navigation announcements), which is a strict-mode violation. Found live
    // 2026-08-08 -- worth remembering for every other alert-role assertion in
    // this codebase's Next apps.
    await expect(page.locator('p[role="alert"]')).toBeVisible();
    await expect(page.locator('p[role="alert"]')).toContainText("Invalid login credentials");
    // Still on sign-in: a failed grant must not navigate.
    await expect(page).toHaveURL(`${WORKSPACE}/auth/sign-in`);
  });

  test("[C22] agent-task API is reachable on the chat origin and still requires auth", async ({
    request,
  }) => {
    // The task console calls this path same-origin. It must be routed here (not
    // 404) and must reject an unauthenticated caller (not 200).
    const response = await request.get(`${CHAT}/v1/agent/tasks`);
    expect(response.status()).toBe(401);
  });
});

test.describe("authenticated task console", () => {
  test.beforeEach(() => {
    test.skip(!HAS_AGENT_CREDS, SKIP_REASON);
  });

  test("[C8] sign-in with valid credentials lands on the task console, not the chat app", async ({
    page,
  }) => {
    await signIn(page);
    // Regression guard: this used to resolve to a bare /tasks, which Open WebUI
    // answered by redirecting to its own /auth login page.
    await expect(page).toHaveURL(`${WORKSPACE}/tasks`);
    await expect(page.getByRole("heading", { name: "Give the agent a task" })).toBeVisible();
  });

  test("[C9] the Back to chat link returns to the chat origin", async ({ page }) => {
    await signIn(page);
    const link = page.getByRole("link", { name: /Back to chat/i });
    await expect(link).toHaveAttribute("href", "/");
    await link.click();
    await page.waitForURL((url) => url.pathname === "/" || url.pathname === "");
    expect(new URL(page.url()).origin).toBe(new URL(CHAT).origin);
  });

  test("[C10][C11][C12] composer textarea and pack radios reflect selection", async ({
    page,
  }) => {
    await signIn(page);
    const textarea = page.locator("#task-instructions");
    await textarea.fill("Prove the composer records what I type.");
    await expect(textarea).toHaveValue("Prove the composer records what I type.");

    const coding = page.getByRole("radio", { name: "Coding" });
    const knowledge = page.getByRole("radio", { name: "Knowledge work" });
    await expect(coding).toBeChecked();
    await knowledge.check();
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
    let createFired = false;
    await page.route("**/v1/agent/tasks", (route) => {
      if (route.request().method() === "POST") createFired = true;
      return route.continue();
    });
    await page.getByRole("button", { name: "Start task" }).click();
    await expect(page.getByText("Describe the task first. The agent needs a goal to work from.")).toBeVisible();
    expect(createFired).toBe(false);
  });

  test("[C14][C15][C16] Ctrl+Enter submits, a task row appears, and the engine notice explains the block", async ({
    page,
  }) => {
    await signIn(page);
    const brief = `interaction-coverage proof ${Date.now()}`;
    const createResponse = page.waitForResponse(
      (res) => res.url().includes("/v1/agent/tasks") && res.request().method() === "POST",
    );
    await page.locator("#task-instructions").fill(brief);
    await page.locator("#task-instructions").press("Control+Enter");
    const response = await createResponse;
    expect(response.ok()).toBe(true);

    const tasks = page.getByRole("region", { name: "Tasks" });
    const row = tasks.getByRole("listitem").filter({ hasText: brief }).first();
    await expect(row).toBeVisible();
    // Known deployment state (#780): the control-plane container cannot exec
    // Apptainer, so every task fails immediately. "Blocked" + the engine
    // notice is the CORRECT outcome here, not a bug -- asserting it is what
    // proves this control does something rather than nothing.
    await expect(row).toContainText("Blocked", { timeout: 20000 });
    await expect(
      page.getByRole("status").filter({ hasText: "The agent runtime is not configured" }),
    ).toBeVisible();
  });

  test("[C17] cancel is correctly withheld once a task is already terminal", async ({
    page,
    request,
  }) => {
    await signIn(page);
    // On this deployment every task create resolves straight to the terminal
    // "failed" (engine-unavailable) state -- there is no live window where a
    // task sits queued/running for a person to cancel via the button (that
    // requires a configured agent runtime, #780). The button's rendering
    // rule (`!TERMINAL_STATUSES.has(task.status)`) is proven correctly
    // negative here: create a task, confirm it lands terminal, confirm no
    // Cancel button is offered for that row, and confirm the guard also
    // holds at the API for a caller that ignores the UI entirely (a real
    // failure mode: client-side hiding a button proves nothing about the
    // server actually refusing the call).
    const brief = `cancel-guard proof ${Date.now()}`;
    const createRequest = page.waitForRequest(
      (req) => req.url().includes("/v1/agent/tasks") && req.method() === "POST",
    );
    const createResponse = page.waitForResponse(
      (res) => res.url().includes("/v1/agent/tasks") && res.request().method() === "POST",
    );
    await page.locator("#task-instructions").fill(brief);
    await page.getByRole("button", { name: "Start task" }).click();
    const authHeader = (await createRequest).headers()["authorization"];
    const created = await (await createResponse).json();

    const tasks = page.getByRole("region", { name: "Tasks" });
    const row = tasks.getByRole("listitem").filter({ hasText: brief }).first();
    await expect(row).toContainText("Blocked", { timeout: 20000 });
    await expect(row.getByRole("button", { name: "Cancel" })).toHaveCount(0);

    const cancelResponse = await request.post(
      `${CHAT}/v1/agent/tasks/${created.id}/cancel`,
      { headers: { Authorization: authHeader } },
    );
    expect(cancelResponse.status()).toBeGreaterThanOrEqual(400);
  });

  test("[C19] a failing task list load renders the retry banner, not a crash", async ({ page }) => {
    await page.route("**/v1/agent/tasks", (route) => {
      if (route.request().method() === "GET") {
        return route.fulfill({ status: 503, body: "{}" });
      }
      return route.continue();
    });
    await signIn(page);
    await expect(page.getByRole("alert").filter({ hasText: "Could not load your tasks" })).toBeVisible(
      { timeout: 15000 },
    );
  });

  test("[C20] a failing task create renders its own error, keeps the draft text", async ({
    page,
  }) => {
    await signIn(page);
    await page.route("**/v1/agent/tasks", (route) => {
      if (route.request().method() === "POST") {
        return route.fulfill({ status: 500, body: "{}" });
      }
      return route.continue();
    });
    const brief = "this text must survive a failed submit";
    await page.locator("#task-instructions").fill(brief);
    await page.getByRole("button", { name: "Start task" }).click();
    await expect(page.getByRole("alert").filter({ hasText: "Could not start the task" })).toBeVisible();
    await expect(page.locator("#task-instructions")).toHaveValue(brief);
  });

  test("[C21] an expired session is reported in place of a silent failure", async ({
    page,
    context,
  }) => {
    await signIn(page);
    // Drop the Supabase session cookies client-side without navigating, then
    // try to act -- this is what a real expiry looks like from the page's
    // point of view (getSession() resolves to null).
    await context.clearCookies();
    await page.locator("#task-instructions").fill("should not be sent");
    await page.getByRole("button", { name: "Start task" }).click();
    await expect(
      page.getByRole("alert").filter({ hasText: "Your session expired" }),
    ).toBeVisible({ timeout: 10000 });
  });

  test("[C18] the empty state renders when the account has no tasks yet", async ({ page }) => {
    await signIn(page);
    const tasks = page.getByRole("region", { name: "Tasks" });
    const count = await tasks.getByRole("listitem").count();
    test.skip(
      count > 0,
      "demo account already has task history from prior runs; empty state is not reachable without deleting live rows, which is out of scope",
    );
    await expect(page.getByText("Nothing submitted yet")).toBeVisible();
  });
});
