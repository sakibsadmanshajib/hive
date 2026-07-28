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
 *   node deploy/cloudflare/reconcile-tunnel-dns.mjs --apply    # confirm, then push ingress
 *   node deploy/cloudflare/reconcile-tunnel-dns.mjs --apply --yes   # non-interactive
 *
 * Requires CLOUDFLARE_API_TOKEN, CLOUDFLARE_ACCOUNT_ID and CLOUDFLARE_ZONE_ID.
 * Check needs read scopes only; --apply additionally needs tunnel edit scope.
 *
 * --apply always reads and prints the full plan first, including every rule it
 * would REMOVE, and then waits for explicit confirmation. That ordering is not
 * cosmetic: the configurations endpoint replaces tunnel ingress wholesale, so a
 * live rule missing from this spec is deleted by the write. An apply with an
 * empty plan performs no write at all.
 *
 * DNS records are never created or deleted by this script in any mode. The
 * check prints the exact record to act on and a human does it, because deleting
 * a DNS record is not a reversible-by-retry operation.
 */

import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { createInterface } from "node:readline/promises";
import { fileURLToPath } from "node:url";

const HERE = dirname(fileURLToPath(import.meta.url));
const SPEC_PATH = resolve(HERE, "tunnel-ingress.json");
const API = "https://api.cloudflare.com/client/v4";

// Only address records make a hostname resolve to an origin, so only these can
// produce the 522-instead-of-404 failure the orphan check hunts for. A TXT, MX
// or CAA record under a managed name (`_acme-challenge.api-hive.scubed.co`, for
// instance) is not an ingress problem and must not be reported as one.
const ADDRESS_RECORD_TYPES = new Set(["A", "AAAA", "CNAME"]);

const CATCH_ALL = "<catch-all>";

const apply = process.argv.includes("--apply");
const assumeYes = process.argv.includes("--yes");

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

// Cloudflare returns object keys in its own order, so compare on a canonical
// form. Without this every rule reads as changed because the live payload spells
// {service, hostname} and the spec spells {hostname, service}.
function canonical(value) {
  if (Array.isArray(value)) return value.map(canonical);
  if (value !== null && typeof value === "object") {
    return Object.fromEntries(
      Object.keys(value)
        .sort()
        .map((key) => [key, canonical(value[key])])
    );
  }
  return value;
}

function same(a, b) {
  return JSON.stringify(canonical(a)) === JSON.stringify(canonical(b));
}

function show(value) {
  return value === undefined ? "(absent)" : JSON.stringify(value);
}

// Which fields of a rule differ, so a "change" line says what actually changes
// rather than repeating the same service twice.
function fieldDiffs(live, desired) {
  const keys = [...new Set([...Object.keys(live), ...Object.keys(desired)])].sort();
  return keys
    .filter((key) => key !== "hostname" && !same(live[key], desired[key]))
    .map((key) => `${key} ${show(live[key])} => ${show(desired[key])}`);
}

function ruleKey(rule) {
  return typeof rule.hostname === "string" ? rule.hostname : CATCH_ALL;
}

function describe(rule) {
  return `${ruleKey(rule)} -> ${rule.service}`;
}

function byKey(ingress) {
  return new Map(ingress.map((rule) => [ruleKey(rule), rule]));
}

/**
 * What a PUT of `desired` would do to `live`. Includes `drops`: top-level
 * config keys that exist live and are absent from the spec, which the wholesale
 * replace would silently discard.
 */
function planFor(liveConfig, desired) {
  const liveIngress = liveConfig?.ingress ?? [];
  const live = byKey(liveIngress);
  const want = byKey(desired.ingress);

  const add = [];
  const change = [];
  const remove = [];

  for (const [key, rule] of want) {
    const current = live.get(key);
    if (current === undefined) {
      add.push(describe(rule));
    } else if (!same(current, rule)) {
      change.push(`${key}: ${fieldDiffs(current, rule).join(", ")}`);
    }
  }
  for (const [key, rule] of live) {
    if (!want.has(key)) remove.push(describe(rule));
  }

  const drops = Object.keys(liveConfig ?? {}).filter(
    (key) => key !== "ingress" && !(key in desired)
  );

  const settings = Object.keys(desired)
    .filter((key) => key !== "ingress" && !same(liveConfig?.[key], desired[key]))
    .map((key) => `config.${key} ${show(liveConfig?.[key])} => ${show(desired[key])}`);

  return { add, change, remove, drops, settings };
}

function planIsEmpty(plan) {
  return (
    plan.add.length === 0 &&
    plan.change.length === 0 &&
    plan.remove.length === 0 &&
    plan.drops.length === 0 &&
    plan.settings.length === 0
  );
}

function printPlan(plan) {
  console.log("tunnel ingress plan (spec versus live Cloudflare):");
  if (planIsEmpty(plan)) {
    console.log("  no ingress changes: live config already matches the spec");
    return;
  }
  for (const line of plan.add) console.log(`  + add     ${line}`);
  for (const line of plan.change) console.log(`  ~ change  ${line}`);
  for (const line of plan.settings) console.log(`  ~ change  ${line}`);
  for (const line of plan.remove) {
    console.log(`  - REMOVE  ${line}   (live rule absent from the spec)`);
  }
  for (const key of plan.drops) {
    console.log(`  - DROP    config.${key}   (live config key absent from the spec)`);
  }
}

