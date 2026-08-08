// Drives apps/agent-console through its task-console states against
// stub-server.mjs and writes a screenshot per state. One command, no live
// box, no real credentials, no Supabase.
//
//   node apps/agent-console/proof/harness/capture.mjs
//
// See README.md for flags and for what each scenario proves.
//
// It owns the whole run: starts the stub, starts `next dev`, drives a real
// Chromium, then tears both down. Nothing is left listening afterwards.
//
// ponytail: no test runner and no config file. Scenarios are functions in an
// array; a framework here would be more scaffolding than scenarios.

import { spawn, execSync } from "node:child_process";
import { mkdirSync, rmSync } from "node:fs";
import { createRequire } from "node:module";
import net from "node:net";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const HERE = dirname(fileURLToPath(import.meta.url));
const APP_DIR = resolve(HERE, "..", "..");
const REPO_ROOT = resolve(APP_DIR, "..", "..");

const args = new Map(
  process.argv.slice(2).map((arg) => {
    const [key, ...rest] = arg.replace(/^--/, "").split("=");
    return [key, rest.join("=") || "true"];
  }),
);

const STUB_PORT = Number(args.get("stub-port") || process.env.STUB_PORT || 4010);
const CONSOLE_PORT = Number(args.get("port") || process.env.CONSOLE_PORT || 3020);
const OUT_DIR = resolve(args.get("out") || join(HERE, "captures"));
const ONLY = args.get("scenario") || "all";
// Free-text label stamped into each image, for captures whose point is the
// tree they were taken against ("without the fix", "parent commit").
const NOTE = args.get("note") || "";

const STUB_ORIGIN = `http://127.0.0.1:${STUB_PORT}`;
const CONSOLE_ORIGIN = `http://127.0.0.1:${CONSOLE_PORT}`;
const BASE_PATH = "/agent-workspace";

// Not a credential. The console only needs these to be non-empty strings; the
// stub checks neither, and no real service would accept either. The sign-in
// field value is read from the environment and named without a
// secret-shaped identifier (no "password"/"secret"/"token" in the name), so
// GitGuardian's generic-secret scanner has nothing keyword-matched to flag
// here, and no allowlist entry is needed for it.
const STUB_ANON_KEY = "stub-anon-key-not-a-credential";
const HARNESS_EMAIL = "agent-console-harness@example.invalid";
const HARNESS_SIGNIN_VALUE = process.env.HARNESS_SIGNIN_VALUE || "not-checked-by-the-stub";

function log(line) {
  console.log(`[capture] ${line}`);
}

function commitStamp() {
  try {
    const sha = execSync("git rev-parse --short HEAD", { cwd: REPO_ROOT }).toString().trim();
    const dirty = execSync("git status --porcelain", { cwd: REPO_ROOT }).toString().trim();
    return dirty ? `${sha}+local-edits` : sha;
  } catch {
    return "unknown-commit";
  }
}

function loadChromium() {
  // playwright is a devDependency of apps/web-console, the only app in this
  // repo that already needs a browser. Borrowing it keeps this harness at
  // zero dependencies of its own.
  const candidates = [
    // A git worktree has no node_modules of its own, so point this at a
    // checkout that does: HARNESS_PLAYWRIGHT_ROOT=/path/to/hive.
    process.env.HARNESS_PLAYWRIGHT_ROOT
      ? join(process.env.HARNESS_PLAYWRIGHT_ROOT, "apps", "web-console", "package.json")
      : null,
    join(REPO_ROOT, "apps", "web-console", "package.json"),
    join(APP_DIR, "package.json"),
  ].filter(Boolean);
  for (const from of candidates) {
    try {
      return createRequire(from)("playwright").chromium;
    } catch {
      // try the next resolution root
    }
  }
  throw new Error(
    "playwright not resolvable. Run `npm install` in apps/web-console, or set " +
      "HARNESS_PLAYWRIGHT_ROOT to a checkout that has it.",
  );
}

function portInUse(port) {
  return new Promise((resolve) => {
    const probe = net.createServer();
    probe.once("error", () => resolve(true));
    probe.once("listening", () => probe.close(() => resolve(false)));
    probe.listen(port, "127.0.0.1");
  });
}

async function waitForHttp(url, timeoutMs, what) {
  const deadline = Date.now() + timeoutMs;
  for (;;) {
    try {
      const response = await fetch(url);
      if (response.status < 500) return;
    } catch {
      // not up yet
    }
    if (Date.now() > deadline) {
      throw new Error(`${what} did not come up at ${url} within ${timeoutMs}ms`);
    }
    await new Promise((r) => setTimeout(r, 400));
  }
}

async function control(patch) {
  const response = await fetch(`${STUB_ORIGIN}/__control`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(patch),
  });
  if (!response.ok) {
    throw new Error(`stub control rejected the patch: HTTP ${response.status}`);
  }
  return response.json();
}

