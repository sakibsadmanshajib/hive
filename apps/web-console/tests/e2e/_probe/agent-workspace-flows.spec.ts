import { randomUUID } from "node:crypto";

import { expect, test, type APIRequestContext, type BrowserContext, type Page } from "@playwright/test";

import { signInToChat } from "../support/live-auth";

// agent-workspace-controls.json is read by scripts/build-agent-workspace-
// coverage.mjs, which cross-checks every [C#] tag below against it (a
// declared control with no matching test in the run fails loudly, and so
// does a tag with no declared control). This file does not import it itself:
// unlike the retired suite's C23/C24, no test here re-derives a ledger
// entry's `dom` string from a live sweep (see the controls file's own
// not_present entries for why), so there is nothing here for the JSON to
// feed.

/*
 * Live interaction-coverage probe for Cowork, the agent surface. Retired
 * apps/agent-console (a standalone app reverse-proxied at
 * https://chat-hive.scubed.co/agent-workspace) is NOT what this file measures
 * any more, and was never repointed to it either: both /agent-workspace and
 * /agent-workspace/auth/sign-in return 404, confirmed live 2026-08-29
 * (deploy/docker/Caddyfile.owui's @removedSurfaces block; PR #951; D-045).
 * Cowork moved INTO the chat composer as a mode (issue #944), and its pack
 * selector shipped there in #1500. This file was rewritten to test that
 * surface, not to relax its assertions to agree with the 404: every claim
 * below is something the composer actually does today, cited against the
 * source that does it, and every one is written so a regression in that
 * source fails the matching test here.
 *
 * WHERE THE EVIDENCE COMES FROM (file:line, not guesswork)
 * ---------------------------------------------------------
 * - vendor/open-webui/src/lib/hive/ComposerModeToggle.svelte: the `Chat |
 *   Cowork` radiogroup, `data-hive-composer-mode` on the group,
 *   `data-hive-mode="chat"|"cowork"` on each segment.
 * - vendor/open-webui/src/lib/hive/ComposerCoworkRow.svelte: the pack row that
 *   only renders in Cowork mode, `data-hive-cowork-row` on the row,
 *   `data-hive-composer-pack` on its own radiogroup, `data-hive-pack` per
 *   segment.
 * - vendor/open-webui/src/lib/components/chat/Chat.svelte (submitHandler,
 *   submitCoworkRun, applyCoworkRun, followCoworkRun): Cowork mode decides
 *   what a submit does; a run is created via createTask and rendered as an
 *   assistant turn in the SAME transcript, not on a separate page.
 * - vendor/open-webui/src/lib/hive/coworkMode.ts (renderRun): the turn's
 *   content while a run is in flight is literally "Queued. Waiting for a
 *   sandbox." or "Working on it.", and its settled content is the run's own
 *   summary or error.
 * - There is no Cancel control on this surface (see agent-workspace-
 *   controls.json's not_present entry) and no separate task-list page: a run
 *   IS a conversation (D-045), so containment here proves the server-side
 *   cancel contract directly against the API, the same way the retired
 *   suite's C17 did, without a button to click.
 *
 * AUTHENTICATION. ENABLE_LOGIN_FORM is false and OAUTH_AUTO_REDIRECT is true
 * on this deployment (deploy/docker/docker-compose.yml, the open-webui
 * service): there is no local email/password form anywhere on this surface,
 * unauthenticated or authenticated, and an unauthenticated visitor is bounced
 * toward the Hive OIDC provider rather than shown a button to click. The
 * unauthenticated group below asserts what holds regardless of exactly where
 * that bounce lands (C25, C26) rather than a specific DOM on a page the app
 * does not linger on; see agent-workspace-controls.json's second not_present
 * entry for why a sign-in-screen DOM sweep is not attempted. The authenticated
 * group signs in through tests/e2e/support/live-auth.ts's signInToChat, which
 * mints a session with the admin one-time-token flow (docs/live-test-auth.md)
 * and walks the real "Continue with Hive" hop; it needs no password and
 * changes none, and it already tolerates the auto-redirect skipping the
 * button entirely.
 *
 * TASK LIFECYCLE. Since PR #870 the demo box runs real agent tasks through an
 * unprivileged host launcher, so a submitted task moves queued, then running,
 * then succeeded over roughly sixteen minutes. POST /api/v1/hive/agent/tasks
 * (the chat-origin proxy, deploy/docker/owui-patches/hive_agent_proxy.py) is
 * synchronous over the launch (control-plane's CreateTask calls Engine.Launch
 * inline, bounded at five minutes), so a create can take tens of seconds
 * before it answers with a running task. The timeouts below are sized for
 * that, not for a stub.
 *
 * CONTAINMENT. This suite writes to a live deployment, so the complete set of
 * writes it can make is stated here rather than inferred:
 *
 *   1. POST /api/v1/hive/agent/tasks, exactly once per run, which is the
 *      control under test. Cancelled in a `finally` whatever happens.
 *   2. POST /api/v1/hive/agent/tasks/{id}/cancel, on that one task.
 *   3. admin generate_link plus verify, inside live-auth.mjs. Touches no
 *      password. It moves the account's sign-in timestamps, which any login
 *      does, and nothing else (evidence:
 *      docs/proof/live-auth-helper-2026-08-08/README.md).
 *
 * That is the whole list. Cancel here is a bare database transition
 * (control-plane's Engine interface has only Launch), so it does not free the
 * sandbox's concurrency slot (issue #886), same limitation the retired suite
 * carried and same reason this suite creates exactly one task per run.
 */
