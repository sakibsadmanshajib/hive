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
 * (`tests/e2e/console-workspace-admin.spec.ts`) covers the rendered symptom
 * when a live deployment and platform-admin credentials are available.
 *
 * The second suite below extends the same idea to `deploy/**`, which is where
 * the next instance of this bug actually hid: `docker-compose.staging.yml` was a
 * fourth place the hostname was written down, it kept naming the retired host
 * long after the record died, and nothing checked it. Every hive hostname named
 * anywhere under `deploy/` must be one that `deploy/cloudflare/tunnel-ingress.json`
 * says exists. That is a file-to-file assertion, so it runs in the required unit
 * check with no Cloudflare credentials and no network, unlike
 * `npm run cloudflare:check`, which diffs the same spec against live Cloudflare
 * and needs an API token.
 */

import { describe, it, expect } from "vitest";
import { readdirSync, readFileSync, statSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, extname, relative, resolve } from "node:path";

const HERE = dirname(fileURLToPath(import.meta.url));
const APP_ROOT = resolve(HERE, "../..");
const REPO_ROOT = resolve(APP_ROOT, "../..");
const DEPLOY_ROOT = resolve(REPO_ROOT, "deploy");
const SPEC_PATH = resolve(DEPLOY_ROOT, "cloudflare/tunnel-ingress.json");

// `deploy/cloudflare` is the hostname registry itself: tunnel-ingress.json
// defines the live set, and the README plus the reconcile script name retired
// hostnames on purpose so the history stays discoverable. Everything else under
// `deploy/` is configuration that reaches a running service, and may only name
// hostnames that exist.
const REGISTRY_DIR = resolve(DEPLOY_ROOT, "cloudflare");

// Answered /health while serving a stale build. Never a valid target again.
const RETIRED_HOST = "cp-hive.scubed.co";

// Any DNS name in the zone, however it is embedded (URL, YAML value, comment).
const ZONE_HOSTNAME = /\b[a-z0-9][a-z0-9.-]*\.scubed\.co\b/gi;

const BINARY_EXTENSIONS: ReadonlySet<string> = new Set([
  ".png",
  ".jpg",
  ".jpeg",
  ".gif",
  ".webp",
  ".ico",
  ".pdf",
  ".sif",
  ".gz",
  ".tgz",
  ".zip",
  ".tar",
]);
const MAX_SCANNED_BYTES = 512 * 1024;

function read(path: string): string {
  return readFileSync(path, "utf8");
}

function textFilesUnder(dir: string): ReadonlyArray<string> {
  const found: string[] = [];
  for (const entry of readdirSync(dir, { withFileTypes: true })) {
    const full = resolve(dir, entry.name);
    if (entry.isDirectory()) {
      if (full === REGISTRY_DIR || entry.name === "node_modules") continue;
      found.push(...textFilesUnder(full));
      continue;
    }
    if (!entry.isFile()) continue;
    if (BINARY_EXTENSIONS.has(extname(entry.name).toLowerCase())) continue;
    if (statSync(full).size > MAX_SCANNED_BYTES) continue;
    found.push(full);
  }
  return found;
}

/**
 * Hostnames the tunnel spec declares. Read by regex rather than by parsing into
 * a shape, to match how the rest of this file reads config and to keep the
 * assertion honest if the file grows keys.
 */
function specHostnames(source: string): ReadonlySet<string> {
  const served = [...source.matchAll(/"hostname"\s*:\s*"([^"]+)"/g)].map(
    (match) => match[1] ?? ""
  );
  const unmanagedBlock = /"unmanaged"\s*:\s*\{([^}]*)\}/.exec(source);
  const unmanaged =
    unmanagedBlock === null
      ? []
      : [...(unmanagedBlock[1] ?? "").matchAll(/"([^"]+)"\s*:/g)].map(
          (match) => match[1] ?? ""
        );
  return new Set([...served, ...unmanaged].map((host) => host.toLowerCase()));
}

interface HostnameUse {
  readonly file: string;
  readonly hostname: string;
}

function hostnameUsesUnderDeploy(): ReadonlyArray<HostnameUse> {
  return textFilesUnder(DEPLOY_ROOT).flatMap((file) =>
    [...read(file).matchAll(ZONE_HOSTNAME)].map((match) => ({
      file: relative(REPO_ROOT, file),
      hostname: match[0].toLowerCase(),
    }))
  );
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

  it("is a hostname the tunnel spec actually serves", () => {
    const declared = [...specHostnames(read(SPEC_PATH))];
    expect(declared).toContain(new URL(workerVar).hostname.toLowerCase());
  });
});

describe("deploy configuration hostnames", () => {
  const declared = specHostnames(read(SPEC_PATH));
  const uses = hostnameUsesUnderDeploy();

  it("parses the tunnel spec and finds hostnames to check", () => {
    expect(declared.size, "no hostnames parsed out of tunnel-ingress.json").toBeGreaterThan(0);
    expect(uses.length, "no hostnames found under deploy/, scan is not looking at anything").toBeGreaterThan(0);
  });

  it("names only hostnames the tunnel spec says exist", () => {
    const unknown = uses.filter((use) => !declared.has(use.hostname));
    expect(
      unknown.map((use) => `${use.file} names ${use.hostname}`),
      "every scubed.co hostname under deploy/ must appear in deploy/cloudflare/tunnel-ingress.json, " +
        "either as a tunnel ingress rule or under dns.unmanaged. An unknown hostname here is the " +
        "cp-hive defect: config naming a host that no longer reaches this deployment."
    ).toEqual([]);
  });

  it("never names the retired host", () => {
    const reintroduced = uses.filter((use) => use.hostname === RETIRED_HOST);
    expect(
      reintroduced.map((use) => use.file),
      `${RETIRED_HOST} was deleted from DNS and must not come back in deploy configuration`
    ).toEqual([]);
  });
});
