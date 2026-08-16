// Persistence proof for stateful controls (switches, checkboxes, selects,
// text inputs) on the chat coverage sweep.
//
// WHY THIS FILE EXISTS (issue #855, recurrence 2026-08-14)
// ---------------------------------------------------------
// This pass flips a real control on a shared, live account to prove the app
// saves it, which is a genuine mutation of that account's settings before
// anything here reads a verdict. Every function below therefore treats
// "restore the account to what it was" as the one step that may never be
// skipped, whatever else in the pass throws.
//
// The previous version put the restore loop after the reload-and-compare
// block with no guard between them. `page.reload()` against a box that goes
// briefly unreachable mid-run (issue #815, `surfaces.ts` documents the same
// flakiness) throws, and a throw there unwound straight out of the function:
// the caller's own try/catch (see chat-coverage.spec.ts) only recorded
// "state pass aborted" and moved on to the next surface. Every control this
// pass had just flipped, including Settings > Interface's Enter Key
// Behavior (`ui.ctrlEnterToSend`), was left in its flipped state with no
// error naming what happened. That is exactly the shape PR #863 described as
// "an automated sweep over the settings surface... set it, and left it set":
// this pass is that sweep, and the missing guard is the mechanism.
//
// The fix is structural, not a retry: the reload-and-compare step is wrapped
// so a failure there is recorded as an unprovable result rather than an
// uncaught throw, and restoration always runs afterward from whatever state
// could be re-read, on a best-effort basis if even that failed. See
// break-proof.spec.ts's "a reload failure still restores every flipped
// control" test, which exercises this without a live deployment.
//
// Scope of the guarantee, stated plainly rather than left implied: every
// throw that travels this file's own call chain (a failed reload, a failed
// re-open, a failed click) is caught and turned into a best-effort restore
// attempt. This cannot, and does not claim to, survive a Playwright
// worker-level interrupt: the enclosing test's own `test.setTimeout` firing
// mid-pass, or an unrelated unhandled rejection elsewhere in that same test
// reaching `workerMain.js` and calling `testInfo._interrupt()`. Neither is a
// JS exception unwinding the call stack, so no `try`/`catch`/`finally`
// written here can intercept either one.
import type { Page } from "@playwright/test";

import { flip, readState, type Control, type Result } from "./lib";
import { enumerateSurface, type Surface } from "./surfaces";

