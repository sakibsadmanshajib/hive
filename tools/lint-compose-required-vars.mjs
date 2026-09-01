#!/usr/bin/env node
// Compose required-variable propagation guard (issue #1610).
//
// deploy/docker/docker-compose.enterprise.yml declares WEBUI_SECRET_KEY in
// compose's REQUIRED form, ${VAR:?message} (issue #1602). That form is right:
// it stops a deployment starting with the public dev literal committed in
// docker-compose.yml. What it also does, and what nobody accounted for, is
// fail every OTHER compose subcommand for the same reason. Compose
// interpolates the whole merged config before it decides what a subcommand
// needs, so a caller that merges that file without supplying the variable
// cannot even run `ps`.
//
// post-deploy-verify.yml did exactly that from 2026-08-31, the day after the
// required form landed. Its image-freshness step got no container ids back and
// reported "no running containers matched these compose flags, so nothing was
// compared" on every deploy: a message about containers, for a cause that was
// a missing variable, on the one workflow whose job is to notice when the box
// stops matching the repository.
//
// ── What is asserted, and why it is narrower than it first looks ────────────
//
// docker-compose.enterprise.yml makes nine variables required today, and
// deploy-demo-box.yml sets exactly one of them. The other eight are not a
// latent bug: every workflow here runs compose with `--env-file ../../.env`,
// and the box's own untracked .env carries them. That file cannot be read from
// a checkout, so a lint demanding all nine would be eight parts noise.
//
// The one that broke is the one deploy-demo-box.yml passes as workflow ENV
// instead, deliberately, so the versioned value wins over anything the box's
// .env happens to hold (issue #1602). That choice is exactly what makes the
// variable invisible to every other caller: it is not in .env, so
// `--env-file` supplies nothing for it.
//
// So the invariant is: a required compose variable that the deploy passes as
// workflow env must be passed by every other workflow that merges the same
// compose file. The list is derived from the deploy workflow rather than
// hard-coded, so the next secret promoted to workflow env is covered without
// touching this file.
//
// ponytail: matching is per FILE, not per step. This cannot tell that the
// variable sits on the step that actually shells out to compose, only that the
// workflow assigns it somewhere. That is enough to have caught the real
// defect, where post-deploy-verify.yml named it nowhere at all; tighten to
// per-step if a misplaced env block ever becomes the thing that bites.

import { readFileSync, readdirSync } from "node:fs";
import { join } from "node:path";

const COMPOSE_DIR = "deploy/docker";
const WORKFLOW_DIR = ".github/workflows";
const DEPLOY_WORKFLOW = "deploy-demo-box.yml";

// `${NAME:?message}`. The `:-default` form is deliberately NOT matched: it
// supplies its own value and can never fail interpolation.
const REQUIRED_VAR = /\$\{([A-Za-z_][A-Za-z0-9_]*):\?/g;

// A YAML mapping key: `NAME: value`, which in these files means an `env:`
// entry. Anchored to the start of a line with only whitespace in front, so a
// mention in prose or a commented-out example does not count.
//
// This is the predicate used on the DEPLOY workflow, and it is deliberately
// the strict one. What makes a variable this lint's business is that the
// deploy passes it as workflow env; a `NAME=$(grep ... .env)` line inside a
// `run:` block means the opposite, that the value is being read OUT of the
// box's .env, which is the case this lint must stay out of.
const passedAsWorkflowEnv = (text, name) =>
  new RegExp(`^\\s*${name}\\s*:`, "m").test(text);

// The same, or a shell assignment at the start of a line (`NAME=value`,
// optionally exported), which does supply compose when it sits in the same
// `run:` block. Used on the CONSUMER workflows, where either shape is a real
// answer.
//
// Both arms are line-anchored. `# WEBUI_SECRET_KEY=value` in a comment
// supplies compose with nothing, and counting it would let this lint report
// success over a workflow that still cannot run: that exact hole was found by
// a CodeRabbit pass on PR #1642 and is pinned by case 1 of the self-check.
const assigns = (text, name) =>
  passedAsWorkflowEnv(text, name) ||
  new RegExp(`^\\s*(?:export\\s+)?${name}=`, "m").test(text);

const errors = [];
const workflowText = new Map();
for (const file of readdirSync(WORKFLOW_DIR).sort()) {
  if (!/\.ya?ml$/.test(file)) continue;
  workflowText.set(file, readFileSync(join(WORKFLOW_DIR, file), "utf8"));
}

const deployText = workflowText.get(DEPLOY_WORKFLOW);
if (!deployText) {
  console.error(
    `${WORKFLOW_DIR}/${DEPLOY_WORKFLOW} is missing, and it is the reference ` +
      `this lint derives its variable list from. If the deploy workflow was ` +
      `renamed, point DEPLOY_WORKFLOW at the new name.`,
  );
  process.exit(1);
}

/** compose file name -> required variables the deploy passes as workflow env */
const guarded = new Map();
for (const file of readdirSync(COMPOSE_DIR).sort()) {
  if (!/^docker-compose.*\.ya?ml$/.test(file)) continue;
  const text = readFileSync(join(COMPOSE_DIR, file), "utf8");
  const names = new Set(
    [...text.matchAll(REQUIRED_VAR)]
      .map((match) => match[1])
      .filter((name) => passedAsWorkflowEnv(deployText, name)),
  );
  if (names.size > 0) guarded.set(file, names);
}

if (guarded.size === 0) {
  // Zero of zero is not a pass. With nothing in the intersection every
  // assertion below is vacuous, and this lint would report green forever while
  // checking nothing, which is the exact failure shape it exists to prevent.
  console.error(
    `No compose file under ${COMPOSE_DIR} declares a \${VAR:?...} required ` +
      `variable that ${DEPLOY_WORKFLOW} also sets, so this lint asserts ` +
      `nothing. If that intersection was emptied deliberately, delete this ` +
      `lint and its CI step rather than leaving it green over an empty set.`,
  );
  process.exit(1);
}

for (const [workflow, text] of workflowText) {
  if (workflow === DEPLOY_WORKFLOW) continue;
  if (!text.includes("docker compose")) continue;

  for (const [composeFile, names] of guarded) {
    if (!text.includes(`-f ${composeFile}`)) continue;
    for (const name of names) {
      if (assigns(text, name)) continue;
      errors.push(
        `${WORKFLOW_DIR}/${workflow} runs docker compose with ` +
          `-f ${composeFile}, which declares ${name} as a required variable, ` +
          `but the workflow never sets it. ${DEPLOY_WORKFLOW} passes ${name} ` +
          `as workflow env rather than through the box's .env, so ` +
          `--env-file supplies nothing for it and compose fails ` +
          `interpolation before it runs anything: even a read-only 'ps' ` +
          `returns no containers, and the step fails naming containers ` +
          `instead of the variable. Add ` +
          `${name}: \${{ secrets.${name} }} to the env of the job or step ` +
          `that runs compose.`,
      );
    }
  }
}

if (errors.length > 0) {
  console.error("compose required-variable propagation check failed:\n");
  for (const error of errors) console.error(`  - ${error}\n`);
  process.exit(1);
}

const summary = [...guarded]
  .map(([file, names]) => `${file} (${[...names].sort().join(", ")})`)
  .join("; ");
console.log(`compose required-variable propagation OK: ${summary}`);