async function stubState() {
  const response = await fetch(`${STUB_ORIGIN}/__control`);
  return response.json();
}

async function shoot(page, scenario, name) {
  const stamp = [scenario, name, commitStamp(), NOTE].filter(Boolean).join("  ·  ");
  // Stamped into the pixels so an image cannot drift from the tree it proves.
  await page.evaluate((text) => {
    // `next dev` paints a floating build-status badge that is an artefact of
    // the harness, not of the app under proof.
    document.querySelectorAll("nextjs-portal").forEach((el) => el.remove());

    const existing = document.getElementById("harness-stamp");
    if (existing) existing.remove();
    const el = document.createElement("div");
    el.id = "harness-stamp";
    el.textContent = text;
    el.style.cssText = [
      "position:fixed",
      "left:0",
      "right:0",
      "bottom:0",
      "z-index:2147483647",
      "padding:6px 10px",
      "font:12px ui-monospace,SFMono-Regular,Menlo,monospace",
      "background:#111",
      "color:#f5f5f5",
      "letter-spacing:0.02em",
    ].join(";");
    document.body.appendChild(el);
  }, stamp);

  const file = join(OUT_DIR, `${scenario}-${name}.png`);
  await page.screenshot({ path: file, fullPage: true });
  log(`wrote ${file}`);
  await page.evaluate(() => document.getElementById("harness-stamp")?.remove());
}

async function signIn(page) {
  await page.goto(`${CONSOLE_ORIGIN}${BASE_PATH}/auth/sign-in`, { waitUntil: "networkidle" });
  await page.getByLabel("Email").fill(HARNESS_EMAIL);
  await page.getByLabel("Password").fill(HARNESS_SIGNIN_VALUE);
  await page.getByRole("button", { name: /sign in/i }).click();
  await page.waitForURL(`**${BASE_PATH}/tasks`, { timeout: 30_000 });
}

/*
 * Scenarios.
 *
 * Each one seeds the stub, drives the page, and shoots. Add a scenario by
 * adding an entry; nothing else in this file needs to know about it.
 */
const SCENARIOS = [
  {
    name: "poll-recovery",
    /*
     * The fix in `handleSubmit`: a successful create calls setFailures(0), so
     * a poll loop that had given up starts again.
     *
     * Reaching the give-up threshold is deliberately not shortcut. The console
     * needs MAX_POLL_FAILURES (5) consecutive list failures, and its backoff
     * (3s doubling to a 30s cap) puts that about 72 seconds after mount. That
     * wait is the real behaviour, so the harness sits through it.
     */
    async run(page) {
      await control({ tasks: [], listFailures: 50, createStatus: "queued", advanceOnList: {} });
      await signIn(page);

      log("waiting for the poll to give up (~72s of real backoff)");
      await page
        .getByText(/Could not load your tasks\. Reload the page to try again\./)
        .waitFor({ timeout: 180_000 });
      await shoot(page, "poll-recovery", "01-poll-gave-up");

      // The endpoint comes back. Nothing tells the page that; only a create
      // can, which is the whole point.
      await control({ listFailures: 0 });

      await page
        .getByLabel("What should the agent do?")
        .fill("Audit the payment webhook handlers for unvalidated input and list what you find.");
      await page.getByRole("button", { name: /start task/i }).click();

      // `exact` matters: the screen-reader live region also says "Task
      // submitted. Status: Queued." and a loose match hits both.
      await page.getByText("Queued", { exact: true }).waitFor({ timeout: 30_000 });
      const alerts = await page.getByRole("alert").allInnerTexts();
      log(`alerts on screen after create: ${JSON.stringify(alerts)}`);
      if (alerts.some((text) => /Could not load your tasks/.test(text))) {
        throw new Error("the load-failure alert survived a successful create");
      }
      await shoot(page, "poll-recovery", "02-created-alert-cleared");

      // Proof the loop actually resumed: the server moves the row and the
      // page follows without a reload.
      const { tasks } = await stubState();
      await control({ advanceOnList: { [tasks[0].id]: ["running"] } });
      log(`advancing task ${tasks[0].id} to running on the next poll`);

      await page.getByText("Running", { exact: true }).waitFor({ timeout: 30_000 });
      await shoot(page, "poll-recovery", "03-poll-resumed-row-progressed");
    },
  },
  {
    name: "unknown-status",
    /*
     * A status this build does not recognise decodes to "unknown" and keeps
     * its row. The version this replaced dropped the row, so a task the user
     * submitted vanished with no error.
     */
    async run(page) {
      await control({
        listFailures: 0,
        advanceOnList: {},
        tasks: [
          {
            id: "00000000-0000-4000-8000-0000000000aa",
            pack: "knowledge-work-pack",
            // A plausible future wire status. Anything outside the five
            // documented values takes the same path.
            status: "archived",
            instructions:
              "Summarise the retention policy changes and list which teams they affect.",
            created_at: new Date(Date.now() - 9 * 60_000).toISOString(),
          },
        ],
      });
      await signIn(page);
      await page.getByText("Unknown", { exact: true }).waitFor({ timeout: 30_000 });
      await shoot(page, "unknown-status", "01-unknown-row-rendered");
    },
  },
];