async function confirmApply(plan) {
  const destructive = plan.remove.length + plan.drops.length;
  const summary =
    `About to replace tunnel ingress: ${plan.add.length} added, ` +
    `${plan.change.length + plan.settings.length} changed, ${destructive} removed or dropped.`;

  if (assumeYes) {
    console.log(`${summary} Proceeding because --yes was passed.`);
    return true;
  }
  if (process.stdin.isTTY !== true) {
    console.error(
      `${summary}\nRefusing to apply: stdin is not a terminal, so the plan cannot be ` +
        "confirmed. Re-run interactively, or pass --yes if you have already reviewed " +
        "the plan above."
    );
    return false;
  }

  const rl = createInterface({ input: process.stdin, output: process.stdout });
  const answer = await rl.question(`${summary}\nType "apply" to proceed: `);
  rl.close();
  if (answer.trim() !== "apply") {
    console.error("Aborted. Nothing was written to Cloudflare.");
    return false;
  }
  return true;
}

// --- spec ------------------------------------------------------------------

const spec = JSON.parse(readFileSync(SPEC_PATH, "utf8"));
const tunnelId = spec.tunnel_id;
const managedSuffix = spec.dns.managed_suffix;
const unmanaged = new Set(Object.keys(spec.dns.unmanaged ?? {}));
const expectedTarget = `${tunnelId}.cfargotunnel.com`;

const desiredConfig = stripAnnotations(spec.config);
const desiredHosts = desiredConfig.ingress
  .filter((rule) => typeof rule.hostname === "string")
  .map((rule) => rule.hostname);

const token = requireEnv("CLOUDFLARE_API_TOKEN");
const accountId = requireEnv("CLOUDFLARE_ACCOUNT_ID");
const zoneId = requireEnv("CLOUDFLARE_ZONE_ID");

const CONFIG_PATH = `/accounts/${accountId}/cfd_tunnel/${tunnelId}/configurations`;

// --- read live state, then diff. Never write before both are printed. -------

const liveBeforeApply = await cf(token, CONFIG_PATH);
const plan = planFor(liveBeforeApply?.config, desiredConfig);

printPlan(plan);
console.log("");

// --- DNS: every managed hostname must be a proxied CNAME to the tunnel -----

const records = await cf(token, `/zones/${zoneId}/dns_records?per_page=200`);
const byName = new Map(records.map((r) => [r.name, r]));
const dnsProblems = [];

for (const host of desiredHosts) {
  const record = byName.get(host);
  if (record === undefined) {
    dnsProblems.push(`DNS missing: ${host} has an ingress rule but no DNS record`);
    continue;
  }
  if (record.type !== "CNAME" || record.content !== expectedTarget) {
    dnsProblems.push(
      `DNS wrong target: ${host} is ${record.type} -> ${record.content}, expected CNAME -> ${expectedTarget} (record id ${record.id})`
    );
  }
  if (record.proxied !== true) {
    dnsProblems.push(`DNS not proxied: ${host} must be proxied for the tunnel to serve it`);
  }
}

for (const record of records) {
  if (!ADDRESS_RECORD_TYPES.has(record.type)) continue;
  if (!record.name.endsWith(managedSuffix)) continue;
  if (unmanaged.has(record.name)) continue;
  if (desiredHosts.includes(record.name)) continue;
  dnsProblems.push(
    `orphaned DNS record: ${record.name} is ${record.type} -> ${record.content} with no tunnel ingress rule. ` +
      `It will time out (522) instead of 404ing. Delete it, or add it to tunnel-ingress.json. Record id ${record.id}`
  );
}

function reportDNS() {
  if (dnsProblems.length === 0) {
    console.log(
      `DNS OK: ${desiredHosts.length} hostnames are proxied CNAMEs to the tunnel; no orphaned ${managedSuffix} address records.`
    );
    return;
  }
  console.error(`DNS DRIFT: ${dnsProblems.length} problem(s)\n`);
  for (const problem of dnsProblems) console.error(`  - ${problem}`);
  console.error(
    "\nDNS changes are manual on purpose: this script never creates or deletes records."
  );
}

// --- check mode: report and exit -------------------------------------------

if (!apply) {
  reportDNS();
  if (planIsEmpty(plan) && dnsProblems.length === 0) {
    console.log("\nOK: tunnel ingress and DNS match tunnel-ingress.json.");
    process.exit(0);
  }
  console.error(
    "\nDRIFT: fix the spec or Cloudflare, then re-run. Ingress can be pushed with --apply."
  );
  process.exit(1);
}

// --- apply mode: confirm, write, then verify against a fresh read -----------

if (planIsEmpty(plan)) {
  console.log("Nothing to apply: tunnel ingress already matches the spec. No write issued.");
  reportDNS();
  process.exit(dnsProblems.length === 0 ? 0 : 1);
}

if (!(await confirmApply(plan))) {
  reportDNS();
  process.exit(1);
}

await cf(token, CONFIG_PATH, {
  method: "PUT",
  body: JSON.stringify({ config: desiredConfig }),
});
console.log(`applied ${desiredConfig.ingress.length} ingress rules to tunnel ${tunnelId}`);

const liveAfterApply = await cf(token, CONFIG_PATH);
const residual = planFor(liveAfterApply?.config, desiredConfig);
if (!planIsEmpty(residual)) {
  console.error("\nFAIL: Cloudflare did not accept the full spec. Remaining difference:");
  printPlan(residual);
  process.exit(1);
}
console.log("verified: live tunnel ingress now matches tunnel-ingress.json.");

reportDNS();
process.exit(dnsProblems.length === 0 ? 0 : 1);
