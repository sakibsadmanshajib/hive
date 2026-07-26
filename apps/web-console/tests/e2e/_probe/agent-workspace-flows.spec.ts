import { expect, test, type Page } from "@playwright/test";

// Live probe for the agent-console sidecar (apps/agent-console), which is
// served under a path prefix on the chat host rather than its own subdomain
// (deploy/docker/Caddyfile.owui). It has no Playwright project of its own, so
// it rides along in this environment-driven probe suite next to the
// web-console staging flows. Endpoints and credentials come from env so the
// same spec runs against the demo box, staging, or a local stack.
const CHAT = process.env.HIVE_CHAT_BASE_URL ?? "https://chat-hive.scubed.co";
const WORKSPACE = `${CHAT}/agent-workspace`;

// Must be a user with tenant role OWNER on a tenant whose ENABLE_COWORK
// feature gate is on, otherwise the console fails closed and renders the
// "not enabled for your organization" notice instead of the task panel
// (apps/agent-console/lib/edge-api/gate.ts).
const AGENT_EMAIL = process.env.HIVE_QA_AGENT_EMAIL ?? "";
const AGENT_PASSWORD = process.env.HIVE_QA_AGENT_PASSWORD ?? "";
const HAS_AGENT_CREDS = !!(AGENT_EMAIL && AGENT_PASSWORD);

async function signIn(page: Page) {
  await page.goto(`${WORKSPACE}/auth/sign-in`);
  await page.locator("#email").fill(AGENT_EMAIL);
  await page.locator("#password").fill(AGENT_PASSWORD);
  await page.click('button[type="submit"]');
  await page.waitForURL((url) => url.pathname === "/agent-workspace/tasks", {
    timeout: 25000,
  });
}

test("unauthenticated workspace entry redirects to the prefixed sign-in page", async ({
  page,
}) => {
  await page.goto(WORKSPACE);
  // The whole redirect chain must stay inside /agent-workspace. A target that
  // drops the basePath leaves Caddy's route for this app and is answered by
  // Open WebUI's catch-all instead.
  await expect(page).toHaveURL(`${WORKSPACE}/auth/sign-in`);
  await expect(page.getByRole("heading", { name: "Agent workspace" })).toBeVisible();
});

test("sign-in lands on the task console and not on the chat app", async ({ page }) => {
  test.skip(!HAS_AGENT_CREDS, "HIVE_QA_AGENT_EMAIL / HIVE_QA_AGENT_PASSWORD not set");
  await signIn(page);

  // Regression guard: this used to resolve to a bare /tasks, which Open WebUI
  // answered by redirecting to its own /auth login page.
  await expect(page).toHaveURL(`${WORKSPACE}/tasks`);
  await expect(page.getByRole("heading", { name: "Start a task" })).toBeVisible();
});

test("task console loads the task list instead of erroring", async ({ page }) => {
  test.skip(!HAS_AGENT_CREDS, "HIVE_QA_AGENT_EMAIL / HIVE_QA_AGENT_PASSWORD not set");
  await signIn(page);

  // Regression guard for the cross-origin fetch: edge-api serves no CORS
  // headers, so a non-relative base URL failed preflight and the panel
  // rendered this error for every list, create, and cancel call.
  await expect(page.getByText("Could not load tasks.")).toHaveCount(0);
  await expect(page.getByRole("region", { name: "Tasks" })).toBeVisible();
});

test("a coding-pack task can be started and cancelled", async ({ page }) => {
  test.skip(!HAS_AGENT_CREDS, "HIVE_QA_AGENT_EMAIL / HIVE_QA_AGENT_PASSWORD not set");
  await signIn(page);

  const tasks = page.getByRole("region", { name: "Tasks" });
  await page.getByRole("button", { name: "Start coding pack" }).click();

  const task = tasks.getByRole("listitem").first();
  await expect(task).toContainText("Coding pack");
  // "queued" is the correct resting state wherever the Apptainer sandbox is
  // not configured on the control-plane host: agenttask.NotConfiguredEngine
  // persists the task and leaves it queued rather than failing the request.
  await expect(task).toContainText("queued", { timeout: 15000 });

  await task.getByRole("button", { name: "Cancel" }).click();
  await expect(task).toContainText("cancelled", { timeout: 15000 });
  // Cancel is offered only while the task is still cancellable.
  await expect(task.getByRole("button", { name: "Cancel" })).toHaveCount(0);
});

test("agent-task API is reachable on the chat origin and still requires auth", async ({
  request,
}) => {
  // The task console calls this path same-origin. It must be routed here (not
  // 404) and must reject an unauthenticated caller (not 200).
  const response = await request.get(`${CHAT}/v1/agent/tasks`);
  expect(response.status()).toBe(401);
});
