#!/usr/bin/env node
// Structural guard: an environment knob an SDK suite READS must be one its
// container is actually GIVEN.
//
// Why this exists (issue #1088). `ci.yml` set `HIVE_TEST_MODEL` in the
// live-integration job env, the suites read it
// (`process.env.HIVE_TEST_MODEL`, `os.getenv("HIVE_TEST_MODEL")`), and the
// `sdk-tests-*` compose services declared only `HIVE_BASE_URL` and
// `HIVE_API_KEY`. `docker compose run` passes a service nothing but its own
// declared environment, so the value never crossed into the container and
// every suite silently fell back to its literal default. The workflow, the
// comment above it claiming the value "propagates into sdk-tests containers",
// and the green checks were all consistent with a knob that did nothing. That
// is how the merge-gate check came to spend the live demo's provider budget on
// every run for a month: the lever meant to point it somewhere cheaper was
// wired to nothing.
//
// A knob that reads green while doing nothing is the defect class here, not
// one particular variable name, so this scans for the class: every HIVE_*
// variable any suite reads must be satisfied by the compose service that runs
// it, or by an ENV in the image that service builds. Either is a real value at
// runtime; neither present means the read can only ever see its fallback.
//
// Deliberately NOT checked: that ci.yml (or any other caller) sets the
// variable. A suite is allowed to run on its default, and a caller is allowed
// to leave a knob alone. What is not allowed is a caller setting one that
// cannot arrive.
//
// Mirrors the other tools/lint-*.mjs scanners: self-test first, so a broken
// detector fails loudly instead of turning this into a lint that always
// passes.

import { readFileSync } from "node:fs";
import { execFileSync } from "node:child_process";
import { parse } from "yaml";
import assert from "node:assert/strict";

const COMPOSE_PATH = "deploy/docker/docker-compose.yml";
const SUITES = [
  {
    service: "sdk-tests-js",
    dockerfile: "deploy/docker/Dockerfile.sdk-tests-js",
    sources: ["packages/sdk-tests/js"],
  },
  {
    service: "sdk-tests-py",
    dockerfile: "deploy/docker/Dockerfile.sdk-tests-py",
    sources: ["packages/sdk-tests/python"],
  },
  {
    service: "sdk-tests-java",
    dockerfile: "deploy/docker/Dockerfile.sdk-tests-java",
    sources: ["packages/sdk-tests/java"],
  },
];

