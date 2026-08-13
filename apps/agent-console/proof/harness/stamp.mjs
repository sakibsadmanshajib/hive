// Screenshot stamping and browser loading, shared by the two harnesses in
// this directory: capture.mjs (stubbed, no stack) and capture-live.mjs (a
// real booted stack with a real Apptainer sandbox behind it).
//
// Extracted rather than copied: an image whose footer says one thing while
// the other harness stamps another is exactly the drift these stamps exist
// to prevent.

import { execSync } from "node:child_process";
import { createRequire } from "node:module";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const HERE = dirname(fileURLToPath(import.meta.url));
const APP_DIR = resolve(HERE, "..", "..");
const REPO_ROOT = resolve(APP_DIR, "..", "..");

/** Short commit of the tree under proof, marked when the tree is dirty. */
export function commitStamp() {
  try {
    const sha = execSync("git rev-parse --short HEAD", { cwd: REPO_ROOT }).toString().trim();
    const dirty = execSync("git status --porcelain", { cwd: REPO_ROOT }).toString().trim();
    return dirty ? `${sha}+local-edits` : sha;
  } catch {
    return "unknown-commit";
  }
}

/**
 * playwright is a devDependency of apps/web-console, the only app in this
 * repo that already needs a browser. Borrowing it keeps these harnesses at
 * zero dependencies of their own.
 */
export function loadChromium() {
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

/**
 * Screenshots `page` into `outDir`, with a footer stamped into the pixels.
 *
 * The stamp is the scenario, the shot, the commit, and a free-text note, in
 * that order. It never carries a URL: a capture of a flow whose URL holds a
 * credential (invitation accept, password reset, magic link) would then
 * publish that credential in an image no text linter can inspect. Anything
 * secret-shaped a caller wants recorded belongs in the text log, redacted
 * there, not in a pixel.
 */
export async function shoot(page, { outDir, scenario, name, note = "" }) {
  const stamp = [scenario, name, commitStamp(), note].filter(Boolean).join("  ·  ");
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

  const file = join(outDir, `${scenario}-${name}.png`);
  await page.screenshot({ path: file, fullPage: true });
  await page.evaluate(() => document.getElementById("harness-stamp")?.remove());
  return file;
}

/** Polls `url` until it answers below 500, or throws naming `what`. */
export async function waitForHttp(url, timeoutMs, what) {
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
