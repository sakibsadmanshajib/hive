// Live interaction coverage for the Hive chat surface.
//
// Owner mandate: "Every control surface, anything that can be clicked, dropped
// down, changed, or swapped, should be tested live." This suite enumerates the
// chat app's controls from the rendered DOM and requires each one to produce an
// observable effect against a running deployment. It reports a ratio, writes a
// machine-readable file so the number can be tracked between runs, and fails
// loudly on any control that does nothing.
//
// Everything here needs a live deployment and a session. The checks that need
// neither live in break-proof.spec.ts.
import fs from "node:fs";
import path from "node:path";

import { test, expect, type Page } from "@playwright/test";

import { EXCLUSIONS, FLOORS, REGISTRY, REMOVED } from "./data";
import {
  checkWithoutFiring,
  enumerate,
  exclusionFailures,
  floorFailures,
  isDeferred,
  isDestructive,
  identityOf,
  isStateful,
  proveByClick,
  redactText,
  redactUrl,
  sampleChatter,
  summarise,
  type Control,
  type Result,
  type Swept,
} from "./lib";
import { persistencePass, valuePass } from "./persistence";
import {
  STATIC_SURFACES,
  discoverSettingsTabs,
  discoverWorkspaceTabs,
  enumerateSurface,
  relocate,
  gotoHome,
  openSettings,
  type Surface,
} from "./surfaces";

const REPORT_DIR = path.join(__dirname, "../../chat-coverage-report");

/**
 * Safety net, and deliberately not a source of proof.
 *
 * Destructive controls are recognised by their label and never clicked (see
 * checkWithoutFiring). This second guard exists for the control whose label
 * says nothing about what it does: its write is aborted at the network layer
 * so the demo account's data survives, and the control it came from is then
 * recorded as not-fired rather than as proven, because an aborted request
 * never reaches the server and so has no verdict to read.
 */
function isDestructiveRequest(method: string, url: string): boolean {
  if (method === "DELETE") return true;
  return /\/(delete|archive|clear|reset|unshare)/i.test(url);
}

/**
 * Failures that say nothing about the control.
 *
 * The demo box has gone unreachable for minutes at a time mid-run (#815), and
 * a click that lands in that window is indistinguishable from a dead control.
 * Only these retry. A clean verdict of "this control does nothing" is final on
 * the first attempt: retrying it until it passes is how an in-test loop turns
 * itself into the retries the config sets to zero on purpose.
 */
function isInfrastructural(result: Result): boolean {
  return /surface would not re-open|was not there when the surface was re-opened|no verdict within/.test(
    result.detail,
  );
}

type PassContext = {
  /** Request signatures the page emits on its own, sampled while idle. */
  chatter: ReadonlySet<string>;
  /** Writes the safety net aborted, appended to for the whole run. */
  blockedWrites: string[];
};

/**
 * Click pass: one control at a time, re-opening the surface when an action
 * moved the app. Results are appended to the caller's array as they are
 * produced: an earlier version returned them at the end, and one timeout in
 * the middle of a surface threw away every proof collected before it.
 */
async function clickPass(
  page: Page,
  surface: Surface,
  controls: Control[],
  results: Result[],
  ctx: PassContext,
): Promise<void> {
  for (const ctl of controls) {
    let res: Result = {
      key: ctl.key,
      surface: surface.id,
      name: ctl.label || ctl.name,
      role: ctl.role || ctl.tag,
      proven: false,
      proof: "none",
      detail: "the pass produced no verdict for this control",
    };

    for (let attempt = 0; attempt < 2; attempt++) {
      let target: Control | undefined;
      try {
        target = await relocate(page, surface, ctl.key);
      } catch (err) {
        res = {
          ...res,
          proven: false,
          proof: "none",
          detail: `surface would not re-open: ${String(err).split("\n")[0].slice(0, 120)}`,
        };
        continue;
      }
      if (!target) {
        res = {
          ...res,
          proven: false,
          proof: "none",
          detail: "control was not there when the surface was re-opened",
        };
        continue;
      }

      if (isDestructive(target)) {
        res = await checkWithoutFiring(page, target);
      } else {
        const before = ctx.blockedWrites.length;
        res = await proveByClick(page, target, { ignoreRequests: ctx.chatter });
        if (ctx.blockedWrites.length > before) {
          // It fired a write the safety net stopped. Whatever the positive
          // channels saw, the server never answered, so there is no verdict
          // to record and this is not proof.
          res = {
            ...res,
            proven: false,
            proof: "not-fired",
            detail: `fired a write that was intercepted (${ctx.blockedWrites[before]}), so the server never answered it`,
          };
        }
      }
      if (res.proven || isDeferred(res) || !isInfrastructural(res)) break;
    }

    results.push(res);
    // eslint-disable-next-line no-console
    console.log(`[control] ${res.proven ? res.proof : res.proof === "not-fired" ? "NOT-FIRED" : "UNPROVEN"} :: ${res.key}`);
  }
}

