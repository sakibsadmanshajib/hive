// Visual proof of the Cowork task surface against a REAL stack: a booted
// edge-api and control-plane, and an agent-engine launch daemon that starts
// an actual Apptainer sandbox per task. The sibling capture.mjs proves
// rendering against stubs; this one proves behaviour that only exists when
// something really launches.
//
//   node apps/agent-console/proof/harness/capture-live.mjs
//
// Driven by .github/workflows/agent-visual-proof.yml, which stands the whole
// thing up. It is a plain script, not a Playwright project, on purpose: the
// web-console Playwright projects boot no launcher and would report this
// suite as skipped, which is indistinguishable from passing.
//
// ponytail: no test runner. Scenarios are functions in an array, the same
// shape capture.mjs already uses.
//
// WHAT IT NEVER DOES
//   * Rotate a password. The session comes from the admin one-time-token
//     flow in live-auth.mjs (docs/live-test-auth.md).
//   * Print a secret. Every line it writes goes through redactSecrets, and
//     the screenshot stamp carries no URL (see stamp.mjs).

import { mkdirSync, readFileSync, readdirSync, rmSync, writeFileSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

import { loadChromium, shoot as shootStamped, waitForHttp } from "./stamp.mjs";
import { redactSecrets } from "../../../web-console/tests/e2e/support/e2e-fixture-seed.mjs";
import { mintSession, sessionCookies } from "../../../web-console/tests/e2e/support/live-auth.mjs";

const HERE = dirname(fileURLToPath(import.meta.url));
const BASE_PATH = "/agent-workspace";

function requiredEnv(name, why) {
  const value = (process.env[name] ?? "").trim();
  if (!value) {
    throw new Error(`${name} is required: ${why}`);
  }
  return value;
}

// The origin that serves the console and proxies /v1/agent/* to edge-api, so
// the browser's fetches stay same-origin exactly as they do behind
// Caddyfile.owui in production.
const BASE_URL = (process.env.PROOF_BASE_URL || "http://127.0.0.1:3030").replace(/\/$/, "");
const EMAIL = requiredEnv("PROOF_EMAIL", "the run-scoped fixture address to mint a session for");
// The launcher's state directory (RUNTIME_DIR in
// scripts/install-agent-engine-host.sh). Its sessions/ subdirectory is the
// slot count this proof turns on; see liveSessions below.
const RUNTIME_DIR = requiredEnv(
  "PROOF_RUNTIME_DIR",
  "the agent-engine launcher's runtime directory, whose sessions/ entries are the observable slot count",
);
const OUT_DIR = resolve(process.env.PROOF_OUT || join(HERE, "captures-live"));
const NOTE = process.env.PROOF_NOTE || "";
const SELECTED = (process.env.PROOF_SCENARIOS || "all").trim();
// Every task this run creates carries it, so a row is addressable in the UI
// and so a leftover row is traceable to the run that made it.
const RUN_TAG = process.env.PROOF_RUN_TAG || `local-${Date.now()}`;

// #881's bound, and the reason this proof exists: edge-api's control-plane
// client gives up at 15 seconds, so a create that waits for a cold sandbox
// answers 500 to the browser. Kept slightly under the real ceiling so a
// create that only just squeaks in still reads as the bug.
const CREATE_BUDGET_MS = 14_000;

const logLines = [];
function log(line) {
  const safe = redactSecrets(String(line));
  logLines.push(safe);
  console.log(`[capture-live] ${safe}`);
}

/**
 * The launcher's in-use concurrency slots, counted from outside the process.
 *
 * SandboxEngine.Launch creates exactly one directory here per live session
 * (os.MkdirTemp under RunDir) and reap() removes it in the same call that
 * runs the quota release, so this number is the slot count rather than a
 * proxy for it. That pairing is the whole point: a cancelled task's status
 * badge turning grey proves a row was written, not that anything was freed.
 */
function liveSessions() {
  try {
    return readdirSync(join(RUNTIME_DIR, "sessions")).length;
  } catch (err) {
    throw new Error(`cannot read the launcher's session directory: ${err.message}`);
  }
}

async function waitForSessionsAtLeast(want, timeoutMs, what) {
  const deadline = Date.now() + timeoutMs;
  for (;;) {
    const now = liveSessions();
    if (now >= want) return now;
    if (Date.now() > deadline) {
      throw new Error(`${what}: launcher never reached ${want} live session(s), saw ${now}`);
    }
    await new Promise((r) => setTimeout(r, 1000));
  }
}

async function waitForSessions(want, timeoutMs, what) {
  const deadline = Date.now() + timeoutMs;
  for (;;) {
    const now = liveSessions();
    if (now === want) return now;
    if (Date.now() > deadline) {
      throw new Error(`${what}: launcher still reports ${now} live session(s), wanted ${want}`);
    }
    await new Promise((r) => setTimeout(r, 1000));
  }
}

const shots = [];
async function shoot(page, scenario, name, extra = "") {
  const note = [NOTE, extra].filter(Boolean).join("  ·  ");
  const file = await shootStamped(page, { outDir: OUT_DIR, scenario, name, note });
  shots.push(file);
  log(`wrote ${file}`);
  return file;
}

function taskRow(page, instructions) {
  return page.getByRole("listitem").filter({ hasText: instructions });
}

/**
 * Fills the composer, submits, and reports what the create call actually
 * answered and how long it took. The response is read from the wire rather
 * than from the rendered row: #881 is a status code and a latency, and a
 * screenshot of a row cannot show either.
 */
async function createTask(page, instructions) {
  const started = Date.now();
  const responsePromise = page.waitForResponse(
    (r) => r.request().method() === "POST" && new URL(r.url()).pathname === "/v1/agent/tasks",
    { timeout: 120_000 },
  );
  await page.getByLabel("What should the agent do?").fill(instructions);
  await page.getByRole("button", { name: /start task/i }).click();
  const response = await responsePromise;
  const elapsedMs = Date.now() - started;
  let body = {};
  try {
    body = await response.json();
  } catch {
    // A 500 from the timeout path is not JSON; the status carries the story.
  }
  log(
    `create answered HTTP ${response.status()} in ${elapsedMs}ms, status=${body.status ?? "n/a"}`,
  );
  return { status: response.status(), elapsedMs, id: body.id, taskStatus: body.status };
}

async function waitForRowStatus(page, instructions, label, timeoutMs) {
  // A string is matched exactly; a RegExp is for the states the console
  // renders under more than one label.
  const target =
    label instanceof RegExp
      ? taskRow(page, instructions).getByText(label)
      : taskRow(page, instructions).getByText(label, { exact: true });
  await target.waitFor({ timeout: timeoutMs });
  log(`row "${instructions.slice(0, 40)}…" reached ${await target.innerText()}`);
}

/*
 * A launch the engine refused reads as "Blocked", not "Failed": the console
 * gives ENGINE_LAUNCH_FAILED_MESSAGE its own label so a deployment problem
 * does not read to the user as a problem with what they asked for (see
 * isEngineLaunchFailure in lib/edge-api/tasks.ts). Both are terminal and both
 * mean the same thing here, so the refusal wait accepts either rather than
 * pinning the proof to today's copy.
 */
const REFUSED = /^(Blocked|Failed)$/;

async function cancelRow(page, instructions) {
  await taskRow(page, instructions).getByRole("button", { name: "Cancel" }).click();
}

const SCENARIOS = [
  {
    name: "launch-liveness",
    /*
     * The floor every other scenario stands on, and the one that fails when
     * the plumbing is wrong rather than when the product is: a task created
     * in the browser reaches Running because a real Apptainer sandbox came
     * up for it on the host. It asserts nothing about #881 or #886, so it
     * passes on a tree that predates both, which is what makes it usable as
     * this workflow's own self-test.
     */
    async run(page) {
      const instructions = `${RUN_TAG} liveness: list the files in the workspace and stop.`;
      await shoot(page, "launch-liveness", "01-empty-console", `launcher slots in use=${liveSessions()}`);

      const created = await createTask(page, instructions);
      /*
       * The launcher's own session count is the assertion, not the row's
       * status, and deliberately so. On a tree that predates #881 the create
       * blocks on the launch, edge-api gives up at 15 seconds and cancels the
       * request, and control-plane can lose the row to that cancelled
       * context: the sandbox still started, but the row may never read
       * Running. Asserting on the row would make this scenario fail for the
       * bug it is not measuring. A session directory appearing means an
       * Apptainer sandbox really came up on this host, which is the one thing
       * every agent-surface change can be held to, and it is exactly what the
       * sabotage run removes.
       */
      const slots = await waitForSessionsAtLeast(
        1,
        300_000,
        "after creating a task from the console",
      );
      await shoot(
        page,
        "launch-liveness",
        "02-sandbox-launched",
        `launcher slots in use=${slots} · create HTTP ${created.status} in ${created.elapsedMs}ms`,
      );

      // Best effort: a row that already reached a terminal state has no
      // Cancel control, and that is not this scenario's business.
      const cancel = taskRow(page, instructions).getByRole("button", { name: "Cancel" });
      if (await cancel.count()) {
        await cancel.click();
        await waitForRowStatus(page, instructions, "Cancelled", 120_000);
      }
    },
  },
  {
    name: "create-returns-queued",
    /*
     * Issue #881. Create answers with the persisted queued task instead of
     * blocking on the launch, so a cold sandbox (tens of seconds) no longer
     * outlives edge-api's 15 second client bound and 500s an interactive
     * request. Measured on the wire, then screenshotted with the measurement
     * stamped into the image.
     */
    async run(page) {
      const instructions = `${RUN_TAG} create: summarise the repository README in three bullets.`;
      const created = await createTask(page, instructions);

      const failures = [];
      if (created.status !== 201) failures.push(`create answered HTTP ${created.status}, want 201`);
      if (created.taskStatus !== "queued") {
        failures.push(`create body status was ${created.taskStatus ?? "absent"}, want queued`);
      }
      if (created.elapsedMs > CREATE_BUDGET_MS) {
        failures.push(`create took ${created.elapsedMs}ms, over the ${CREATE_BUDGET_MS}ms budget`);
      }
      await shoot(
        page,
        "create-returns-queued",
        "01-queued-immediately",
        `HTTP ${created.status} in ${created.elapsedMs}ms`,
      );
      if (failures.length) throw new Error(failures.join("; "));

      // The launch still lands, it just lands on the read path.
      await waitForRowStatus(page, instructions, "Running", 300_000);
      await shoot(
        page,
        "create-returns-queued",
        "02-launch-landed-on-the-poll",
        `slots=${liveSessions()}`,
      );

      await cancelRow(page, instructions);
      await waitForRowStatus(page, instructions, "Cancelled", 120_000);
      await waitForSessions(0, 120_000, "after the cleanup cancel");
    },
  },
  {
    name: "cancel-frees-slot",
    /*
     * Issue #886. Needs HIVE_QUOTA_USER_CONCURRENCY=1 on the launcher, which
     * makes the ceiling observable in one task instead of two.
     *
     * The sequence is a differential: the same create is refused while the
     * slot is held and accepted once it is released, and the launcher's own
     * session count moves 1 -> 0 across the cancel. Before the fix, cancel
     * wrote a row and left the sandbox running, so the count stayed at 1 and
     * the second create stayed refused for the roughly sixteen minutes the
     * sandbox took to finish by itself.
     */
    async run(page) {
      const first = `${RUN_TAG} slot A: hold the only concurrency slot.`;
      const blocked = `${RUN_TAG} slot B: submitted while the slot is held.`;
      const after = `${RUN_TAG} slot C: submitted after the cancel.`;

      await createTask(page, first);
      await waitForRowStatus(page, first, "Running", 300_000);
      const held = await waitForSessions(1, 60_000, "with one task running");
      await shoot(page, "cancel-frees-slot", "01-slot-held", `launcher slots in use=${held}`);

      // The refusal arrives on the read path (the launch is asynchronous), so
      // this waits for the row rather than reading the create response.
      await createTask(page, blocked);
      await waitForRowStatus(page, blocked, REFUSED, 180_000);
      await shoot(
        page,
        "cancel-frees-slot",
        "02-second-create-refused",
        `launcher slots in use=${liveSessions()}`,
      );

      await cancelRow(page, first);
      await waitForRowStatus(page, first, "Cancelled", 120_000);
      const freed = await waitForSessions(0, 180_000, "after cancelling the running task");
      await shoot(
        page,
        "cancel-frees-slot",
        "03-slot-released-on-cancel",
        `launcher slots in use=${freed}`,
      );

      await createTask(page, after);
      await waitForRowStatus(page, after, "Running", 300_000);
      await shoot(
        page,
        "cancel-frees-slot",
        "04-next-task-launched",
        `launcher slots in use=${liveSessions()}`,
      );

      await cancelRow(page, after);
      await waitForRowStatus(page, after, "Cancelled", 120_000);
      await waitForSessions(0, 180_000, "after the cleanup cancel");
    },
  },
  {
    name: "attachment-reaches-the-sandbox",
    /*
     * Issue #1065. A document attached in the composer has to EXIST inside
     * the sandbox, not merely be accepted by a form, and the only place that
     * can be observed is a host where Apptainer really runs. This scenario is
     * the reason that distinction is checkable before merge rather than after.
     *
     * The console has no attachment control of its own: the control lives in
     * the chat composer, which is a different front end. So the create the
     * console makes is amended on the wire with an attachment, which
     * exercises every layer under the browser exactly as the composer's own
     * request does. The composer's half, that it assembles that request at
     * all, is proven separately in
     * docs/proof/cowork-attachment-1065-2026-09-02/.
     *
     * The assertion is the file's CONTENT, read off the launcher's own
     * workspace directory. A row, a 201 or a name in a list would all pass on
     * a tree where the bytes never arrive, which is the defect this whole
     * issue is.
     */
    async run(page) {
      const code = `HIVE-1065-${RUN_TAG.replace(/[^A-Za-z0-9]/g, "").slice(-8).toUpperCase()}`;
      const name = "service-record.txt";
      const content = `Hive demo box service record.\n\nRack asset tag: ${code}\n`;
      const instructions = `${RUN_TAG} attachment: read ${name} and state the rack asset tag.`;

      // The console attaches the session bearer in its own client, so the
      // header is taken off its create request rather than rebuilt here.
      // Never logged: it is a live credential and log() would only redact it
      // if it happened to match a known shape.
      let bearer = "";
      await page.route("**/v1/agent/tasks", async (route) => {
        const request = route.request();
        if (request.method() !== "POST") return route.fallback();
        bearer = request.headers()["authorization"] || "";
        const body = JSON.parse(request.postData() || "{}");
        body.attachments = [{ name, content }];
        log(`amended the create with 1 attachment (${Buffer.byteLength(content, "utf8")} bytes)`);
        await route.continue({ postData: JSON.stringify(body) });
      });

      const created = await createTask(page, instructions);
      if (created.status !== 201 || !created.id) {
        throw new Error(`create answered HTTP ${created.status} with id ${created.id ?? "absent"}`);
      }
      await waitForSessionsAtLeast(1, 300_000, "after creating a task carrying an attachment");

      const planted = join(RUNTIME_DIR, "workspaces", created.id, name);
      const deadline = Date.now() + 120_000;
      let landed = null;
      for (;;) {
        try {
          landed = readFileSync(planted, "utf8");
          break;
        } catch {
          if (Date.now() > deadline) {
            throw new Error(`the attachment never reached the sandbox workspace at ${planted}`);
          }
          await new Promise((r) => setTimeout(r, 1000));
        }
      }
      if (!landed.includes(code)) {
        throw new Error(`the workspace file exists but does not carry ${code}: ${JSON.stringify(landed)}`);
      }
      log(`the sandbox workspace holds ${name} carrying ${code}`);

      // The Working folder panel's own data, read through the customer route
      // rather than off disk, so the panel and the file agree.
      const listed = await page.evaluate(
        async ({ id, auth }) => {
          const res = await fetch(`/v1/agent/tasks/${id}/files`, {
            headers: { Accept: "application/json", ...(auth ? { Authorization: auth } : {}) },
          });
          return { status: res.status, body: await res.text() };
        },
        { id: created.id, auth: bearer },
      );
      log(`GET /v1/agent/tasks/{id}/files answered HTTP ${listed.status}: ${listed.body.slice(0, 400)}`);
      if (!listed.body.includes(name)) {
        throw new Error(`the working folder listing does not name ${name}: ${listed.body}`);
      }

      await shoot(
        page,
        "attachment-reaches-the-sandbox",
        "01-attachment-inside-the-sandbox",
        `${name} present in the sandbox workspace carrying ${code} \u00b7 listed in the working folder \u00b7 slots=${liveSessions()}`,
      );

      const cancel = taskRow(page, instructions).getByRole("button", { name: "Cancel" });
      if (await cancel.count()) {
        await cancel.click();
        await waitForRowStatus(page, instructions, "Cancelled", 120_000);
      }
      await page.unroute("**/v1/agent/tasks");
    },
  },
];

async function cancelLeftovers(page) {
  // A row left non-terminal outlives this run in a shared database, where
  // every control-plane poller keeps asking a launcher that no longer exists
  // about it. Best effort, and never the reason a run fails.
  for (let i = 0; i < 10; i += 1) {
    const buttons = page.getByRole("button", { name: "Cancel" });
    const count = await buttons.count().catch(() => 0);
    if (!count) return;
    await buttons.first().click().catch(() => {});
    await page.waitForTimeout(500);
  }
}

async function main() {
  rmSync(OUT_DIR, { recursive: true, force: true });
  mkdirSync(OUT_DIR, { recursive: true });

  const selected =
    SELECTED === "all"
      ? SCENARIOS
      : SCENARIOS.filter((s) => SELECTED.split(",").map((n) => n.trim()).includes(s.name));
  if (selected.length === 0) {
    throw new Error(`no scenario matched "${SELECTED}". Known: ${SCENARIOS.map((s) => s.name).join(", ")}`);
  }
  log(`scenarios: ${selected.map((s) => s.name).join(", ")}`);
  log(`launcher runtime dir: ${RUNTIME_DIR}, live sessions at start: ${liveSessions()}`);

  await waitForHttp(`${BASE_URL}${BASE_PATH}/tasks`, 120_000, "the console origin");

  const browser = await loadChromium().launch();
  const context = await browser.newContext({ viewport: { width: 1280, height: 1000 } });
  // Mints a one-shot token for an EXISTING account and exchanges it. No
  // password is read, written or rotated.
  //
  // reauthenticate() inlined rather than called, for one reason: it would
  // build the cookies with the same project identity it minted with, and
  // @supabase/ssr names its cookie after the project ref in the URL it is
  // given. The browser bundle is built with NEXT_PUBLIC_SUPABASE_URL, so a
  // deployment where that differs from the admin SUPABASE_URL gets a cookie
  // under the wrong name and a console that is simply signed out.
  const session = await mintSession({ email: EMAIL });
  await context.addCookies(
    await sessionCookies(session, `${BASE_URL}${BASE_PATH}/tasks`, {
      supabaseUrl: process.env.NEXT_PUBLIC_SUPABASE_URL || process.env.SUPABASE_URL,
      anonKey: process.env.NEXT_PUBLIC_SUPABASE_ANON_KEY || process.env.SUPABASE_ANON_KEY,
    }),
  );
  log(`signed in as user ${session.userId}`);
  /*
   * Claim names and the tenant claim, never the token. edge-api resolves the
   * caller's tenant from this claim and answers 403 without it, which is the
   * difference between "the gate is off" (a row) and "this session has no
   * tenant" (a token), and the two are indistinguishable from the rendered
   * page. A user id and a tenant id are identifiers, not credentials, and the
   * user id is already logged above.
   */
  const claims = JSON.parse(
    Buffer.from(session.access_token.split(".")[1], "base64url").toString(),
  );
  log(`token claims: ${Object.keys(claims).sort().join(",")}`);
  log(
    `token tenant claim: ${claims.selected_tenant_id ?? claims.tenant_id ?? "ABSENT"}` +
      ` · role=${claims.role ?? "-"} · aal=${claims.aal ?? "-"}`,
  );

  const page = await context.newPage();
  page.on("pageerror", (err) => log(`[browser pageerror] ${err.message}`));
  await page.goto(`${BASE_URL}${BASE_PATH}/tasks`, { waitUntil: "domcontentloaded" });
  // Waited for, not counted once: the console is a client component, so an
  // immediate count races the first render and would report a working surface
  // as missing.
  try {
    await page.getByLabel("What should the agent do?").waitFor({ timeout: 60_000 });
  } catch {
    // Say which of the two it was, instead of leaving the next run to guess.
    // The path only, never the full URL: an auth redirect carries its
    // credential in the fragment.
    await shoot(page, "setup", "00-no-composer").catch(() => {});
    log(`landed on ${new URL(page.url()).pathname}`);
    // What edge-api answers for this exact session. Paired with the
    // control-plane assertion the workflow already made, this says which hop
    // dropped the gate: same answer means the row is off, a disagreement
    // means the token's tenant claim is.
    const gates = await context.request
      .get(`${BASE_URL}/v1/featuregate`, {
        headers: { Authorization: `Bearer ${session.access_token}` },
      })
      .then(async (r) => `HTTP ${r.status()} ${(await r.text()).slice(0, 300)}`)
      .catch((err) => `unreachable: ${err.message}`);
    log(`edge-api /v1/featuregate for this session: ${gates}`);
    const text = await page
      .locator("body")
      .innerText()
      .catch(() => "");
    log(`page said: ${text.replace(/\s+/g, " ").slice(0, 400)}`);
    throw new Error(
      "the task composer never rendered: the session is not signed in, or ENABLE_COWORK is off " +
        "for this tenant, so there is no Cowork surface to prove",
    );
  }

  let failure = null;
  for (const scenario of selected) {
    log(`--- scenario: ${scenario.name} ---`);
    try {
      await scenario.run(page);
      log(`scenario ${scenario.name}: ok`);
    } catch (err) {
      failure = failure ?? err;
      log(`scenario ${scenario.name}: FAILED ${err.message}`);
      // A failure capture is evidence too: it is what the missing fix looks
      // like. It is not counted as proof, because the run still exits non-zero.
      await shoot(page, scenario.name, "FAILED", err.message.slice(0, 120)).catch(() => {});
      break;
    }
  }

  await cancelLeftovers(page).catch(() => {});
  await context.close();
  await browser.close();

  writeFileSync(join(OUT_DIR, "proof-log.txt"), `${logLines.join("\n")}\n`);

  // A run that captured nothing must never read as a pass. This is the
  // property the workflow's deliberate-sabotage run exercises.
  if (!failure && shots.length === 0) {
    throw new Error("no screenshots were captured, so there is no proof here");
  }
  if (failure) throw failure;
  log(`captured ${shots.length} screenshots into ${OUT_DIR}`);
}

process.on("unhandledRejection", (err) => {
  console.error("[capture-live] unhandled rejection:", err);
  process.exit(1);
});

main().catch((err) => {
  try {
    writeFileSync(join(OUT_DIR, "proof-log.txt"), `${logLines.join("\n")}\n${err.message}\n`);
  } catch {
    // the throw below is the report either way
  }
  console.error("[capture-live] failed:", err.message);
  process.exit(1);
});