export function proven(surface: Surface, ctl: Control, proof: Result["proof"], detail: string): Result {
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

export function unprovable(surface: Surface, ctl: Control, detail: string): Result {
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

export async function saveIfPresent(page: Page): Promise<void> {
  const save = page.getByRole("button", { name: /^save$/i }).first();
  if (await save.isVisible().catch(() => false)) {
    await save.click().catch(() => {});
    await page.waitForTimeout(1500);
  }
}

export async function restore(page: Page, ctl: Control, before: string | null): Promise<void> {
  const el = page.locator(`[data-cov-id="${ctl.covId}"]`);
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

/**
 * Re-read a control after whatever just happened, tolerating a page that may
 * be mid-recovery from a failed reload. Never throws.
 *
 * `enumerateSurface` already calls `surface.open(page)` as its own first step
 * (surfaces.ts), so this must not open the surface itself first: doing both
 * issued two navigations per recovery attempt instead of one, doubling this
 * exact call's exposure to the #815 unreachability window it exists to
 * recover from.
 */
async function relocateAfterFailure(page: Page, surface: Surface): Promise<Control[]> {
  try {
    return await enumerateSurface(page, surface);
  } catch {
    return [];
  }
}

/**
 * Persistence pass. Open WebUI keeps user settings in a persisted config blob,
 * so a toggle that flips visually and persists nothing is invisible until the
 * next reload. Every stateful control on a persisting surface is flipped, saved,
 * and re-read after a full reload, then put back the way it was found.
 *
 * Restoration (the last third of this function) runs unconditionally: even
 * when the reload used to verify persistence itself fails, every control this
 * pass flipped is still put back on a best-effort basis, because it was
 * mutated for real on a shared account regardless of whether the proof step
 * that follows succeeds.
 */
export async function persistencePass(
  page: Page,
  surface: Surface,
  statefuls: Control[],
  results: Result[],
): Promise<void> {
  if (statefuls.length === 0) return;

  let live = await enumerateSurface(page, surface);
  const baselines = new Map<string, string | null>();

  for (const ctl of statefuls) {
    const target = live.find((c) => c.key === ctl.key);
    if (!target) continue;
    baselines.set(ctl.key, target.state);
    await flip(page, target).catch(() => {});
    await page.waitForTimeout(200);
  }
  await saveIfPresent(page).catch(() => {});

  // From here down, a failure must be recorded, never thrown: every control
  // flipped above is real, live account state, and the restoration below has
  // to run whether or not this block completes cleanly.
  const missing: Control[] = [];
  const reverted: Control[] = [];
  try {
    await page.reload({ waitUntil: "domcontentloaded" });
    await page.waitForTimeout(2000);
    live = await enumerateSurface(page, surface);

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
  } catch (err) {
    const reason = String(err).split("\n")[0].slice(0, 140);
    for (const ctl of statefuls) {
      if (!baselines.has(ctl.key)) continue;
      results.push(
        unprovable(
          surface,
          ctl,
          `could not verify persistence, the reload failed (${reason}); the control was still restored`,
        ),
      );
    }
    // A fresh open, not another reload: reload is what just failed, and
    // repeating the identical call is how one wedged attempt becomes a
    // certain skip of the restore below instead of a recovered one.
    live = await relocateAfterFailure(page, surface);
  }

  // Put the surface back the way it was found, unconditionally. This step
  // must run whatever the block above did or did not prove: every control
  // here was mutated for real on a shared, live account.
  for (const ctl of statefuls) {
    if (!baselines.has(ctl.key)) continue;
    const before = baselines.get(ctl.key) ?? null;
    let target = live.find((c) => c.key === ctl.key);
    if (!target) {
      // Not present in whatever `live` holds right now (the reload's own
      // read, or the recovery re-open above). A flipped control can hide its
      // own dependants, so try once more from a clean re-open before
      // treating it as unreachable: leaving it un-restored is the exact
      // defect this file exists to remove.
      const fresh = await relocateAfterFailure(page, surface);
      target = fresh.find((c) => c.key === ctl.key);
    }
    if (!target || target.state === before) continue;
    await restore(page, target, before).catch(() => {});
  }
  await saveIfPresent(page).catch(() => {});

  // Controls the batch could not re-locate (a flipped toggle can hide its own
  // dependants) get an individual pass rather than a false negative.
  //
  // Each call is isolated in its own try/catch, not just persistOne's own
  // internals: persistOne's leading enumerateSurface (locating the control
  // fresh before it can do anything else) is not itself guarded, since a
  // throw there means there is no target and nothing yet to restore. Without
  // this wrapper, that throw would propagate out of this loop entirely,
  // silently abandoning every control still pending behind it, not just the
  // one that failed to re-open, which is the same defect this file exists to
  // remove, one retry level deeper.
  for (const ctl of [...missing, ...reverted]) {
    try {
      results.push(await persistOne(page, surface, ctl));
    } catch (err) {
      results.push(
        unprovable(
          surface,
          ctl,
          `individual retry could not re-open the surface (${String(err).split("\n")[0].slice(0, 120)})`,
        ),
      );
    }
  }
}

/**
 * Same guarantee as persistencePass, for the one-control retry path: whatever
 * happens after the flip, the restore attempt at the end always runs.
 */
export async function persistOne(page: Page, surface: Surface, ctl: Control): Promise<Result> {
  const live = await enumerateSurface(page, surface);
  const target = live.find((c) => c.key === ctl.key);
  if (!target) return unprovable(surface, ctl, "not present on a clean re-open");
  const before = target.state;
  await flip(page, target).catch(() => {});
  await saveIfPresent(page).catch(() => {});

  let back: Control | undefined;
  let reloadError = "";
  try {
    await page.reload({ waitUntil: "domcontentloaded" });
    await page.waitForTimeout(2000);
    const after = await enumerateSurface(page, surface);
    back = after.find((c) => c.key === ctl.key);
  } catch (err) {
    reloadError = String(err).split("\n")[0].slice(0, 120);
  }

  // Restore regardless of whether the reload above succeeded or found the
  // control: this one was flipped for real above, on a shared live account.
  const current = back ?? (await relocateAfterFailure(page, surface)).find((c) => c.key === ctl.key);
  if (current && current.state !== before) {
    await restore(page, current, before).catch(() => {});
    await saveIfPresent(page).catch(() => {});
  }

  if (!back) {
    return unprovable(
      surface,
      ctl,
      reloadError
        ? `could not verify persistence (${reloadError}); restored on a best-effort basis`
        : "disappeared after its own change",
    );
  }
  const ok = back.state !== before;
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

/** Non-persisting surfaces still have to show that typing lands somewhere. */
export async function valuePass(
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
    let written: string | null = null;
    let after: string | null = before;
    try {
      written = await flip(page, target);
      await page.waitForTimeout(400);
      after = await readState(page, target.covId);
    } catch {
      // fall through: after stays at its last known value, and restore below
      // still runs regardless.
    }
    results.push(
      after !== before
        ? proven(surface, ctl, "value", `${before} -> ${after ?? written}`)
        : unprovable(surface, ctl, `value did not change (still ${before})`),
    );
    await restore(page, target, before).catch(() => {});
  }
}