// persistencePass and valuePass (the flip-verify-restore logic for stateful
// controls, e.g. Settings > Interface's checkboxes and switches) live in
// ./persistence, not here. See that file's header for why: a throw between
// flipping a control on this shared live account and restoring it used to
// leave the flip permanent (issue #855, recurrence 2026-08-14), and the fix
// needed a harness (break-proof.spec.ts) that does not require a live
// deployment.

test.describe("chat interaction coverage", () => {
  test("every rendered chat control has a proven live effect", async ({ page }) => {
    test.setTimeout(45 * 60_000);

    const sabotage = process.env.COV_SABOTAGE ?? "";
    if (sabotage) {
      // Self-test hook: neuter one named control so the gate can be watched
      // going red on a control that works in reality.
      await page.addInitScript((needle: string) => {
        // Every event a handler could be bound to, stopped in the capture phase
        // so it never reaches Svelte's delegated listeners at the document root.
        // Blocking "click" alone left pointerdown handlers running.
        for (const type of ["pointerdown", "mousedown", "mouseup", "click", "keydown"]) {
          window.addEventListener(
            type,
            (e) => {
              const el =
                e.target instanceof HTMLElement
                  ? e.target.closest("button, a, [role=button], [role=tab], [role=switch]")
                  : null;
              if (!el) return;
              const text = (el.getAttribute("aria-label") || el.textContent || "").trim();
              if (text.toLowerCase() === needle.toLowerCase()) {
                e.stopImmediatePropagation();
                e.preventDefault();
                window.__covBlocked = (window.__covBlocked ?? 0) + 1;
              }
            },
            true,
          );
        }
      }, sabotage);
    }

    const stage = (msg: string) => {
      // eslint-disable-next-line no-console
      console.log(`[stage ${new Date().toISOString().slice(11, 19)}] ${msg}`);
    };

    // Safety net for the whole run. Nothing that reaches this is proof of
    // anything; see isDestructiveRequest.
    const blockedWrites: string[] = [];
    await page.route("**/*", async (route) => {
      const req = route.request();
      if (isDestructiveRequest(req.method(), req.url())) {
        // Redacted, because this list is written into the ledger and the
        // ledger is uploaded as a CI artifact.
        blockedWrites.push(`${req.method()} ${redactUrl(req.url())}`);
        await route.abort();
        return;
      }
      await route.continue();
    });

    stage("opening the chat surface");
    await gotoHome(page);

    // Everything pushed here goes through redactText on the way out (see the
    // ledger write below): these strings are mostly Playwright's own error
    // messages, which quote the URLs a failed navigation walked through, and
    // this suite signs in through an OAuth callback.
    const errors: string[] = [];
    errors.push(...exclusionFailures(EXCLUSIONS));

    // Surface list is assembled live. Settings tabs and Workspace sections come
    // from the DOM, so this run covers whatever the deployment actually renders.
    // A discovery that finds nothing throws rather than returning an empty
    // list; it is recorded and the sweep continues, so the run still produces a
    // ledger, and the floor guard below fails on every surface it lost.
    stage("discovering settings tabs");
    let settings: Surface[] = [];
    try {
      settings = await discoverSettingsTabs(page);
    } catch (err) {
      errors.push(`settings discovery failed: ${String(err).split("\n")[0]}`);
    }
    await page.keyboard.press("Escape").catch(() => {});
    stage(`discovering workspace sections (settings tabs: ${settings.length})`);
    let workspace: Surface[] = [];
    try {
      workspace = await discoverWorkspaceTabs(page);
    } catch (err) {
      errors.push(`workspace discovery failed: ${String(err).split("\n")[0]}`);
    }
    stage(`workspace sections: ${workspace.length}`);
    const allSurfaces: Surface[] = [
      ...STATIC_SURFACES.filter((s) => s.id !== "user-menu"),
      ...settings,
      ...workspace,
      // Last: it holds Sign Out, which ends the session.
      ...STATIC_SURFACES.filter((s) => s.id === "user-menu"),
    ];
    // #782 kills a chat session about 55 minutes after sign-in, so a full sweep
    // can outlive its own credentials. COV_SURFACES runs one slice per
    // invocation; the tracker file below merges slices back into one number.
    const filter = process.env.COV_SURFACES ? new RegExp(process.env.COV_SURFACES) : null;
    // Exclusions are enumerated, owned, and tied to an open issue, and they
    // leave the denominator entirely rather than counting as covered. The
    // expiry test in break-proof.spec.ts fails once the blocking issue closes,
    // so an exclusion cannot quietly outlive its reason.
    const excludedIds = new Set(EXCLUSIONS.map((e) => e.id));
    const inScope = (id: string) => !excludedIds.has(id) && (filter === null || filter.test(id));
    const surfaces = allSurfaces.filter((s) => inScope(s.id));
    expect(surfaces.length, "surface filter matched nothing").toBeGreaterThan(0);
    // A sliced run measures a slice. It still writes a ledger and still fails
    // on any control that does nothing, but it must never print a percentage
    // of the whole, because its denominator is not the whole.
    const partial = filter !== null;

    const results: Result[] = [];
    const swept: Swept[] = [];

    for (const surface of surfaces) {
      let controls: Control[] = [];
      stage(`enumerating ${surface.id}`);
      try {
        controls = await enumerateSurface(page, surface);
        swept.push({ surface: surface.id, enumerated: controls.length });
        stage(`${surface.id}: ${controls.length} controls`);
      } catch (err) {
        errors.push(`${surface.id}: could not open (${String(err).split("\n")[0]})`);
        continue;
      }
      if (controls.length === 0) {
        errors.push(`${surface.id}: enumerated zero controls`);
        continue;
      }
      // Destructive first, whatever kind of control it is. flip() fills every
      // text input and then clicks Save, so a stateful control named "Clear
      // memories" or a "Reset to default" select was being activated for real
      // while only the click pass consulted isDestructive.
      const destructive = controls.filter(isDestructive);
      const rest = controls.filter((c) => !isDestructive(c));
      const stateful = rest.filter(isStateful);
      const clickable = rest.filter((c) => !isStateful(c));
      for (const ctl of destructive) {
        const target = await relocate(page, surface, ctl.key).catch(() => undefined);
        results.push(
          target
            ? await checkWithoutFiring(page, target)
            : {
                key: ctl.key,
                surface: surface.id,
                name: ctl.label || ctl.name,
                role: ctl.role || ctl.tag,
                proven: false,
                proof: "none",
                detail: "destructive control was not there when the surface was re-opened",
              },
        );
      }

      // What this surface does while nobody is touching it. Anything in here
      // cannot be counted as a control's network proof.
      const chatter = await sampleChatter(page, 1500);
      if (chatter.size > 0) {
        stage(`${surface.id}: ignoring ${chatter.size} background request signature(s)`);
      }

      try {
        await clickPass(page, surface, clickable, results, { chatter, blockedWrites });
      } catch (err) {
        errors.push(`${surface.id}: click pass aborted (${String(err).split("\n")[0]})`);
      }
      try {
        // A search or filter box on a settings screen is a view control, not a
        // setting, and nothing is wrong with it forgetting what was typed. It
        // still has to prove that typing lands somewhere.
        const transient = (c: Control) => /search|filter/i.test(c.label || c.name);
        if (surface.persists) {
          await persistencePass(page, surface, stateful.filter((c) => !transient(c)), results);
          await valuePass(page, surface, stateful.filter(transient), results);
        } else {
          await valuePass(page, surface, stateful, results);
        }
      } catch (err) {
        errors.push(`${surface.id}: state pass aborted (${String(err).split("\n")[0]})`);
      }
      // eslint-disable-next-line no-console
      console.log(
        `[coverage] ${surface.id}: ${results.filter((r) => r.surface === surface.id && r.proven).length}/${
          results.filter((r) => r.surface === surface.id).length
        } proven`,
      );
    }

    if (sabotage) {
      const blocked = await page
        .evaluate(() => window.__covBlocked ?? 0)
        .catch(() => 0);
      stage(`sabotage "${sabotage}" intercepted ${blocked} events on the last page`);
    }

    // Denominator guard, before anything is said about the ratio. This run only
    // ever reads the floors. Raising or lowering them is a separate, deliberate
    // operation: scripts/update-chat-coverage-floors.mjs, run against a
    // recorded ledger, in its own commit, with a reason. A run that could
    // rewrite its own bar in the same pass that checks it has no bar at all,
    // which is how a real degradation was once ratified as the new baseline.
    errors.push(...floorFailures(FLOORS, swept, inScope));

    const summary = summarise(results);
    const registryHits = new Map<string, string>();
    for (const entry of REGISTRY) {
      if (!entry.key) continue;
      registryHits.set(entry.key, entry.justification.trim());
    }
    // A control nobody fired is not coverage, and it is not a free pass
    // either. It fails the gate exactly like a dead control unless
    // inert-registry.json carries a justification for its key, which is the
    // same escape hatch every other unfireable control uses and which someone
    // has to write down and sign. An earlier version dropped every not-fired
    // result from the failure list, which would have turned two controls this
    // repository's own ledger proves dead into a green gate.
    // Exact key, or the identity the key is an instance of. Substring matching
    // used to be the rule, and it excuses far more than whoever wrote the entry
    // intended: "settings::button::Save" would also excuse
    // "settings::button::Save and close". An excuse is a decision about one
    // control, so it names one control.
    const excused = results.filter((r) => {
      if (r.proven) return false;
      const justification =
        registryHits.get(r.key) ?? registryHits.get(identityOf(r.key)) ?? "";
      return justification.length > 0;
    });
    const deferred = results.filter(isDeferred);
    const failures = results.filter(
      (r) => !r.proven && !excused.some((e) => e.key === r.key),
    );

    fs.mkdirSync(REPORT_DIR, { recursive: true });
    const generatedAt = new Date().toISOString();
    const report = {
      generatedAt,
      target: process.env.CHAT_URL ?? process.env.OWUI_URL ?? "",
      surfaceFilter: process.env.COV_SURFACES ?? null,
      partial,
      sabotage: sabotage || null,
      summary,
      swept,
      excused: excused.map((r) => r.key),
      deferred: deferred.map((r) => r.key),
      blockedWrites,
      surfaceErrors: errors.map(redactText),
      results,
    };
    fs.writeFileSync(
      path.join(REPORT_DIR, "coverage.run.json"),
      JSON.stringify(report, null, 2),
    );

    // Tracker. Assertions below judge this run only; this file carries the
    // running total across sliced runs.
    //
    // It is written from a full sweep only. A slice used to merge into it and
    // then print summarise() over the merged set as though it were a total,
    // mixing this run's verdicts with whatever a previous run happened to
    // leave behind and labelling the result with no marker at all. A number
    // built half from now and half from an unknown then is worse than no
    // number.
    const trackerPath = path.join(REPORT_DIR, "coverage.json");
    const tracker = partial ? {} : readTracker(trackerPath);
    if (partial) {
      // eslint-disable-next-line no-console
      console.log("[coverage] partial run: coverage.json left untouched");
    }
    // Drop stale keys before merging. A surface swept in this run is fully
    // described by this run, so anything it used to contain and no longer does
    // has to go, or a control that was removed (or an enumeration bug that has
    // since been fixed) keeps dragging the ratio down forever.
    const sweptSurfaces = new Set(results.map((r) => r.surface));
    const seenKeys = new Set(results.map((r) => r.key));
    for (const key of Object.keys(tracker)) {
      if (sweptSurfaces.has(tracker[key].surface) && !seenKeys.has(key)) {
        delete tracker[key];
      }
    }
    if (!partial) {
      for (const r of results) tracker[r.key] = { ...r, lastSeen: generatedAt };
      fs.writeFileSync(
        trackerPath,
        JSON.stringify(
          {
            generatedAt,
            target: report.target,
            summary: summarise(Object.values(tracker)),
            controls: tracker,
          },
          null,
          2,
        ),
      );
    }
    const headline = partial
      ? [
          `Partial run: surfaces matching ${String(process.env.COV_SURFACES)} only.`,
          `Proven ${summary.identitiesProven} of the ${summary.identities} distinct control identities on those surfaces. No percentage is reported for a slice, because its denominator is not the app.`,
        ]
      : [
          `Proven ${summary.identitiesProven}/${summary.identities} distinct control identities (${(summary.identityRatio * 100).toFixed(1)}%)`,
          ``,
          `Secondary, not comparable between runs: ${summary.proven}/${summary.total} raw instances (${(summary.ratio * 100).toFixed(1)}%). Instance counts move with account data, for example the number of chat rows, so only the identity figure above should be compared over time.`,
        ];
    const lines = [
      `# Chat interaction coverage`,
      ``,
      ...headline,
      ``,
      `Checked but deliberately not activated: ${summary.identitiesDeferred} identities (${summary.deferred} instances). A control whose label says it destroys data is asserted present, enabled and named, and never clicked. That is not coverage and is never counted as proof.`,
      ``,
      `| surface | proven | not fired | total |`,
      `| --- | ---: | ---: | ---: |`,
      ...Object.entries(summary.surfaces).map(
        ([s, v]) => `| ${s} | ${v.proven} | ${v.deferred} | ${v.total} |`,
      ),
      ``,
      `## Unproven`,
      ...(failures.length === 0
        ? ["none"]
        : failures.map((f) => `- \`${f.key}\`: ${f.detail}`)),
    ];
    fs.writeFileSync(path.join(REPORT_DIR, "coverage.md"), lines.join("\n"));

    // eslint-disable-next-line no-console
    console.log(
      partial
        ? `[coverage] PARTIAL slice ${String(process.env.COV_SURFACES)}: ${summary.identitiesProven}/${summary.identities} identities on the swept surfaces, ${summary.deferred} instances not fired. No total for a slice.`
        : `[coverage] TOTAL ${summary.identitiesProven}/${summary.identities} identities (${(summary.identityRatio * 100).toFixed(1)}%), ${summary.proven}/${summary.total} instances, ${summary.deferred} not fired`,
    );

    // Redacted here too: a failing assertion's message is the one string that
    // reaches the CI log, the JSON report and the HTML report at once.
    const reportedErrors = errors.map(redactText);
    expect(
      reportedErrors,
      `surfaces that could not be swept: ${reportedErrors.join(" | ")}`,
    ).toEqual([]);
    expect(
      failures.map((f) => `${f.key} :: ${f.detail}`),
      "controls with no proven effect and no registry justification",
    ).toEqual([]);
  });

  test("surfaces removed from upstream Open WebUI stay absent", async ({ page }) => {
    test.setTimeout(10 * 60_000);
    await gotoHome(page);

    const found: string[] = [];
    const seen: Array<{ surface: string; label: string }> = [];
    const enumerated = new Map<string, number>();

    const collect = async (surface: string) => {
      const controls = await enumerate(page, surface);
      enumerated.set(surface, controls.length);
      for (const c of controls) {
        seen.push({ surface, label: (c.name || c.label).trim() });
      }
    };

    await openSettings(page);
    await collect("settings:");
    await page.keyboard.press("Escape").catch(() => {});

    await gotoHome(page);
    await page.getByRole("button", { name: /user menu/i }).first().click();
    await page.waitForTimeout(1200);
    await collect("user-menu");
    await page.keyboard.press("Escape").catch(() => {});

    await page.goto("/workspace", { waitUntil: "domcontentloaded" });
    await page.waitForTimeout(3000);
    await collect("workspace");

    // Absence only means something if the enumeration happened. A settings
    // modal that never opened produces the same empty list as a settings modal
    // with nothing forbidden in it, and this test used to call that a pass.
    for (const [surface, count] of enumerated) {
      expect(
        count,
        `${surface} enumerated no controls at all, so it proves nothing about what was removed from it`,
      ).toBeGreaterThan(0);
    }

    for (const rule of REMOVED.forbiddenControls) {
      const surfaceRe = new RegExp(rule.surface);
      const labelRe = new RegExp(rule.label);
      for (const item of seen) {
        if (surfaceRe.test(item.surface) && labelRe.test(item.label)) {
          found.push(`${item.surface} still renders "${item.label}" (${rule.issue})`);
        }
      }
    }

    for (const route of REMOVED.forbiddenRoutes) {
      await page.goto(route.path, { waitUntil: "domcontentloaded" });
      await page.waitForTimeout(2500);
      const landed = new URL(page.url()).pathname;
      // A bounce to the sign-in page is a dead session, not a removed route.
      // Treating it as removal is how this test would pass against an app
      // nobody is signed in to.
      expect(
        landed.startsWith("/auth"),
        `${route.path} redirected to the sign-in page, so the session died and absence proves nothing`,
      ).toBe(false);
      const body = (await page.locator("body").innerText()).toLowerCase();
      expect(
        body.trim().length,
        `${route.path} rendered an empty document, so absence proves nothing`,
      ).toBeGreaterThan(0);
      const gone =
        body.includes("not found") || body.includes("404") || !landed.startsWith(route.path);
      if (!gone) found.push(`${route.path} still renders a page (${route.issue})`);
    }

    expect(found, "removed upstream surfaces are still reachable").toEqual([]);
  });
});

