// Live interaction coverage for the Hive chat surface.
//
// Owner mandate: "Every control surface, anything that can be clicked, dropped
// down, changed, or swapped, should be tested live." This suite enumerates the
// chat app's controls from the rendered DOM and requires each one to produce an
// observable effect against a running deployment. It reports a ratio, writes a
// machine-readable file so the number can be tracked between runs, and fails
// loudly on any control that does nothing.
import fs from "node:fs";
import path from "node:path";

import { test, expect, type Page } from "@playwright/test";

import {
  enumerate,
  flip,
  isStateful,
  locate,
  proveByClick,
  readState,
  summarise,
  type Control,
  type Result,
} from "./lib";
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
const REGISTRY = JSON.parse(
  fs.readFileSync(path.join(__dirname, "inert-registry.json"), "utf8"),
) as { allowed: Array<{ key?: string; justification?: string }> };
const REMOVED = JSON.parse(
  fs.readFileSync(path.join(__dirname, "removed-surfaces.json"), "utf8"),
) as {
  forbiddenControls: Array<{ surface: string; label: string; issue: string }>;
  forbiddenRoutes: Array<{ path: string; issue: string }>;
};

// Anything that could destroy demo data. The gate still clicks these -- an
// untested destructive control is exactly what the owner is complaining about --
// but the write is intercepted at the network layer, so the request itself is
// the proof and nothing is actually deleted.
const DESTRUCTIVE = /delete|archive|clear|reset|remove|unshare|erase/i;

function isDestructiveRequest(method: string, url: string): boolean {
  if (method === "DELETE") return true;
  return /\/(delete|archive|clear|reset|unshare)/i.test(url);
}

async function withDestructiveGuard<T>(
  page: Page,
  fn: () => Promise<T>,
): Promise<{ value: T; blocked: string[] }> {
  const blocked: string[] = [];
  await page.route("**/*", async (route) => {
    const req = route.request();
    if (isDestructiveRequest(req.method(), req.url())) {
      blocked.push(`${req.method()} ${req.url()}`);
      await route.abort();
      return;
    }
    await route.continue();
  });
  try {
    const value = await fn();
    return { value, blocked };
  } finally {
    await page.unroute("**/*");
  }
}

async function saveIfPresent(page: Page): Promise<void> {
  const save = page.getByRole("button", { name: /^save$/i }).first();
  if (await save.isVisible().catch(() => false)) {
    await save.click().catch(() => {});
    await page.waitForTimeout(1500);
  }
}

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
): Promise<void> {
  for (const ctl of controls) {
    let res: Result | null = null;

    // Two attempts. The demo box has gone unreachable for minutes at a time
    // mid-run and recovered on its own (#815); a click that lands in that
    // window is indistinguishable from a dead control, so only a control that
    // does nothing twice from a clean surface is reported as one.
    for (let attempt = 0; attempt < 2 && !res?.proven; attempt++) {
      let target: Control | undefined;
      try {
        target = await relocate(page, surface, ctl.key);
      } catch (err) {
        res = {
          key: ctl.key,
          surface: surface.id,
          name: ctl.label || ctl.name,
          role: ctl.role || ctl.tag,
          proven: false,
          proof: "none",
          detail: `surface would not re-open: ${String(err).split("\n")[0].slice(0, 120)}`,
        };
        continue;
      }
      if (!target) {
        res = {
          key: ctl.key,
          surface: surface.id,
          name: ctl.label || ctl.name,
          role: ctl.role || ctl.tag,
          proven: false,
          proof: "none",
          detail: "control was not there when the surface was re-opened",
        };
        continue;
      }

      if (DESTRUCTIVE.test(target.label || target.name)) {
        const { value, blocked } = await withDestructiveGuard(page, () =>
          proveByClick(page, target!),
        );
        res =
          blocked.length > 0 && !value.proven
            ? { ...value, proven: true, proof: "network", detail: `blocked write: ${blocked[0]}` }
            : value;
        await page.keyboard.press("Escape").catch(() => {});
      } else {
        res = await proveByClick(page, target);
      }
    }

    results.push(res!);
    // eslint-disable-next-line no-console
    console.log(`[control] ${res!.proven ? res!.proof : "UNPROVEN"} :: ${res!.key}`);
  }
}

/**
 * Persistence pass. Open WebUI keeps user settings in a persisted config blob,
 * so a toggle that flips visually and persists nothing is invisible until the
 * next reload. Every stateful control on a persisting surface is flipped, saved,
 * and re-read after a full reload, then put back the way it was found.
 */