const CHAT = process.env.HIVE_CHAT_BASE_URL ?? "https://chat-hive.scubed.co";
// The console origin: where live-auth.mjs mints the Supabase session, and
// where Caddy fronts GoTrue at /auth/v1 (PR #992). The job passes this same
// value as SUPABASE_URL (see deploy-demo-box.yml's agent-workspace-coverage
// job, which names console-hive.scubed.co rather than a deleted hosted
// project, issue #1059), so it is read from there rather than duplicated
// under a second name that could drift from it.
const CONSOLE_URL = process.env.SUPABASE_URL ?? "https://console-hive.scubed.co";
const TASKS_ENDPOINT = `${CHAT}/api/v1/hive/agent/tasks`;

/*
 * Must be a user with tenant role OWNER on a tenant whose ENABLE_COWORK
 * feature gate is on, otherwise the composer's Cowork submit fails closed.
 *
 * #848. This USED to default silently to `demo@hive-demo.invalid` "to keep
 * the suite runnable by hand with no setup." That default is exactly what put
 * a wall of undeletable "interaction-coverage proof ..." rows onto the live
 * account the owner demos to prospects (issue #848). Fails hard instead of
 * defaulting: set HIVE_QA_AGENT_EMAIL to an identity dedicated to this
 * measurement, never let it fall back onto the shared demo account by
 * omission.
 */
const AGENT_EMAIL = process.env.HIVE_QA_AGENT_EMAIL ?? "";
const AGENT_EMAIL_FAILURE_REASON =
  "HIVE_QA_AGENT_EMAIL not set. This suite creates a real, currently undeletable agent " +
  "task on whatever account it signs in as (issue #848), so it must never silently fall " +
  "back to the shared demo account. Point it at an identity dedicated to this measurement.";

/*
 * live-auth.mjs mints against these three. Without them there is no session
 * and no honest way to get one. A HARD failure, deliberately, not a skip: a
 * skip here is the same defect the retired suite was rewritten to remove,
 * just moved into the numerator (a renamed secret would quietly drop every
 * authenticated control out of the run and the job would report a cheerful
 * green over a shrunken denominator instead).
 */
const MINT_ENV = ["SUPABASE_URL", "SUPABASE_SERVICE_ROLE_KEY", "SUPABASE_ANON_KEY"];
const MISSING_MINT_ENV = MINT_ENV.filter((name) => !(process.env[name] ?? "").trim());
const MINT_FAILURE_REASON =
  `cannot mint a live session: ${MISSING_MINT_ENV.join(", ")} not set. ` +
  "See docs/live-test-auth.md. There is no credential-rotating fallback and there must never " +
  "be one. This fails rather than skips on purpose: a skipped control is not a proven control, " +
  "and a run that cannot authenticate has not measured this surface at all.";

