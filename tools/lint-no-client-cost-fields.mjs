#!/usr/bin/env node
// Structural guard: no cost, USD/FX, or provider-identity field may reach a
// customer-bound Go struct.
//
// Two hard product rules this backs, per CLAUDE.md:
//   * Provider-blind errors: provider names never leak to customers. Errors
//     and responses are sanitized at both the control-plane and edge-api
//     boundaries.
//   * Regulatory: BD customers must never see FX rates, currency-exchange
//     language, or USD amounts. `amount_usd` was stripped from every
//     customer-bound surface by PR #137 (2026-05-09); the dedicated FX guard
//     that followed it was deleted by owner decision on 2026-07-19, and its
//     USD-absence assertions now live inside the broader billing functional
//     tests. This lint is the cheap structural guard, scoped to what a lint
//     can actually prove (a field name reaching a JSON struct tag) -- it does
//     not replace those functional tests.
//
// False-positive shape this lint must not repeat (the deleted FX guard was
// over-eager and got disabled): a JSON tag exactly named "provider" is
// forbidden, but a field that merely CONTAINS "provider" as a substring
// (provider_intent_id, provider_event_id -- payment-gateway ids, a different
// concept) is not. Likewise "cost_credits" (Hive's internal cost basis) is
// forbidden, but "price_credits" (input_price_credits/output_price_credits,
// what the customer is shown and expected to see) is not -- "cost" is what
// Hive pays upstream, "price" is what the customer pays, and conflating them
// would flag the catalog response on every run.
//
// Some JSON-tagged Go structs carrying these exact field names are
// deliberately internal: service-to-service payloads over an
// internal-token-gated endpoint, an Asynq job-queue message, or (once Wave 1b
// of this spec lands) a log-only shadow-mode verdict row. None of those are
// ever marshaled into an HTTP response a customer receives, so they are
// allowlisted by path below, each with its own justification, the same shape
// tools/lint-no-direct-tenant-id.mjs already uses for its own internal
// service-to-service exceptions.
//
// Not yet wired into CI (see .github/workflows/*): Wave 4 of this spec owns
// that wiring. Runnable standalone today via `npm run lint:no-client-cost-fields`.

import { readFileSync } from "node:fs";
import { execSync } from "node:child_process";
import assert from "node:assert/strict";
import { fileURLToPath } from "node:url";

// Exact JSON struct-tag names that must never reach a customer response.
// Matched against the literal tag value (optionally with ",omitempty"), not
// as a substring -- see the false-positive note above.
const FORBIDDEN_FIELDS = [
  {
    tag: "provider",
    re: /json:"provider(,omitempty)?"/,
    why: "names the upstream provider (OpenRouter, Groq, ...); provider identity must never leak to a customer",
  },
  {
    tag: "provider_name",
    re: /json:"provider_name(,omitempty)?"/,
    why: "same as provider: names the upstream provider",
  },
  {
    tag: "upstream_provider",
    re: /json:"upstream_provider(,omitempty)?"/,
    why: "same as provider: names the upstream provider",
  },
  {
    tag: "cost_credits",
    re: /json:"cost_credits(,omitempty)?"/,
    why: "Hive's internal cost basis (what Hive pays), distinct from the customer-facing input_price_credits/output_price_credits fields",
  },
  {
    tag: "cost_usd",
    re: /json:"cost_usd(,omitempty)?"/,
    why: "internal cost basis denominated in USD; must never reach a customer response",
  },
  {
    tag: "upstream_cost",
    re: /json:"upstream_cost(,omitempty)?"/,
    why: "internal cost basis; must never reach a customer response",
  },
  {
    tag: "amount_usd",
    re: /json:"amount_usd(,omitempty)?"/,
    why: "the exact field PR #137 stripped from every BD-customer-bound surface; regulatory bar on FX/USD language for BD customers",
  },
  {
    tag: "usd",
    re: /json:"usd(,omitempty)?"/,
    why: "a bare USD amount; regulatory bar on FX/USD language for BD customers",
  },
  {
    tag: "fx_rate",
    re: /json:"fx_rate(,omitempty)?"/,
    why: "an FX rate; regulatory bar on currency-exchange language for BD customers",
  },
  {
    tag: "exchange_rate",
    re: /json:"exchange_rate(,omitempty)?"/,
    why: "an FX rate; regulatory bar on currency-exchange language for BD customers",
  },
];

