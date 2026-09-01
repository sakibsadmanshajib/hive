// tools/lint-compose-required-vars.test.mjs
//
// Negative fixtures for lint-compose-required-vars.mjs, asked for by a
// CodeRabbit pass on PR #1642 that found the real hole: the shell-assignment
// arm of `assigns` matched `# WEBUI_SECRET_KEY=value` inside a COMMENT,
// because `#` is a non-identifier character. A workflow could then carry the
// variable name in prose, supply compose with nothing, and still be reported
// as compliant, which is the precise failure shape this lint exists to catch.
//
// The lint reads fixed relative paths, so each case runs it as a subprocess
// with cwd pointed at a throwaway tree. Same boundary
// lint-go-db-test-wiring.test.mjs uses, and for the same reason: the script's
// body is the measurement, so testing it any closer would mean rewriting it
// to be pure for the test's benefit.
//
// Run: node tools/lint-compose-required-vars.test.mjs

import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { mkdtempSync, mkdirSync, writeFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, dirname } from "node:path";
import { fileURLToPath } from "node:url";

const HERE = dirname(fileURLToPath(import.meta.url));
const LINT = join(HERE, "lint-compose-required-vars.mjs");

const COMPOSE_REQUIRED = `services:
  open-webui:
    environment:
      FIXTURE_SECRET: \${FIXTURE_SECRET:?FIXTURE_SECRET must be set}
      HARMLESS: \${HARMLESS:-a default can never fail interpolation}
`;

// The reference workflow. The lint only guards variables THIS file passes as
// workflow env, since everything else is expected from the box's .env.
const DEPLOY = `name: deploy-demo-box
env:
  FIXTURE_SECRET: \${{ secrets.FIXTURE_SECRET }}
jobs:
  deploy:
    steps:
      - run: docker compose -f docker-compose.enterprise.yml up -d
`;

const consumer = (envLines) => `name: consumer
jobs:
  check:
    env:
${envLines}
    steps:
      - run: docker compose -f docker-compose.enterprise.yml ps -q
`;

function run(files) {
  const root = mkdtempSync(join(tmpdir(), "lint-compose-required-vars-"));
  try {
    for (const [rel, content] of Object.entries(files)) {
      const full = join(root, rel);
      mkdirSync(dirname(full), { recursive: true });
      writeFileSync(full, content);
    }
    const result = spawnSync(process.execPath, [LINT], {
      cwd: root,
      encoding: "utf8",
    });
    return { status: result.status, out: result.stdout + result.stderr };
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
}

// 1. A commented-out example supplies compose with nothing, so it must not
//    satisfy the requirement. This is the case that regressed.
const commented = run({
  "deploy/docker/docker-compose.enterprise.yml": COMPOSE_REQUIRED,
  ".github/workflows/deploy-demo-box.yml": DEPLOY,
  ".github/workflows/consumer.yml": consumer(
    "      # FIXTURE_SECRET=value  (an example, not an assignment)\n" +
      "      OTHER: set\n",
  ),
});
assert.equal(
  commented.status,
  1,
  `a commented example must not count as supplying the variable, got exit ` +
    `${commented.status}:\n${commented.out}`,
);
assert.match(commented.out, /consumer\.yml.*FIXTURE_SECRET/s);

// 2. A real env entry does satisfy it.
const assigned = run({
  "deploy/docker/docker-compose.enterprise.yml": COMPOSE_REQUIRED,
  ".github/workflows/deploy-demo-box.yml": DEPLOY,
  ".github/workflows/consumer.yml": consumer(
    "      FIXTURE_SECRET: ${{ secrets.FIXTURE_SECRET }}\n",
  ),
});
assert.equal(
  assigned.status,
  0,
  `an env entry must satisfy the requirement, got exit ` +
    `${assigned.status}:\n${assigned.out}`,
);

// 3. A workflow that never runs compose is not this lint's business, even
//    though it names the file.
const unrelated = run({
  "deploy/docker/docker-compose.enterprise.yml": COMPOSE_REQUIRED,
  ".github/workflows/deploy-demo-box.yml": DEPLOY,
  ".github/workflows/consumer.yml":
    "name: docs\njobs:\n  d:\n    steps:\n      - run: echo -f docker-compose.enterprise.yml\n",
});
assert.equal(unrelated.status, 0, unrelated.out);

// 4. Nothing required anywhere is a failure, not a pass. A lint reporting
//    green over an empty set is the shape this repository keeps paying for.
const vacuous = run({
  "deploy/docker/docker-compose.enterprise.yml":
    "services:\n  open-webui:\n    environment:\n      HARMLESS: ${HARMLESS:-fine}\n",
  ".github/workflows/deploy-demo-box.yml": DEPLOY,
});
assert.equal(vacuous.status, 1, vacuous.out);
assert.match(vacuous.out, /asserts\s+nothing/);

// 5. A variable the deploy READS OUT of the box's .env inside a run block is
//    not one it passes as workflow env, so it is not this lint's business and
//    consumers must not be asked for it. deploy-demo-box.yml really does this
//    for CONTROL_PLANE_INTERNAL_TOKEN and LITELLM_MASTER_KEY, and a looser
//    predicate here counted both, which would have demanded values that the
//    --env-file already supplies.
const readFromEnvFile = run({
  "deploy/docker/docker-compose.enterprise.yml": COMPOSE_REQUIRED,
  ".github/workflows/deploy-demo-box.yml": `name: deploy-demo-box
jobs:
  deploy:
    steps:
      - run: |
          FIXTURE_SECRET=$(grep -E '^FIXTURE_SECRET=' .env | cut -d= -f2-)
          docker compose -f docker-compose.enterprise.yml up -d
`,
  ".github/workflows/consumer.yml": consumer("      OTHER: set\n"),
});
assert.equal(readFromEnvFile.status, 1, readFromEnvFile.out);
assert.match(readFromEnvFile.out, /asserts\s+nothing/);

console.log("lint-compose-required-vars self-check: 5 cases OK");
