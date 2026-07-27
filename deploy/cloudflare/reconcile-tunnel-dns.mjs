#!/usr/bin/env node
/**
 * reconcile-tunnel-dns.mjs
 *
 * Diffs deploy/cloudflare/tunnel-ingress.json against live Cloudflare, so the
 * public hostname map is reviewable in the repository instead of living only in
 * the Cloudflare dashboard.
 *
 * The defect this exists to catch: cp-hive.scubed.co was a proxied A record to
 * 40.233.86.19, a retired VM. It had no tunnel ingress rule at all, so every
 * request returned 522 (origin connection timeout) while the control-plane it
 * appeared to name was healthy under control-hive.scubed.co. A hostname that
 * resolves and then hangs reads as an outage, and nothing in the repository
 * described the mapping well enough for review to notice.
 *
 * Usage:
 *   node deploy/cloudflare/reconcile-tunnel-dns.mjs            # check, exit 1 on drift
 *   node deploy/cloudflare/reconcile-tunnel-dns.mjs --apply    # push ingress to Cloudflare
 *
 * Requires CLOUDFLARE_API_TOKEN, CLOUDFLARE_ACCOUNT_ID and CLOUDFLARE_ZONE_ID.
 * --apply writes tunnel ingress only. DNS records are never created or deleted
 * automatically; the check prints the exact record to act on and a human does
 * it, because deleting a DNS record is not a reversible-by-retry operation.
 */

import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const HERE = dirname(fileURLToPath(import.meta.url));
const SPEC_PATH = resolve(HERE, "tunnel-ingress.json");
const API = "https://api.cloudflare.com/client/v4";

const apply = process.argv.includes("--apply");

function requireEnv(name) {
  const value = process.env[name];
  if (value === undefined || value === "") {
    console.error(`FAIL: ${name} is not set`);
    process.exit(2);
  }
  return value;
}

async function cf(token, path, init) {
  const response = await fetch(`${API}${path}`, {
    ...init,
    headers: {
      Authorization: `Bearer ${token}`,
      "Content-Type": "application/json",
      ...(init?.headers ?? {}),
    },
  });
  const body = await response.json();
  if (body.success !== true) {
    const detail = (body.errors ?? []).map((e) => e.message).join("; ");
    throw new Error(`Cloudflare API ${path} failed: ${detail || response.status}`);
  }
  return body.result;
}

// Strip the "$"-prefixed annotation keys the spec uses for reviewer comments.
// They are documentation, not part of the Cloudflare payload.
function stripAnnotations(value) {
  if (Array.isArray(value)) return value.map(stripAnnotations);
  if (value !== null && typeof value === "object") {
    return Object.fromEntries(
      Object.entries(value)
        .filter(([key]) => !key.startsWith("$"))
        .map(([key, inner]) => [key, stripAnnotations(inner)])
    );
  }
  return value;
}

const spec = JSON.parse(readFileSync(SPEC_PATH, "utf8"));
const tunnelId = spec.tunnel_id;
const managedSuffix = spec.dns.managed_suffix;
const unmanaged = new Set(Object.keys(spec.dns.unmanaged ?? {}));
const expectedTarget = `${tunnelId}.cfargotunnel.com`;

const desiredConfig = stripAnnotations(spec.config);
const desiredIngress = desiredConfig.ingress;
const desiredHosts = desiredIngress
  .filter((rule) => typeof rule.hostname === "string")
  .map((rule) => rule.hostname);

const token = requireEnv("CLOUDFLARE_API_TOKEN");
const accountId = requireEnv("CLOUDFLARE_ACCOUNT_ID");
const zoneId = requireEnv("CLOUDFLARE_ZONE_ID");

const problems = [];

if (apply) {
  await cf(token, `/accounts/${accountId}/cfd_tunnel/${tunnelId}/configurations`, {
    method: "PUT",
    body: JSON.stringify({ config: desiredConfig }),
  });
  console.log(`applied ${desiredIngress.length} ingress rules to tunnel ${tunnelId}`);
}

// --- ingress: live vs spec -------------------------------------------------

const liveConfig = await cf(
  token,
  `/accounts/${accountId}/cfd_tunnel/${tunnelId}/configurations`
);
const liveIngress = (liveConfig?.config?.ingress ?? []).filter(
  (rule) => typeof rule.hostname === "string"
);
const liveByHost = new Map(liveIngress.map((rule) => [rule.hostname, rule.service]));

for (const rule of desiredIngress) {
  if (typeof rule.hostname !== "string") continue;
  const live = liveByHost.get(rule.hostname);
  if (live === undefined) {
    problems.push(`ingress missing in Cloudflare: ${rule.hostname} -> ${rule.service}`);
  } else if (live !== rule.service) {
    problems.push(
      `ingress mismatch: ${rule.hostname} serves ${live}, spec says ${rule.service}`
    );
  }
}
for (const rule of liveIngress) {
  if (!desiredHosts.includes(rule.hostname)) {
    problems.push(
      `ingress rule not in spec: ${rule.hostname} -> ${rule.service} (add it to tunnel-ingress.json or remove it from Cloudflare)`
    );
  }
}

// --- DNS: every managed hostname must be a proxied CNAME to the tunnel -----

const records = await cf(token, `/zones/${zoneId}/dns_records?per_page=200`);
const byName = new Map(records.map((r) => [r.name, r]));

for (const host of desiredHosts) {
  const record = byName.get(host);
  if (record === undefined) {
    problems.push(`DNS missing: ${host} has an ingress rule but no DNS record`);
    continue;
  }
  if (record.type !== "CNAME" || record.content !== expectedTarget) {
    problems.push(
      `DNS wrong target: ${host} is ${record.type} -> ${record.content}, expected CNAME -> ${expectedTarget} (record id ${record.id})`
    );
  }
  if (record.proxied !== true) {
    problems.push(`DNS not proxied: ${host} must be proxied for the tunnel to serve it`);
  }
}

for (const record of records) {
  if (!record.name.endsWith(managedSuffix)) continue;
  if (unmanaged.has(record.name)) continue;
  if (desiredHosts.includes(record.name)) continue;
  problems.push(
    `orphaned DNS record: ${record.name} is ${record.type} -> ${record.content} with no tunnel ingress rule. ` +
      `It will time out (522) instead of 404ing. Delete it, or add it to tunnel-ingress.json. Record id ${record.id}`
  );
}

// --- report ----------------------------------------------------------------

if (problems.length > 0) {
  console.error(`DRIFT: ${problems.length} problem(s) between tunnel-ingress.json and Cloudflare\n`);
  for (const problem of problems) console.error(`  - ${problem}`);
  console.error(
    "\nFix the spec or Cloudflare, then re-run. Ingress can be pushed with --apply; DNS changes are manual on purpose."
  );
  process.exit(1);
}

console.log(
  `OK: ${desiredHosts.length} hostnames match tunnel ingress and are proxied CNAMEs to the tunnel; no orphaned ${managedSuffix} records.`
);
