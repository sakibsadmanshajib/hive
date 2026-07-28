#!/usr/bin/env node
// Structural guard: every LiteLLM fallback target must be a real route.
//
// deploy/litellm/config.yaml's litellm_settings.fallbacks list reroutes a
// failing model group to another one, e.g.
//   - route-openrouter-embedding: [route-openrouter-embedding-fallback]
// Both the source key and every name in its target array are model_name
// values that must exist in model_list. A typo or a removed model_list entry
// leaves a fallback pointing at nothing: LiteLLM only discovers this at
// request time ("No fallback model group found for original
// model_group=..."), after the primary route has already failed, which is
// the worst possible moment to learn the safety net is missing.
//
// This does not flag a group with no fallback of its own (the last entry in
// a cascade, e.g. route-openrouter-embedding-fallback today) -- that is a
// deliberate terminal node, not a dangling reference. It only flags a
// fallback list that NAMES a model_name absent from model_list.
//
// Mirrors the shape of the other tools/lint-*.mjs scanners: self-test first
// so a broken detector fails loudly instead of turning this into a lint that
// always passes.

import { readFileSync } from "node:fs";
import { parse } from "yaml";
import assert from "node:assert/strict";

const CONFIG_PATH = "deploy/litellm/config.yaml";

// Returns the list of {source, target} pairs this config's fallbacks
// declare, and the set of model_name values model_list actually defines.
function extractFallbackRefs(doc) {
  const modelNames = new Set(
    (doc.model_list ?? []).map((entry) => entry.model_name),
  );
  const pairs = [];
  for (const entry of doc.litellm_settings?.fallbacks ?? []) {
    for (const [source, targets] of Object.entries(entry)) {
      for (const target of targets) {
        pairs.push({ source, target });
      }
    }
  }
  return { modelNames, pairs };
}

function findDanglingRefs(source) {
  const doc = parse(source);
  const { modelNames, pairs } = extractFallbackRefs(doc);
  const dangling = [];
  for (const { source: from, target: to } of pairs) {
    if (!modelNames.has(from)) dangling.push(`fallback source '${from}' is not a model_list model_name`);
    if (!modelNames.has(to)) dangling.push(`fallback target '${to}' (from '${from}') is not a model_list model_name`);
  }
  return dangling;
}

function selfTest() {
  const valid = `
model_list:
  - model_name: route-a
    litellm_params: {model: x}
  - model_name: route-b
    litellm_params: {model: y}
litellm_settings:
  fallbacks:
    - route-a: [route-b]
`;
  assert.deepEqual(
    findDanglingRefs(valid),
    [],
    "detector flags a fallback chain where every name resolves",
  );

  const danglingTarget = `
model_list:
  - model_name: route-a
    litellm_params: {model: x}
litellm_settings:
  fallbacks:
    - route-a: [route-a-fallback]
`;
  assert.equal(
    findDanglingRefs(danglingTarget).length,
    1,
    "detector misses a fallback target absent from model_list",
  );

  const danglingSource = `
model_list:
  - model_name: route-b
    litellm_params: {model: y}
litellm_settings:
  fallbacks:
    - route-a: [route-b]
`;
  assert.equal(
    findDanglingRefs(danglingSource).length,
    1,
    "detector misses a fallback source absent from model_list",
  );

  const terminalNodeIsFine = `
model_list:
  - model_name: route-a
    litellm_params: {model: x}
  - model_name: route-b
    litellm_params: {model: y}
litellm_settings:
  fallbacks:
    - route-a: [route-b]
`;
  // route-b has no fallback of its own -- a deliberate terminal node, not a
  // defect, and must not be flagged.
  assert.deepEqual(
    findDanglingRefs(terminalNodeIsFine),
    [],
    "detector wrongly flags a terminal fallback node (one with no fallback of its own)",
  );
}

selfTest();
if (process.argv.includes("--self-test")) {
  console.log("lint-litellm-config-fallbacks: SELF-TEST PASS");
  process.exit(0);
}

const dangling = findDanglingRefs(readFileSync(CONFIG_PATH, "utf8"));
if (dangling.length > 0) {
  console.error(`${CONFIG_PATH}: dangling LiteLLM fallback reference(s):`);
  for (const msg of dangling) console.error(`  - ${msg}`);
  console.error(
    "\n  Every fallback source and target must be a model_name defined in model_list.\n" +
      "  Fix the typo, or add/restore the missing model_list entry.",
  );
  process.exit(1);
}
console.log("lint-litellm-config-fallbacks: PASS");
