#!/usr/bin/env node
// Structural guard: no LiteLLM dispatch without route selection.
//
// LiteLLM model names ARE route ids. deploy/litellm/config.yaml defines
// route-openrouter-default, route-groq-fast and friends as its model_list
// entries, so any handler that POSTs a client-supplied model straight at the
// LiteLLM proxy lets a caller name a route instead of a Hive alias. That single
// shortcut skips three controls at once:
//
//   * per-tenant model entitlement (enforced inside routing.Service.SelectRoute),
//   * the API-key alias allowlist (authz.CheckAccess, on the alias),
//   * prepaid credit reservation and settlement (the accounting calls the
//     inference orchestrator makes around a resolved route).
//
// It has now shipped twice in this family:
//   * apps/edge-api/internal/anthropic/handler.go POSTed the client's model to
//     LiteLLM with no SelectRoute at all (POST /v1/messages, fixed here).
//   * the same surface returned upstream errors before provider-blind
//     sanitisation was applied to it (PR #170).
//
// Review did not hold the line, so make it structural: an edge-api package that
// dispatches inference to LiteLLM must also resolve the client alias through
// SelectRoute. The check is package-scoped deliberately -- the transport
// (inference/litellm_client.go) legitimately holds no routing call, while the
// orchestrator in the same package does.
//
// `--self-test` asserts the detector still fires on the exact code that caused
// the defect and still ignores in-process delegation to the OpenAI chat path. It
// runs as a preflight on every invocation, so a broken regex fails loudly
// instead of turning this into a lint that always passes.
//
// Mirrors the shape of the other tools/lint-*.mjs scanners (dependency-free,
// git ls-files driven, non-zero exit on violation).

import { readFileSync } from "node:fs";
import { execSync } from "node:child_process";
import assert from "node:assert/strict";

// A file names the LiteLLM proxy: its address, its credential, or its client.
// A whitelist of identifier shapes, not a blacklist of one wrong expression, so
// renaming a variable cannot slip past.
const LITELLM_TARGET =
  /litellm(url|baseurl|base_url|base|key|masterkey|master_key|client)/i;

// ...and actually sends a request somewhere.
const DISPATCH = /http\.NewRequest|http\.Post|httpClient\.Do|HTTP\.Do|\.Do\(/;

// ...and resolves a client alias to a route.
const ROUTE_SELECTION = /SelectRoute/;

const SCAN_PREFIX = "apps/edge-api/internal/";

function dispatchesToLiteLLM(source) {
  return LITELLM_TARGET.test(source) && DISPATCH.test(source);
}

function selfTest() {
  const offending = `
    upstream, err := http.NewRequestWithContext(r.Context(), http.MethodPost,
      strings.TrimRight(h.deps.LiteLLMURL, "/")+"/v1/chat/completions", bytes.NewReader(body))
    resp, err := h.deps.HTTP.Do(upstream)
  `;
  assert.equal(
    dispatchesToLiteLLM(offending),
    true,
    "detector no longer recognises a direct LiteLLM dispatch; this lint would be vacuous",
  );

  const benign = `
    sub := r.Clone(r.Context())
    sub.URL.Path = "/v1/chat/completions"
    h.deps.OpenAIChat.ServeHTTP(translator, sub)
  `;
  assert.equal(
    dispatchesToLiteLLM(benign),
    false,
    "detector flags in-process delegation to the OpenAI chat path",
  );
}

selfTest();
if (process.argv.includes("--self-test")) {
  console.log("lint-litellm-dispatch-requires-routing: SELF-TEST PASS");
  process.exit(0);
}

const files = execSync("git ls-files", { encoding: "utf8" })
  .split("\n")
  .filter(Boolean)
  .filter((f) => f.startsWith(SCAN_PREFIX) && f.endsWith(".go") && !f.endsWith("_test.go"));

const dispatchers = new Map(); // package dir -> [files]
const resolvers = new Set(); // package dirs that call SelectRoute

for (const file of files) {
  const source = readFileSync(file, "utf8");
  const pkgDir = file.slice(0, file.lastIndexOf("/"));
  if (ROUTE_SELECTION.test(source)) resolvers.add(pkgDir);
  if (dispatchesToLiteLLM(source)) {
    dispatchers.set(pkgDir, [...(dispatchers.get(pkgDir) ?? []), file]);
  }
}

if (dispatchers.size === 0) {
  console.error(
    `no LiteLLM dispatch found under ${SCAN_PREFIX}; the lint is scanning the wrong tree`,
  );
  process.exit(1);
}

let violations = 0;
for (const [pkgDir, offenders] of dispatchers) {
  if (resolvers.has(pkgDir)) continue;
  violations++;
  console.error(
    `${pkgDir}: dispatches to LiteLLM but never calls SelectRoute (${offenders.join(", ")})\n` +
      `  LiteLLM model names are route ids, so an unresolved dispatch lets a caller address a\n` +
      `  route directly and skip per-tenant model entitlement, the API-key alias allowlist, and\n` +
      `  credit metering. Resolve the client alias through inference.RoutingClient.SelectRoute,\n` +
      `  or delegate to a handler that does.`,
  );
}

if (violations > 0) {
  console.error(`\n${violations} unrouted-LiteLLM-dispatch violation(s).`);
  process.exit(1);
}
console.log("lint-litellm-dispatch-requires-routing: PASS");
