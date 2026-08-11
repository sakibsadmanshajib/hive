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

// A file mentions the LiteLLM proxy at all: its address, its credential, its
// client type, a receiver named after it, or the environment variables carrying
// it. Deliberately the widest possible match rather than a list of the
// identifier shapes that happen to exist today, because this repo's origin lint
// taught it the hard way that a guard matching only the shapes already fixed
// catches no new variant. A file mentioning LiteLLM only in a comment is
// harmless: it is flagged only if it also dispatches (see DISPATCH), and a false
// positive costs a reviewer one line of explanation while a false negative costs
// unmetered inference.
const LITELLM_TARGET = /litellm/i;

// ...and actually sends the request. Covers a hand-built request on any client
// (including a fresh http.Client rather than an injected one), the convenience
// helpers, and dispatch through the shared LiteLLM client's methods, which is
// how a new handler would most plausibly reach the proxy without writing any
// HTTP itself.
const DISPATCH = new RegExp([
  "http\\.NewRequest",
  "http\\.Post",
  "http\\.Get",
  "http\\.Client\\{",
  "\\.Do\\(",
  "dispatchWithRetry",
  "\\.ChatCompletion\\(",
  "\\.Completion\\(",
  "\\.Embeddings\\(",
  "\\.Speech\\(",
  "\\.ImageGeneration\\(",
  "\\.ImageEditRaw\\(",
  "\\.TranscriptionRaw\\(",
  "\\.dispatch\\(",
].join("|"));

// ...and resolves a client alias to a route.
const ROUTE_SELECTION = /SelectRoute/;

const SCAN_PREFIX = "apps/edge-api/internal/";

// The single wiring site where the LiteLLM address and credential enter the
// process. Whatever a handler calls its own field, its value comes from here, so
// this is the choke point that catches a dispatcher whose own identifiers avoid
// the word "litellm" entirely.
const WIRING_FILE = "apps/edge-api/cmd/server/main.go";
const WIRING_RESOLVERS = /resolveLiteLLM(BaseURL|MasterKey)\(\)/;
const PACKAGE_QUALIFIER = /\b([a-z][a-z0-9]*)\.[A-Z][A-Za-z0-9]*/g;

function dispatchesToLiteLLM(source) {
  return LITELLM_TARGET.test(source) && DISPATCH.test(source);
}

// packagesGivenTheLiteLLMTarget returns the package qualifiers that receive the
// LiteLLM address or master key at the wiring site, e.g. "chat" from
// `chat.NewDispatch(chat.Deps{ ... LiteLLMURL: resolveLiteLLMBaseURL() ... })`.
//
// Attribution is to the type of the composite literal or call that LEXICALLY
// ENCLOSES the resolver, found by walking back with a brace/paren counter to the
// opener's exact line AND column, then reading the qualified type immediately to
// its left. Column accuracy is load bearing: on a single-line literal the last
// qualifier anywhere on the line is a sibling field's expression, not the
// literal's own type. The
// earlier version took the nearest package qualifier on any preceding line
// instead, which is unsound in the direction that matters: adding an unrelated
// package-qualified sibling field above the credential line moved attribution
// off the real recipient and onto that sibling. That is a false positive on the
// sibling AND a false negative on the package actually handed the credential,
// which is the entire point of this rule. See the self-test cases below.
function enclosingOpener(lines, fromLine, fromColumn) {
  let depth = 0;
  for (let back = fromLine; back >= 0; back--) {
    const line = back === fromLine ? lines[back].slice(0, fromColumn) : lines[back];
    for (let i = line.length - 1; i >= 0; i--) {
      const ch = line[i];
      if (ch === "}" || ch === ")") depth++;
      else if (ch === "{" || ch === "(") {
        if (depth === 0) return { line: back, column: i };
        depth--;
      }
    }
  }
  return null;
}