async function persistencePass(
  page: Page,
  surface: Surface,
  statefuls: Control[],
  results: Result[],
): Promise<void> {
  if (statefuls.length === 0) return;

  let live = await enumerateSurface(page, surface);
  const baselines = new Map<string, string | null>();
  const covIds = new Map<string, string>();

  for (const ctl of statefuls) {
    const target = live.find((c) => c.key === ctl.key);
    if (!target) continue;
    baselines.set(ctl.key, target.state);
    covIds.set(ctl.key, target.covId);
    await flip(page, target).catch(() => {});
    await page.waitForTimeout(200);
  }
  await saveIfPresent(page);

  await page.reload({ waitUntil: "domcontentloaded" });
  await page.waitForTimeout(2000);
  live = await enumerateSurface(page, surface);

  const missing: Control[] = [];
  const reverted: Control[] = [];
  for (const ctl of statefuls) {
    if (!baselines.has(ctl.key)) {
      results.push(unprovable(surface, ctl, "control was not present when the pass started"));
      continue;
    }
    const after = live.find((c) => c.key === ctl.key);
    if (!after) {
      missing.push(ctl);
      continue;
    }
    const before = baselines.get(ctl.key) ?? null;
    if (after.state !== before) {
      results.push(proven(surface, ctl, "persisted", `${before} -> ${after.state} survived a reload`));
    } else {
      // Do not report a batch result as a defect. Open WebUI saves some
      // sections on their own Save button and some immediately, and flipping a
      // whole tab at once can leave one of them behind for reasons that have
      // nothing to do with this control. Re-check it on its own before saying
      // it saves nothing.
      reverted.push(ctl);
    }
  }

  // Put the surface back the way it was found before doing anything else.
  for (const ctl of statefuls) {
    if (!baselines.has(ctl.key)) continue;
    const target = live.find((c) => c.key === ctl.key);
    if (!target) continue;
    const before = baselines.get(ctl.key) ?? null;
    if (target.state === before) continue;
    await restore(page, target, before).catch(() => {});
  }
  await saveIfPresent(page);

  // Controls the batch could not re-locate (a flipped toggle can hide its own
  // dependants) get an individual pass rather than a false negative.
  for (const ctl of [...missing, ...reverted]) {
    results.push(await persistOne(page, surface, ctl));
  }
}

async function restore(page: Page, ctl: Control, before: string | null): Promise<void> {
  const el = locate(page, ctl);
  if (ctl.role === "switch" || ctl.role === "checkbox" || ctl.type === "checkbox") {
    await el.click({ timeout: 6000 });
    return;
  }
  if (ctl.tag === "select") {
    await el.selectOption(before ?? "", { timeout: 6000 });
    return;
  }
  await el.fill(before ?? "", { timeout: 6000 });
}

async function persistOne(page: Page, surface: Surface, ctl: Control): Promise<Result> {
  const live = await enumerateSurface(page, surface);
  const target = live.find((c) => c.key === ctl.key);
  if (!target) return unprovable(surface, ctl, "not present on a clean re-open");
  const before = target.state;
  await flip(page, target).catch(() => {});
  await saveIfPresent(page);
  await page.reload({ waitUntil: "domcontentloaded" });
  await page.waitForTimeout(2000);
  const after = await enumerateSurface(page, surface);
  const back = after.find((c) => c.key === ctl.key);
  if (!back) return unprovable(surface, ctl, "disappeared after its own change");
  const ok = back.state !== before;
  if (ok) {
    await restore(page, back, before).catch(() => {});
    await saveIfPresent(page);
  }
  return ok
    ? proven(surface, ctl, "persisted", `${before} -> ${back.state} survived a reload`)
    : {
        key: ctl.key,
        surface: surface.id,
        name: ctl.label || ctl.name,
        role: ctl.role || ctl.tag,
        proven: false,
        proof: "none",
        detail: `changed in the UI but reverted to ${before} after reload`,
      };
}

function proven(surface: Surface, ctl: Control, proof: Result["proof"], detail: string): Result {
  return {
    key: ctl.key,
    surface: surface.id,
    name: ctl.label || ctl.name,
    role: ctl.role || ctl.tag,
    proven: true,
    proof,
    detail,
  };
}

function unprovable(surface: Surface, ctl: Control, detail: string): Result {
  return {
    key: ctl.key,
    surface: surface.id,
    name: ctl.label || ctl.name,
    role: ctl.role || ctl.tag,
    proven: false,
    proof: "none",
    detail,
  };
}

