#!/usr/bin/env node
// Merge-gate integrity guard (issue #553).
//
// The merge gate on `main` is a list of required status check names in
// .github/branch-protection-main.json. GitHub matches those names against
// whatever check runs arrive, and it has no notion of which workflow was
// supposed to produce them. Two consequences, both of which have already
// bitten this repository:
//
//   1. If two jobs publish the same check name, either one can satisfy the
//      gate. ci-noop.yml used to publish `echo`-only jobs whose names matched
//      the real ci.yml jobs exactly, so a pull request touching both code and
//      docs fired both workflows and the echo could report success on every
//      required context in seconds.
//
//   2. If a job is renamed without updating branch protection, the required
//      context is never reported at all and every pull request blocks forever
//      on a check that cannot arrive.
//
// This script fails the build on either condition. It is intentionally scoped
// to workflows that can report on a pull request, because those are the only
// ones that feed the merge gate; deploy and release workflows are free to
// reuse job names.
//
// Run: node .github/ci/lint-workflow-check-names.mjs

import { readFileSync, readdirSync } from 'node:fs';
import { join } from 'node:path';
import { parse } from 'yaml';

const WORKFLOW_DIR = '.github/workflows';
const PROTECTION_FILE = '.github/branch-protection-main.json';
const PR_EVENTS = ['pull_request', 'pull_request_target', 'merge_group'];

const errors = [];

// `on:` parses to the boolean true under YAML 1.1, so read the raw key set
// rather than trusting the parsed key name.
function triggers(workflow) {
  const on = workflow?.on ?? workflow?.[true] ?? workflow?.True;
  if (typeof on === 'string') return [on];
  if (Array.isArray(on)) return on;
  if (on && typeof on === 'object') return Object.keys(on);
  return [];
}

// The check name GitHub publishes for a job is its `name:` when present and
// its job id otherwise.
function checkNames(workflow) {
  return Object.entries(workflow?.jobs ?? {}).map(([id, job]) => ({
    id,
    name: typeof job?.name === 'string' ? job.name : id,
  }));
}

const prWorkflows = readdirSync(WORKFLOW_DIR)
  .filter((f) => f.endsWith('.yml') || f.endsWith('.yaml'))
  .map((f) => ({ file: join(WORKFLOW_DIR, f), doc: parse(readFileSync(join(WORKFLOW_DIR, f), 'utf8')) }))
  .filter(({ doc }) => triggers(doc).some((t) => PR_EVENTS.includes(t)));

if (prWorkflows.length === 0) {
  errors.push(`no pull-request workflows found under ${WORKFLOW_DIR}; the guard would pass vacuously`);
}

// ---- Check 1: one producer per check name ------------------------------
const producers = new Map();
for (const { file, doc } of prWorkflows) {
  for (const { id, name } of checkNames(doc)) {
    if (!producers.has(name)) producers.set(name, []);
    producers.get(name).push(`${file}#${id}`);
  }
}

for (const [name, sources] of producers) {
  if (sources.length > 1) {
    errors.push(
      `check name "${name}" is published by ${sources.length} jobs: ${sources.join(', ')}. ` +
        'Exactly one job may publish a given check name, otherwise either job can satisfy the merge gate.',
    );
  }
}

// ---- Check 2: every required context has exactly one producer ----------
const required = JSON.parse(readFileSync(PROTECTION_FILE, 'utf8'))?.required_status_checks?.contexts ?? [];
if (required.length === 0) {
  errors.push(`${PROTECTION_FILE} lists no required contexts; nothing guards main`);
}

// A matrix-templated name such as "Go tests (${{ matrix.module }})" expands at
// run time, so compare against a pattern rather than the literal template.
function matches(template, context) {
  const pattern = template
    .split(/\$\{\{[^}]*\}\}/)
    .map((literal) => literal.replace(/[.*+?^${}()|[\]\\]/g, '\\$&'))
    .join('.+');
  return new RegExp(`^${pattern}$`).test(context);
}

for (const context of required) {
  const found = [...producers.entries()]
    .filter(([name]) => matches(name, context))
    .flatMap(([, sources]) => sources);
  if (found.length === 0) {
    errors.push(
      `required check "${context}" from ${PROTECTION_FILE} is not published by any pull-request job. ` +
        'Update the workflow job name and branch protection in the same change.',
    );
  }
}

if (errors.length > 0) {
  console.error('Merge-gate integrity check FAILED:');
  for (const e of errors) console.error(`  - ${e}`);
  process.exit(1);
}

console.log(
  `Merge-gate integrity OK: ${producers.size} check names across ${prWorkflows.length} pull-request workflows, ` +
    `${required.length} required contexts each with exactly one producer.`,
);