function requireMintEnv(): void {
  expect(MISSING_MINT_ENV, MINT_FAILURE_REASON).toEqual([]);
  expect(AGENT_EMAIL, AGENT_EMAIL_FAILURE_REASON).not.toEqual("");
}

/** Signs a fresh context in and returns a page sitting on the chat home. */
async function signIn(context: BrowserContext): Promise<Page> {
  return signInToChat(context, {
    email: AGENT_EMAIL,
    consoleUrl: CONSOLE_URL,
    chatUrl: CHAT,
  });
}

const modeToggle = (page: Page) => page.locator("[data-hive-composer-mode]");
const modeSegment = (page: Page, mode: "chat" | "cowork") => page.locator(`[data-hive-mode="${mode}"]`);
const coworkRow = (page: Page) => page.locator("[data-hive-cowork-row]");
const packGroup = (page: Page) => page.locator("[data-hive-composer-pack]");
const packSegment = (page: Page, pack: "knowledge-work-pack" | "coding-pack") =>
  page.locator(`[data-hive-pack="${pack}"]`);

/** Selects Cowork and waits for the pack row's own group to confirm it rendered. */
async function switchToCowork(page: Page): Promise<void> {
  await modeSegment(page, "cowork").click();
  await expect(modeToggle(page)).toHaveAttribute("data-hive-composer-mode", "cowork");
  await expect(packGroup(page)).toBeVisible();
}

const TASK_LIST_GLOB = "**/api/v1/hive/agent/tasks";

/*
 * The collection endpoint EXACTLY, path and all, not a substring of it.
 * `url().includes(TASKS_ENDPOINT)` also matches `.../tasks/{id}/cancel`, so a
 * predicate built on it would pick the cancel call out of the network stream
 * just as happily as the create.
 */
const isTasksCollection = (url: string): boolean => new URL(url).pathname === "/api/v1/hive/agent/tasks";

interface AgentTaskWire {
  id?: string;
  instructions?: string;
  status?: string;
}

interface AgentTaskListWire {
  tasks?: AgentTaskWire[];
}

/**
 * Arms a waiter so its rejection can only ever be delivered at its `await`.
 * See the retired suite's own comment on this pattern (issue #886): a waiter
 * armed after the action it observes races that action, and an unhandled
 * rejection during that window aborts the test before `finally` can run,
 * orphaning the sandbox this block exists to reclaim.
 */
function armed<T>(waiter: Promise<T>): Promise<T> {
  waiter.catch(() => undefined);
  return waiter;
}

/** Finds a task by the exact brief it was created with, straight from the API. Cleanup-only, swallows everything. */
async function findTaskIdByBrief(
  request: APIRequestContext,
  authHeader: string,
  brief: string,
): Promise<string> {
  try {
    const listed = await request.get(TASKS_ENDPOINT, { headers: { Authorization: authHeader } });
    if (!listed.ok()) return "";
    const body: AgentTaskListWire = await listed.json();
    const match = (body.tasks ?? []).find((task) => task.instructions === brief);
    return typeof match?.id === "string" ? match.id : "";
  } catch {
    return "";
  }
}