/** Non-persisting surfaces still have to show that typing lands somewhere. */
async function valuePass(
  page: Page,
  surface: Surface,
  statefuls: Control[],
  results: Result[],
): Promise<void> {
  if (statefuls.length === 0) return;
  const live = await enumerateSurface(page, surface);
  for (const ctl of statefuls) {
    const target = live.find((c) => c.key === ctl.key);
    if (!target) {
      results.push(unprovable(surface, ctl, "not present on re-open"));
      continue;
    }
    const before = target.state;
    const written = await flip(page, target).catch(() => null);
    await page.waitForTimeout(400);
    const after = await readState(page, target.covId);
    results.push(
      after !== before
        ? proven(surface, ctl, "value", `${before} -> ${after ?? written}`)
        : unprovable(surface, ctl, `value did not change (still ${before})`),
    );
    await restore(page, target, before).catch(() => {});
  }
}

test.describe("chat interaction coverage", () => {
  test("every rendered chat control has a proven live effect", async ({ page }) => {
    test.setTimeout(45 * 60_000);

    const sabotage = process.env.COV_SABOTAGE ?? "";
    if (sabotage) {
      // Self-test hook: neuter one named control so the gate can be watched
      // going red on a control that works in reality.
      await page.addInitScript((needle: string) => {
        window.addEventListener(
          "click",
          (e) => {
            const el = (e.target as HTMLElement | null)?.closest(
              "button, a, [role=button], [role=tab], [role=switch]",
            );
            if (!el) return;
            const text = (el.getAttribute("aria-label") || el.textContent || "").trim();
            if (text.toLowerCase().includes(needle.toLowerCase())) {
              e.stopImmediatePropagation();
              e.preventDefault();
            }
          },
          true,
        );
      }, sabotage);
    }

    const stage = (msg: string) => {
      // eslint-disable-next-line no-console
      console.log(`[stage ${new Date().toISOString().slice(11, 19)}] ${msg}`);
    };
    stage("opening the chat surface");
    await gotoHome(page);

    // Surface list is assembled live. Settings tabs and Workspace sections come
    // from the DOM, so this run covers whatever the deployment actually renders.
    stage("discovering settings tabs");
    const settings = await discoverSettingsTabs(page);
    await page.keyboard.press("Escape").catch(() => {});
    stage(`discovering workspace sections (settings tabs: ${settings.length})`);
    const workspace = await discoverWorkspaceTabs(page);
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
    const surfaces = filter ? allSurfaces.filter((s) => filter.test(s.id)) : allSurfaces;
    expect(surfaces.length, "surface filter matched nothing").toBeGreaterThan(0);

    const results: Result[] = [];
    const errors: string[] = [];

    for (const surface of surfaces) {
      let controls: Control[] = [];
      stage(`enumerating ${surface.id}`);
      try {
        controls = await enumerateSurface(page, surface);
        stage(`${surface.id}: ${controls.length} controls`);
      } catch (err) {
        errors.push(`${surface.id}: could not open (${String(err).split("\n")[0]})`);
        continue;
      }
      if (controls.length === 0) {
        errors.push(`${surface.id}: enumerated zero controls`);
        continue;
      }
      const stateful = controls.filter(isStateful);
      const clickable = controls.filter((c) => !isStateful(c));

      try {
        await clickPass(page, surface, clickable, results);
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

    const summary = summarise(results);
    const registryHits = new Map<string, string>();
    for (const entry of REGISTRY.allowed) {
      if (!entry.key) continue;
      registryHits.set(entry.key, (entry.justification ?? "").trim());
    }
    const excused = results.filter(
      (r) =>
        !r.proven &&
        [...registryHits.entries()].some(
          ([key, justification]) => r.key.includes(key) && justification.length > 0,
        ),
    );
    const failures = results.filter(
      (r) => !r.proven && !excused.some((e) => e.key === r.key),
    );

    fs.mkdirSync(REPORT_DIR, { recursive: true });
    const generatedAt = new Date().toISOString();
    const report = {
      generatedAt,
      target: process.env.CHAT_URL ?? process.env.OWUI_URL ?? "",
      surfaceFilter: process.env.COV_SURFACES ?? null,
      sabotage: sabotage || null,
      summary,
      excused: excused.map((r) => r.key),
      surfaceErrors: errors,
      results,
    };
    fs.writeFileSync(
      path.join(REPORT_DIR, "coverage.run.json"),
      JSON.stringify(report, null, 2),
    );

    // Tracker. Assertions below judge this run only; this file carries the
    // running total across sliced runs so the ratio is comparable over time.
    const trackerPath = path.join(REPORT_DIR, "coverage.json");
    const tracker: Record<string, Result & { lastSeen: string }> = fs.existsSync(trackerPath)
      ? (JSON.parse(fs.readFileSync(trackerPath, "utf8")).controls ?? {})
      : {};
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
    for (const r of results) tracker[r.key] = { ...r, lastSeen: generatedAt };
    const trackedResults = Object.values(tracker);
    fs.writeFileSync(
      trackerPath,
      JSON.stringify(
        {
          generatedAt,
          target: report.target,
          summary: summarise(trackedResults),
          controls: tracker,
        },
        null,
        2,
      ),
    );
    const lines = [
      `# Chat interaction coverage`,
      ``,
      `Proven ${summary.proven}/${summary.total} (${(summary.ratio * 100).toFixed(1)}%)`,
      ``,
      `| surface | proven | total |`,
      `| --- | ---: | ---: |`,
      ...Object.entries(summary.surfaces).map(
        ([s, v]) => `| ${s} | ${v.proven} | ${v.total} |`,
      ),
      ``,
      `## Unproven`,
      ...(failures.length === 0
        ? ["none"]
        : failures.map((f) => `- \`${f.key}\` — ${f.detail}`)),
    ];
    fs.writeFileSync(path.join(REPORT_DIR, "coverage.md"), lines.join("\n"));

    // eslint-disable-next-line no-console
    console.log(
      `[coverage] TOTAL ${summary.proven}/${summary.total} (${(summary.ratio * 100).toFixed(1)}%)`,
    );

    expect(errors, `surfaces that could not be swept: ${errors.join(" | ")}`).toEqual([]);
    expect(
      failures.map((f) => `${f.key} :: ${f.detail}`),
      "controls with no proven effect and no registry justification",
    ).toEqual([]);
  });

  test("the gate reports a control as unproven when its handler is neutered", async ({
    page,
  }) => {
    test.setTimeout(5 * 60_000);
    // Proof that the gate can go red. "Search" opens the search overlay on a
    // healthy deployment; here its click handler is stopped in the capture
    // phase, and the prover must notice that nothing happened.
    const needle = "Search";
    await page.addInitScript((text: string) => {
      window.addEventListener(
        "click",
        (e) => {
          const el = (e.target as HTMLElement | null)?.closest("button");
          if (!el) return;
          const label = (el.getAttribute("aria-label") || el.textContent || "").trim();
          if (label === text) {
            e.stopImmediatePropagation();
            e.preventDefault();
          }
        },
        true,
      );
    }, needle);

    await gotoHome(page);
    const controls = await enumerate(page, "sabotage");
    const target = controls.find((c) => (c.name || c.label) === needle && c.tag === "button");
    expect(target, "the Search button must exist for this self-test to mean anything").toBeTruthy();

    const result = await proveByClick(page, target!);
    expect(result.proven, `neutered control was reported as proven: ${result.detail}`).toBe(
      false,
    );
    expect(result.proof).toBe("none");
  });

  test("surfaces removed from upstream Open WebUI stay absent", async ({ page }) => {
    test.setTimeout(10 * 60_000);
    await gotoHome(page);

    const found: string[] = [];
    const seen: Array<{ surface: string; label: string }> = [];

    await openSettings(page);
    for (const c of await enumerate(page, "settings")) {
      seen.push({ surface: "settings:", label: (c.name || c.label).trim() });
    }
    await page.keyboard.press("Escape").catch(() => {});

    await gotoHome(page);
    await page.getByRole("button", { name: /user menu/i }).first().click();
    await page.waitForTimeout(1200);
    for (const c of await enumerate(page, "user-menu")) {
      seen.push({ surface: "user-menu", label: (c.name || c.label).trim() });
    }
    await page.keyboard.press("Escape").catch(() => {});

    await page.goto("/workspace", { waitUntil: "domcontentloaded" });
    await page.waitForTimeout(3000);
    for (const c of await enumerate(page, "workspace")) {
      seen.push({ surface: "workspace", label: (c.name || c.label).trim() });
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
      const body = (await page.locator("body").innerText()).toLowerCase();
      const gone =
        body.includes("not found") ||
        body.includes("404") ||
        !new URL(page.url()).pathname.startsWith(route.path);
      if (!gone) found.push(`${route.path} still renders a page (${route.issue})`);
    }

    expect(found, "removed upstream surfaces are still reachable").toEqual([]);
  });

  test("every inert-registry entry carries a justification", async () => {
    const bad = REGISTRY.allowed.filter(
      (e) => !e.key || !(e.justification ?? "").trim(),
    );
    expect(
      bad.map((e) => e.key ?? "(no key)"),
      "registry entries without a key or a justification",
    ).toEqual([]);
  });
});