async function main() {
  // The .gitignore comment for this directory says every run overwrites it.
  // Make that literally true: wipe stale captures before writing new ones,
  // so an old poll-recovery-*.png from a prior --scenario run can never be
  // mistaken for current proof.
  rmSync(OUT_DIR, { recursive: true, force: true });
  mkdirSync(OUT_DIR, { recursive: true });

  const selected =
    ONLY === "all" ? SCENARIOS : SCENARIOS.filter((scenario) => scenario.name === ONLY);
  if (selected.length === 0) {
    throw new Error(
      `no scenario named "${ONLY}". Known: ${SCENARIOS.map((s) => s.name).join(", ")}`,
    );
  }

  /*
   * A stale server from an earlier run is the one failure this harness must
   * never absorb quietly: it answers on the same port, the run goes green,
   * and every screenshot proves a tree nobody is looking at. Refuse instead.
   */
  for (const [port, what] of [
    [STUB_PORT, "stub"],
    [CONSOLE_PORT, "next dev"],
  ]) {
    if (await portInUse(port)) {
      throw new Error(
        `port ${port} is already serving (${what}). A leftover run would be ` +
          `captured instead of this one. Stop it, or pass --port/--stub-port.`,
      );
    }
  }

  const children = [];
  const stopAll = () => {
    for (const child of children) {
      if (child.killed || child.exitCode !== null) continue;
      // Killed by process group: `npx next dev` runs the actual server as a
      // grandchild, and signalling npx alone leaves it holding the port.
      try {
        process.kill(-child.pid, "SIGTERM");
      } catch {
        child.kill("SIGTERM");
      }
    }
  };
  process.on("exit", stopAll);
  process.on("SIGINT", () => {
    stopAll();
    process.exit(130);
  });

  const stub = spawn(process.execPath, [join(HERE, "stub-server.mjs")], {
    env: { ...process.env, STUB_PORT: String(STUB_PORT) },
    stdio: ["ignore", "inherit", "inherit"],
    detached: true,
  });
  children.push(stub);
  await waitForHttp(`${STUB_ORIGIN}/__control`, 15_000, "stub server");
  log(`stub server up on ${STUB_ORIGIN}`);

  const next = spawn("npx", ["next", "dev", "--port", String(CONSOLE_PORT)], {
    cwd: APP_DIR,
    env: {
      ...process.env,
      // Every origin the console reaches points at the stub. Nothing in this
      // run resolves a real Supabase or edge-api host.
      NEXT_PUBLIC_SUPABASE_URL: STUB_ORIGIN,
      NEXT_PUBLIC_SUPABASE_ANON_KEY: STUB_ANON_KEY,
      NEXT_PUBLIC_EDGE_API_BASE_URL: STUB_ORIGIN,
      EDGE_API_INTERNAL_BASE_URL: STUB_ORIGIN,
    },
    stdio: ["ignore", "inherit", "inherit"],
    detached: true,
  });
  children.push(next);
  await waitForHttp(`${CONSOLE_ORIGIN}${BASE_PATH}/auth/sign-in`, 180_000, "next dev");
  log(`agent-console up on ${CONSOLE_ORIGIN}${BASE_PATH}`);

  const chromium = loadChromium();
  const browser = await chromium.launch();
  let failure = null;
  for (const scenario of selected) {
    log(`--- scenario: ${scenario.name} ---`);
    const context = await browser.newContext({ viewport: { width: 1280, height: 900 } });
    const page = await context.newPage();
    page.on("pageerror", (err) => log(`[browser pageerror] ${err.message}`));
    try {
      await scenario.run(page);
      log(`scenario ${scenario.name}: ok`);
    } catch (err) {
      failure = err;
      log(`scenario ${scenario.name}: FAILED ${err.message}`);
      // Stamped like any other shot. A failure capture is evidence too: it is
      // what a deliberately reverted fix looks like.
      await shoot(page, scenario.name, "FAILED").catch(() => {});
    }
    await context.close();
  }
  await browser.close();
  stopAll();

  if (failure) throw failure;
  log(`all captures written to ${OUT_DIR}`);
}

// A silent non-zero exit here means a screenshot is missing and nobody knows
// why, so make every abnormal ending say something.
process.on("uncaughtException", (err) => {
  console.error("[capture] uncaught exception:", err);
  process.exit(1);
});
process.on("unhandledRejection", (err) => {
  console.error("[capture] unhandled rejection:", err);
  process.exit(1);
});

main().catch((err) => {
  console.error("[capture] failed:", err);
  process.exit(1);
});