test.describe("sign-in entry (no session required)", () => {
  test("[C25] unauthenticated chat entry never reaches signed-in app content", async ({ page }) => {
    await page.goto(CHAT);
    // Either Open WebUI's own client-side guard parks it on /auth
    // (routes/(app)/+layout.svelte's onMount redirect), or OAUTH_AUTO_REDIRECT
    // carries it off this origin entirely toward the Hive OIDC provider
    // before that page even settles. Both are refusals; a wait keyed on
    // leaving app content, rather than one exact URL, holds for either.
    await page.waitForURL(
      (u) => u.origin !== new URL(CHAT).origin || u.pathname.startsWith("/auth"),
      { timeout: 20000 },
    );
    await expect(
      page
        .getByRole("button", { name: /new chat/i })
        .or(page.getByRole("link", { name: /new chat/i })),
      "an unauthenticated visitor must never see the signed-in chat shell",
    ).toHaveCount(0);
  });

  test("[C26] the chat origin's config reports local sign-in off and the Hive provider configured", async ({
    request,
  }) => {
    const response = await request.get(`${CHAT}/api/config`);
    expect(response.ok(), `GET /api/config answered ${response.status()}`).toBe(true);
    const body: {
      features?: { enable_login_form?: boolean };
      oauth?: { providers?: Record<string, unknown> };
    } = await response.json();
    expect(
      body.features?.enable_login_form,
      "ENABLE_LOGIN_FORM must be false on this deployment (deploy/docker/docker-compose.yml): " +
        "every account here is Hive-OIDC-only and has no Open WebUI password to submit through a " +
        "local form",
    ).toBe(false);
    expect(
      body.oauth?.providers?.oidc,
      "the generic oidc provider (OAUTH_PROVIDER_NAME=Hive) must still be configured, or nothing " +
        "on this deployment can sign in at all",
    ).toBeTruthy();
  });

  test("[C27] the agent task API requires auth on the chat origin (401 unauthenticated)", async ({
    request,
  }) => {
    const response = await request.get(TASKS_ENDPOINT);
    expect(response.status()).toBe(401);
  });
});

