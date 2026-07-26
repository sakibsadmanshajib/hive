/**
 * control-plane-host.test.ts
 *
 * Keeps every place that names the console's control-plane pointing at the
 * same host, and keeps the retired one out.
 *
 * The bug this guards: the deployed Worker read
 * `CONTROL_PLANE_BASE_URL=https://cp-hive.scubed.co` long after that hostname
 * stopped belonging to the environment being deployed. The host still answered
 * `GET /health`, so nothing failed, but it ran an older control-plane build
 * whose `/api/v1/viewer` omitted `platform.admin`, and every admin panel
 * rendered "Admin access required" for a real platform admin.
 *
 * The failure mode is drift: the control-plane hostname is written down in
 * three files, one of them moved, and the others silently did not. Asserting
 * they agree catches the next occurrence in the required unit check, with no
 * credentials and no network. The companion e2e spec
 * (`tests/e2e/console-platform-admin.spec.ts`) covers the rendered symptom
 * when a live deployment and platform-admin credentials are available.
 */

import { describe, it, expect } from "vitest";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, resolve } from "node:path";

const HERE = dirname(fileURLToPath(import.meta.url));
const APP_ROOT = resolve(HERE, "../..");
const REPO_ROOT = resolve(APP_ROOT, "../..");

// Answered /health while serving a stale build. Never a valid target again.
const RETIRED_HOST = "cp-hive.scubed.co";

function read(path: string): string {
  return readFileSync(path, "utf8");
}

function extract(source: string, pattern: RegExp, label: string): string {
  const match = pattern.exec(source);
  if (match === null || match[1] === undefined) {
    throw new Error(`could not find ${label}`);
  }
  return match[1];
}

const workerVar = extract(
  read(resolve(APP_ROOT, "wrangler.jsonc")),
  /"CONTROL_PLANE_BASE_URL"\s*:\s*"([^"]+)"/,
  "CONTROL_PLANE_BASE_URL in wrangler.jsonc"
);

const documentedEnv = extract(
  read(resolve(REPO_ROOT, ".env.example")),
  /^HIVE_CONTROL_PLANE_URL=(\S+)$/m,
  "HIVE_CONTROL_PLANE_URL in .env.example"
);

const probeFallback = extract(
  read(resolve(APP_ROOT, "tests/e2e/_probe/staging-flows.spec.ts")),
  /HIVE_CONTROL_PLANE_URL\s*\?\?\s*"([^"]+)"/,
  "control-plane fallback in staging-flows.spec.ts"
);

describe("control-plane host", () => {
  it("is the same host everywhere it is written down", () => {
    expect(documentedEnv).toBe(workerVar);
    expect(probeFallback).toBe(workerVar);
  });

  it("is not the retired host", () => {
    const sources: ReadonlyArray<{ label: string; value: string }> = [
      { label: "wrangler.jsonc", value: workerVar },
      { label: ".env.example", value: documentedEnv },
      { label: "staging-flows.spec.ts", value: probeFallback },
    ];
    for (const { label, value } of sources) {
      expect(value, `${label} still names the retired host`).not.toContain(
        RETIRED_HOST
      );
    }
  });
});