type TrackedResult = Result & { lastSeen: string };

/**
 * The running tracker from a previous slice, if there is one. Read defensively:
 * a truncated or hand-edited file is not worth failing a 45 minute sweep over,
 * and starting from empty only costs this run its cross-slice history.
 */
function readTracker(file: string): Record<string, TrackedResult> {
  if (!fs.existsSync(file)) return {};
  const out: Record<string, TrackedResult> = {};
  let parsed: unknown;
  try {
    parsed = JSON.parse(fs.readFileSync(file, "utf8"));
  } catch {
    return out;
  }
  if (typeof parsed !== "object" || parsed === null) return out;
  const controls = Object.entries(parsed).find(([name]) => name === "controls")?.[1];
  if (typeof controls !== "object" || controls === null) return out;
  for (const [key, value] of Object.entries(controls)) {
    if (typeof value !== "object" || value === null) continue;
    const entry = new Map(Object.entries(value));
    const surface = entry.get("surface");
    const proofValue = entry.get("proof");
    if (typeof surface !== "string" || typeof proofValue !== "string") continue;
    out[key] = {
      key,
      surface,
      name: String(entry.get("name") ?? ""),
      role: String(entry.get("role") ?? ""),
      proven: entry.get("proven") === true,
      proof: proofOf(proofValue),
      detail: String(entry.get("detail") ?? ""),
      lastSeen: String(entry.get("lastSeen") ?? ""),
    };
  }
  return out;
}

const PROOF_NAMES = [
  "navigate",
  "network",
  "dom",
  "overlay",
  "download",
  "filechooser",
  "value",
  "persisted",
  "not-fired",
  "none",
] as const;

function proofOf(value: string): Result["proof"] {
  const match = PROOF_NAMES.find((name) => name === value);
  return match ?? "none";
}