// Every shape the three suites use to read an environment variable. Kept
// explicit rather than a catch-all `HIVE_[A-Z_]+` match over the whole file so
// a variable NAMED in a comment or in an assertion string is not mistaken for
// one that is read.
const READ_PATTERNS = [
  /process\.env\.(HIVE_[A-Z0-9_]+)/g,
  /process\.env\[["'](HIVE_[A-Z0-9_]+)["']\]/g,
  /os\.getenv\(\s*["'](HIVE_[A-Z0-9_]+)["']/g,
  /os\.environ\.get\(\s*["'](HIVE_[A-Z0-9_]+)["']/g,
  /os\.environ\[\s*["'](HIVE_[A-Z0-9_]+)["']\s*\]/g,
  /System\.getenv\(\s*"(HIVE_[A-Z0-9_]+)"/g,
];

export function readsIn(text) {
  const found = new Set();
  for (const pattern of READ_PATTERNS) {
    for (const match of text.matchAll(pattern)) found.add(match[1]);
  }
  return found;
}

// The names a compose service's `environment:` block provides. Both the map
// form (`KEY: value`) and the list form (`- KEY=value`, `- KEY`) are valid
// compose, so both are read.
export function providedByService(service) {
  const env = service?.environment;
  if (!env) return new Set();
  if (Array.isArray(env)) {
    return new Set(env.map((entry) => String(entry).split("=")[0].trim()));
  }
  return new Set(Object.keys(env));
}

export function providedByDockerfile(text) {
  const found = new Set();
  // `ENV KEY=value` and the legacy `ENV KEY value`, one per line.
  for (const match of text.matchAll(/^\s*ENV\s+([A-Z0-9_]+)[=\s]/gim)) {
    found.add(match[1]);
  }
  return found;
}

function findUnreachableReads({ composeSource, suites }) {
  const compose = parse(composeSource);
  const problems = [];
  for (const suite of suites) {
    const service = compose.services?.[suite.service];
    if (!service) {
      problems.push(`compose service '${suite.service}' does not exist`);
      continue;
    }
    const provided = new Set([
      ...providedByService(service),
      ...providedByDockerfile(suite.dockerfileSource ?? ""),
    ]);
    for (const name of [...suite.reads].sort()) {
      if (!provided.has(name)) {
        problems.push(
          `${suite.service} runs a suite that reads ${name}, but neither the compose ` +
            `service's environment nor ${suite.dockerfile} provides it, so the read can ` +
            `only ever see its in-code fallback`,
        );
      }
    }
  }
  return problems;
}

function selfTest() {
  assert.deepEqual(
    [...readsIn('const m = process.env.HIVE_TEST_MODEL ?? "hive-default";')],
    ["HIVE_TEST_MODEL"],
    "detector misses a JS process.env read",
  );
  assert.deepEqual(
    [...readsIn('MODEL = os.getenv("HIVE_TEST_MODEL", "hive-default")')],
    ["HIVE_TEST_MODEL"],
    "detector misses a Python os.getenv read",
  );
  assert.deepEqual(
    [...readsIn('String k = System.getenv("HIVE_API_KEY");')],
    ["HIVE_API_KEY"],
    "detector misses a Java System.getenv read",
  );
  assert.deepEqual(
    [...readsIn("# HIVE_TEST_MODEL is described here but never read")],
    [],
    "detector wrongly treats a mention in a comment as a read",
  );
  assert.deepEqual(
    [...providedByDockerfile("FROM node:24\nENV HIVE_FIXTURES_DIR=/fixtures/golden\n")],
    ["HIVE_FIXTURES_DIR"],
    "detector misses an image ENV",
  );
  assert.deepEqual(
    [...providedByService({ environment: ["HIVE_API_KEY=x", "HIVE_BASE_URL"] })],
    ["HIVE_API_KEY", "HIVE_BASE_URL"],
    "detector misses the compose list form of environment",
  );

  const composeSource = `
services:
  sdk-tests-js:
    environment:
      HIVE_BASE_URL: http://edge-api:8080/v1
`;
  assert.equal(
    findUnreachableReads({
      composeSource,
      suites: [
        {
          service: "sdk-tests-js",
          dockerfile: "Dockerfile.x",
          dockerfileSource: "FROM node:24\n",
          reads: new Set(["HIVE_BASE_URL", "HIVE_TEST_MODEL"]),
        },
      ],
    }).length,
    1,
    "detector misses a suite read that no service or image provides",
  );
  assert.deepEqual(
    findUnreachableReads({
      composeSource,
      suites: [
        {
          service: "sdk-tests-js",
          dockerfile: "Dockerfile.x",
          dockerfileSource: "FROM node:24\nENV HIVE_TEST_MODEL=hive-default\n",
          reads: new Set(["HIVE_BASE_URL", "HIVE_TEST_MODEL"]),
        },
      ],
    }),
    [],
    "detector wrongly flags a read the image's own ENV satisfies",
  );
  assert.equal(
    findUnreachableReads({
      composeSource,
      suites: [
        {
          service: "sdk-tests-absent",
          dockerfile: "Dockerfile.x",
          dockerfileSource: "",
          reads: new Set(),
        },
      ],
    }).length,
    1,
    "detector misses a compose service that does not exist",
  );
}

function collectReads(dirs) {
  const found = new Set();
  // git ls-files rather than a directory walk: it skips node_modules, build
  // output and anything else ignored, without this scanner having to know what
  // those are.
  const listed = execFileSync("git", ["ls-files", ...dirs], { encoding: "utf8" })
    .split("\n")
    .filter(Boolean);
  for (const file of listed) {
    if (!/\.(ts|js|mjs|py|java)$/.test(file)) continue;
    for (const name of readsIn(readFileSync(file, "utf8"))) found.add(name);
  }
  return found;
}

selfTest();
if (process.argv.includes("--self-test")) {
  console.log("lint-sdk-test-env-propagation: SELF-TEST PASS");
  process.exit(0);
}

const suites = SUITES.map((suite) => ({
  ...suite,
  dockerfileSource: readFileSync(suite.dockerfile, "utf8"),
  reads: collectReads(suite.sources),
}));

const problems = findUnreachableReads({
  composeSource: readFileSync(COMPOSE_PATH, "utf8"),
  suites,
});

if (problems.length > 0) {
  console.error("SDK test environment knob that cannot reach the container:");
  for (const problem of problems) console.error(`  - ${problem}`);
  console.error(
    "\n  Add the variable to the service's environment: block in " +
      `${COMPOSE_PATH} (pass it through, e.g. NAME: \${NAME:-default}), or set it as an\n` +
      "  ENV in the image. A caller that sets a knob nothing forwards gets a green run\n" +
      "  and no effect, which is issue #1088.",
  );
  process.exit(1);
}
console.log(
  `lint-sdk-test-env-propagation: PASS (${suites
    .map((s) => `${s.service}: ${[...s.reads].sort().join(", ") || "no HIVE_* reads"}`)
    .join("; ")})`,
);