// qualifierBefore returns the package qualifier of the type or function named
// immediately to the left of an opener, e.g. "rogue" for the `{` in
// `rogue.NewHandler(rogue.Deps{`. Matching on the whole line instead would pick
// the last qualifier anywhere on it, which on a single-line literal is a
// sibling field's expression rather than the literal's own type.
function qualifierBefore(line, column) {
  const candidates = [...line.matchAll(PACKAGE_QUALIFIER)].filter(
    (m) => m.index + m[0].length <= column,
  );
  return candidates.length > 0 ? candidates[candidates.length - 1][1] : null;
}

function packagesGivenTheLiteLLMTarget(source) {
  const lines = source.split("\n");
  const packages = new Set();
  lines.forEach((line, index) => {
    const at = line.search(WIRING_RESOLVERS);
    if (at < 0) return;
    // Walk outward through nested literals until one names a package-qualified
    // type. `Billing: &metering.PGBillingAccountResolver{...}` sitting above or
    // beside the credential is a sibling field, never the recipient; the
    // recipient is the literal both fields belong to.
    let cursor = { line: index, column: at };
    while (cursor) {
      const opener = enclosingOpener(lines, cursor.line, cursor.column);
      if (!opener) return;
      const pkg = qualifierBefore(lines[opener.line], opener.column);
      if (pkg) {
        packages.add(pkg);
        return;
      }
      cursor = opener;
    }
  });
  return packages;
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

  // Variants a future handler could plausibly be written in. Each must still be
  // caught, because the origin lint in this repo taught us that a guard matching
  // only the shapes already fixed is a guard that catches nothing new.
  const variants = {
    "its own http.Client rather than an injected one": `
      client := &http.Client{Timeout: time.Minute}
      resp, err := client.Post(cfg.LiteLLMBaseURL+"/v1/chat/completions", "application/json", body)
    `,
    "the shared LiteLLM client's method, writing no HTTP itself": `
      resp, err := h.litellmClient.ChatCompletion(ctx, req.Model, body)
    `,
    "the retry helper": `
      resp, err := dispatchWithRetry(ctx, req.Model, body, h.litellm.ChatCompletion)
    `,
    "a plain http.Post to an env-sourced address": `
      base := os.Getenv("LITELLM_BASE_URL")
      resp, err := http.Post(base+"/chat/completions", "application/json", body)
    `,
    "a differently named field on a LiteLLM client type": `
      upstream, err := http.NewRequestWithContext(ctx, http.MethodPost, c.proxyBase+"/chat/completions", body)
      resp, err := c.transport.Do(upstream)
      var _ LiteLLMClient
    `,
  };
  for (const [label, sample] of Object.entries(variants)) {
    assert.equal(dispatchesToLiteLLM(sample), true, `detector misses ${label}`);
  }

  // The wiring-site rule: a handler whose own identifiers avoid the word
  // "litellm" is still caught, because the address it is handed comes from the
  // one resolver in main.go.
  const wiring = `
	rogueHandler := rogue.NewHandler(rogue.Deps{
		UpstreamURL: resolveLiteLLMBaseURL(),
		UpstreamKey: resolveLiteLLMMasterKey(),
	})
  `;
  const wired = packagesGivenTheLiteLLMTarget(wiring);
  assert.equal(
    wired.has("rogue"),
    true,
    "wiring-site rule no longer attributes the LiteLLM address to the package that receives it",
  );

  // ...and it must still name the recipient when an unrelated package-qualified
  // field sits between the literal's opening line and the credential. This is
  // the shape that broke the previous nearest-preceding-qualifier rule: it
  // attributed the credential to `telemetry`, which is both a false positive on
  // a package that never receives it and, worse, a false negative on `rogue`,
  // which does. A guard that can be pointed away from the real recipient by
  // inserting a sibling field is a guard that can be silenced on purpose.
  const wiringWithSibling = `
	rogueHandler := rogue.NewHandler(rogue.Deps{
		Metrics:     telemetry.NewCounter(),
		UpstreamURL: resolveLiteLLMBaseURL(),
		UpstreamKey: resolveLiteLLMMasterKey(),
	})
  `;
  const wiredWithSibling = packagesGivenTheLiteLLMTarget(wiringWithSibling);
  assert.equal(
    wiredWithSibling.has("rogue"),
    true,
    "a package-qualified sibling field moves attribution off the package handed the credential",
  );
  assert.equal(
    wiredWithSibling.has("telemetry"),
    false,
    "attribution leaks onto a sibling field's package, which never receives the credential",
  );

  // A nested literal between the two is the same trap one level deeper.
  const wiringWithNestedSibling = `
	rogueHandler := rogue.NewHandler(rogue.Deps{
		Billing: &metering.PGBillingAccountResolver{
			Pool: dbPool,
		},
		UpstreamURL: resolveLiteLLMBaseURL(),
	})
  `;
  const wiredNested = packagesGivenTheLiteLLMTarget(wiringWithNestedSibling);
  assert.equal(
    wiredNested.has("rogue"),
    true,
    "a nested sibling literal moves attribution off the package handed the credential",
  );
  assert.equal(
    wiredNested.has("metering"),
    false,
    "attribution leaks onto a nested sibling literal's package",
  );

  // The same trap written on ONE line, where the sibling's qualifier sits to
  // the left of the credential rather than above it. Selecting the last
  // qualifier on the line records `telemetry` and lets `rogue` dispatch
  // unrouted forever.
  const wiringSameLine = `
	rogueHandler := rogue.NewHandler(rogue.Deps{Metrics: telemetry.NewCounter(), UpstreamURL: resolveLiteLLMBaseURL()})
  `;
  const wiredSameLine = packagesGivenTheLiteLLMTarget(wiringSameLine);
  assert.equal(
    wiredSameLine.has("rogue"),
    true,
    "a same-line sibling expression moves attribution off the package handed the credential",
  );
  assert.equal(
    wiredSameLine.has("telemetry"),
    false,
    "attribution leaks onto a same-line sibling expression's package",
  );

  // A credential passed straight into a call rather than a struct literal is
  // still attributed to the callee.
  const wiringPositional = `
	rogueHandler := rogue.NewHandler(resolveLiteLLMBaseURL(), resolveLiteLLMMasterKey())
  `;
  assert.equal(
    packagesGivenTheLiteLLMTarget(wiringPositional).has("rogue"),
    true,
    "a positional constructor argument is no longer attributed to its callee",
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

const EXPLANATION =
  `  LiteLLM model names are route ids, so an unresolved dispatch lets a caller address a\n` +
  `  route directly and skip per-tenant model entitlement, the API-key alias allowlist, and\n` +
  `  credit metering. Resolve the client alias through inference.RoutingClient.SelectRoute,\n` +
  `  or delegate to a handler that does.`;

let violations = 0;
for (const [pkgDir, offenders] of dispatchers) {
  if (resolvers.has(pkgDir)) continue;
  violations++;
  console.error(
    `${pkgDir}: dispatches to LiteLLM but never calls SelectRoute (${offenders.join(", ")})\n` +
      EXPLANATION,
  );
}

// Second rule: whoever is handed the LiteLLM address or master key at the wiring
// site must resolve aliases, whatever it calls its own fields.
const wiringSource = readFileSync(WIRING_FILE, "utf8");
const wiredPackages = packagesGivenTheLiteLLMTarget(wiringSource);
const knownPackages = new Map(); // package name -> dir
for (const file of files) {
  const pkgDir = file.slice(0, file.lastIndexOf("/"));
  knownPackages.set(pkgDir.slice(pkgDir.lastIndexOf("/") + 1), pkgDir);
}
for (const pkgName of wiredPackages) {
  const pkgDir = knownPackages.get(pkgName);
  if (!pkgDir) continue; // not an edge-api internal package (e.g. the inference transport itself)
  if (resolvers.has(pkgDir)) continue;
  violations++;
  console.error(
    `${pkgDir}: is handed the LiteLLM address or master key in ${WIRING_FILE} but never ` +
      `calls SelectRoute\n` + EXPLANATION,
  );
}

if (violations > 0) {
  console.error(`\n${violations} unrouted-LiteLLM-dispatch violation(s).`);
  process.exit(1);
}
console.log("lint-litellm-dispatch-requires-routing: PASS");