// Paths allowed to carry these exact field names because the struct they sit
// on is never marshaled into a customer-bound HTTP response. A directory
// entry (trailing "/") matches by prefix; anything else matches the exact
// file.
const ALLOWLIST = [
  // Internal service-to-service payload for the /internal/routing/select
  // endpoint, gated by a shared internal token (not a customer-facing
  // route). tools/lint-no-direct-tenant-id.mjs already grants this same
  // directory the same posture for the same reason.
  "apps/control-plane/internal/routing/",
  // Client-side counterpart of that same internal endpoint.
  "apps/edge-api/internal/inference/routing_client.go",
  // Asynq internal job-queue payload (TypeBatchPoll/TypeBatchExecute);
  // serialized for the queue, never marshaled into an HTTP response.
  "apps/control-plane/internal/batchstore/",
  // Wave 1b of this spec's shadow-mode verdict logger (not yet merged as of
  // this PR). metering_shadow_verdicts is a log-only, service-role-only
  // table (spec section 3.3/4): deliberately internal, never read by any
  // customer-facing path.
  "apps/edge-api/internal/metering/",
  "tools/lint-no-client-cost-fields.mjs",
  "tools/lint-no-client-cost-fields.test.mjs",
];

export function isAllowlistedPath(filePath) {
  return ALLOWLIST.some((entry) =>
    entry.endsWith("/") ? filePath.startsWith(entry) : filePath === entry,
  );
}

export function findClientCostFieldViolations(filePath, source) {
  if (isAllowlistedPath(filePath)) return [];
  const violations = [];
  const lines = source.split("\n");
  lines.forEach((line, index) => {
    for (const field of FORBIDDEN_FIELDS) {
      if (field.re.test(line)) {
        violations.push({
          file: filePath,
          lineNumber: index + 1,
          tag: field.tag,
          why: field.why,
          line: line.trim(),
        });
      }
    }
  });
  return violations;
}

function selfTest() {
  const offending = `
    type ChatCompletionResponse struct {
      Model    string \`json:"model"\`
      Provider string \`json:"provider"\`
    }
  `;
  assert.equal(
    findClientCostFieldViolations("apps/edge-api/internal/chat/response.go", offending).length,
    1,
    "detector no longer recognises a provider field on a customer-bound struct; this lint would be vacuous",
  );

  const clean = `
    type ChatCompletionResponse struct {
      Model string \`json:"model"\`
      Usage Usage  \`json:"usage"\`
    }
  `;
  assert.equal(
    findClientCostFieldViolations("apps/edge-api/internal/chat/response.go", clean).length,
    0,
    "detector flags a clean customer-facing struct",
  );

  assert.equal(
    isAllowlistedPath("apps/control-plane/internal/routing/types.go"),
    true,
    "internal routing service-to-service payload is no longer allowlisted",
  );
}

selfTest();
if (process.argv.includes("--self-test")) {
  console.log("lint-no-client-cost-fields: SELF-TEST PASS");
  process.exit(0);
}

// The repo-wide scan (and its process.exit) only runs when this file is
// invoked directly, so tools/lint-no-client-cost-fields.test.mjs can import
// the functions above without triggering a full scan as a side effect.
const isMain = process.argv[1] && fileURLToPath(import.meta.url) === process.argv[1];
if (isMain) {
  const files = execSync("git ls-files", { encoding: "utf8" })
    .split("\n")
    .filter(Boolean)
    .filter((f) => f.startsWith("apps/") && f.endsWith(".go") && !f.endsWith("_test.go"));

  if (files.length === 0) {
    console.error("no Go files found under apps/; the lint is scanning the wrong tree");
    process.exit(1);
  }

  let violations = 0;
  for (const file of files) {
    const source = readFileSync(file, "utf8");
    for (const v of findClientCostFieldViolations(file, source)) {
      violations++;
      console.error(`${file}:${v.lineNumber}: forbidden client-visible ${v.tag} field -- ${v.why}\n  ${v.line}`);
    }
  }

  if (violations > 0) {
    console.error(`\n${violations} client-visible cost/provider-field violation(s).`);
    process.exit(1);
  }
  console.log("lint-no-client-cost-fields: PASS");
}