test.describe("authenticated composer (Cowork mode)", () => {
  test.beforeEach(requireMintEnv);

  test("[C28] a minted session lands on the chat home, not stuck outside the app", async ({
    context,
  }) => {
    const page = await signIn(context);
    expect(new URL(page.url()).origin).toBe(new URL(CHAT).origin);
    await expect(
      page
        .getByRole("button", { name: /new chat/i })
        .or(page.getByRole("link", { name: /new chat/i }))
        .first(),
    ).toBeVisible();
  });

  test("[C29] the Chat/Cowork toggle defaults to Chat and selecting Cowork reveals the pack row", async ({
    context,
  }) => {
    const page = await signIn(context);
    await expect(modeToggle(page)).toHaveAttribute("data-hive-composer-mode", "chat");
    await expect(coworkRow(page)).toHaveCount(0);
    await switchToCowork(page);
    await expect(coworkRow(page)).toBeVisible();
    // Switching back is the other half of the same claim: the control is a
    // real toggle, not a one-way reveal.
    await modeSegment(page, "chat").click();
    await expect(modeToggle(page)).toHaveAttribute("data-hive-composer-mode", "chat");
    await expect(coworkRow(page)).toHaveCount(0);
  });

  test("[C30] the pack row defaults to Knowledge work and reflects selection", async ({ context }) => {
    const page = await signIn(context);
    await switchToCowork(page);
    await expect(packGroup(page)).toHaveAttribute("data-hive-composer-pack", "knowledge-work-pack");
    await expect(packSegment(page, "knowledge-work-pack")).toHaveAttribute("aria-checked", "true");
    await packSegment(page, "coding-pack").click();
    await expect(packGroup(page)).toHaveAttribute("data-hive-composer-pack", "coding-pack");
    await expect(packSegment(page, "coding-pack")).toHaveAttribute("aria-checked", "true");
    await expect(packSegment(page, "knowledge-work-pack")).toHaveAttribute("aria-checked", "false");
  });

  test("[C31] blank instructions in Cowork mode are refused, no request fires", async ({ context }) => {
    const page = await signIn(context);
    await switchToCowork(page);
    let created = false;
    await page.route(TASK_LIST_GLOB, (route) => {
      if (route.request().method() === "POST") created = true;
      return route.continue();
    });
    // A single space, not an empty string: submitHandler's first guard
    // ("Please enter a prompt") only fires on a literally empty prompt with
    // no files, and that guard applies to both modes identically. The
    // Cowork-only refusal this control is about ("Please enter instructions
    // for Cowork", Chat.svelte's submitHandler) checks `userPrompt.trim() ===
    // ''`, so it needs a prompt that IS non-empty and trims to nothing.
    await page.locator("#chat-input").fill(" ");
    await page.keyboard.press("Enter");
    // Only "enter instructions for Cowork" is reachable here, not "enter a
    // prompt": the comment above the fill establishes the input is a single
    // space, not an empty string, so submitHandler's first guard (userPrompt
    // === '') never fires and only the Cowork-specific trim() === '' guard
    // can produce a message. An OR with the unreachable branch would still
    // pass today and would silently loosen the guarantee below what the cited
    // source actually promises.
    await expect(
      page.getByText(/enter instructions for work/i),
      "a whitespace-only Cowork submission must be refused client side with a visible message",
    ).toBeVisible();
    expect(created, "no request should reach the agent task collection for a refused submission").toBe(
      false,
    );
  });

  test(
    "[C32] Cowork submission creates a real task, the transcript shows the run, and it is cancelled server side",
    async ({ context, request }) => {
      /*
       * One create per run, on purpose; see this file's CONTAINMENT comment.
       * There is no Cancel button on this surface (agent-workspace-
       * controls.json's not_present), so cleanup calls the API directly
       * rather than clicking one.
       */
      test.setTimeout(360_000);
      const page = await signIn(context);
      await switchToCowork(page);

      const brief = `interaction-coverage proof ${Date.now()} ${randomUUID()}`;
      const createRequest = armed(
        page.waitForRequest((req) => isTasksCollection(req.url()) && req.method() === "POST", {
          timeout: 30_000,
        }),
      );
      const createResponse = armed(
        page.waitForResponse(
          (res) => isTasksCollection(res.url()) && res.request().method() === "POST",
          { timeout: 330_000 },
        ),
      );
      await page.locator("#chat-input").fill(brief);
      await page.keyboard.press("Enter");

      const submitted = await createRequest;
      const authHeader = submitted.headers()["authorization"];
      let createdId = "";

      try {
        const response = await createResponse;
        expect(
          response.request() === submitted,
          "the awaited response must belong to the create request this submit issued, not merely " +
            "to the next matching POST that happened to arrive",
        ).toBe(true);
        // Asserted as a boolean rather than with toMatch, so a failure prints
        // "false" instead of echoing a live bearer token into the report.
        expect(
          typeof authHeader === "string" && authHeader.startsWith("Bearer "),
          "the create request must carry a bearer token, otherwise the cancel below would be " +
            "asserting against an unauthenticated 401",
        ).toBe(true);
        // Soft: the transcript and the cleanup below still have to run even if
        // this one status disagrees, so the task this created is still
        // cancelled rather than left holding a sandbox on a shared box.
        expect
          .soft(response.status(), "POST /api/v1/hive/agent/tasks must answer 201")
          .toBe(201);

        if (response.ok()) {
          const created: AgentTaskWire | null = await response.json().catch(() => null);
          if (typeof created?.id === "string") createdId = created.id;
        }

        // The load-bearing claim of #944: a run renders as transcript text in
        // the SAME conversation, not on a separate page (coworkMode.ts's
        // renderRun: "Queued. Waiting for a sandbox." then "Working on it."
        // while in flight).
        await expect(
          page.getByText(/Queued\. Waiting for a sandbox\.|Working on it\./),
          "the submitted run must render as a transcript turn in this conversation",
        ).toBeVisible({ timeout: 30_000 });

        if (!createdId) {
          createdId = await findTaskIdByBrief(request, authHeader, brief);
        }
        expect(
          createdId,
          "the submitted task must be readable back from the API, otherwise nothing was created",
        ).not.toBe("");
      } finally {
        if (!createdId) {
          createdId = await findTaskIdByBrief(request, authHeader, brief);
        }
        if (createdId) {
          await request
            .post(`${TASKS_ENDPOINT}/${createdId}/cancel`, { headers: { Authorization: authHeader } })
            .catch(() => undefined);
        }
      }
    },
  );

  test("[C33] the composer's Cowork controls are exactly two modes and, once selected, two packs", async ({
    context,
  }) => {
    const page = await signIn(context);
    await expect(
      page.locator("[data-hive-mode]"),
      "the Chat/Cowork toggle must carry exactly two segments; a third means a control shipped " +
        "with no entry in agent-workspace-controls.json",
    ).toHaveCount(2);
    await switchToCowork(page);
    await expect(
      page.locator("[data-hive-pack]"),
      "the pack row must carry exactly two segments; a third means a control shipped with no " +
        "entry in agent-workspace-controls.json",
    ).toHaveCount(2);
  });
});
