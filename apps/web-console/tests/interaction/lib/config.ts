import { readFileSync } from "node:fs";
import { join } from "node:path";

/**
 * apps/web-console.
 *
 * Resolved from the working directory rather than from `import.meta.url` or
 * `__dirname` so the module works unchanged whether Playwright transpiles the
 * suite to CommonJS or to ESM. The name check turns a wrong working directory
 * into an immediate, readable failure instead of an empty route list that
 * would read as "nothing to test" and pass.
 */
export const WEB_CONSOLE_DIR = (() => {
  const cwd = process.cwd();
  try {
    const manifest: unknown = JSON.parse(
      readFileSync(join(cwd, "package.json"), "utf8"),
    );
    if (
      typeof manifest === "object" &&
      manifest !== null &&
      "name" in manifest &&
      (manifest as { name: unknown }).name === "@hive/web-console"
    ) {
      return cwd;
    }
  } catch {
    // fall through to the explicit error below
  }
  throw new Error(
    `the interaction coverage gate must run with apps/web-console as the working directory (got ${cwd})`,
  );
})();

export const APP_DIR = join(WEB_CONSOLE_DIR, "app");
export const INTERACTION_DIR = join(WEB_CONSOLE_DIR, "tests", "interaction");
export const REGISTRY_FILE = join(INTERACTION_DIR, "control-registry.json");
export const ROUTE_FIXTURE_FILE = join(INTERACTION_DIR, "route-fixtures.json");
export const AUTH_STATE_FILE = join(INTERACTION_DIR, ".auth", "user.json");
export const REPORT_DIR = join(WEB_CONSOLE_DIR, "interaction-coverage");

export function interactionCredentials(): { email: string; password: string } {
  return {
    email: process.env.INTERACTION_EMAIL ?? process.env.E2E_VERIFIED_EMAIL ?? "",
    password:
      process.env.INTERACTION_PASSWORD ?? process.env.E2E_VERIFIED_PASSWORD ?? "",
  };
}

/**
 * Origin under test. The gate is origin agnostic on purpose: the same run
 * targets the composed stack in CI and the deployed demo box for a live run,
 * so a control that only works against fixtures cannot pass for working.
 */
export function interactionBaseUrl(): string {
  return (
    process.env.INTERACTION_BASE_URL ??
    process.env.PLAYWRIGHT_BASE_URL ??
    "http://localhost:3000"
  );
}

/** Optional comma separated route filter, for local iteration only. */
export function routeFilter(): string[] {
  const raw = process.env.INTERACTION_ROUTES ?? "";
  return raw
    .split(",")
    .map((value) => value.trim())
    .filter((value) => value !== "");
}
