#!/usr/bin/env node
// Merge-gate integrity guard (issue #553).
//
// The merge gate on `main` is a list of required status check names in
// .github/branch-protection-main.json. GitHub matches those names against
// whatever check runs arrive, and it has no notion of which workflow was
// supposed to produce them. Consequences, all of which have already bitten
// this repository:
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
//   3. If a workflow publishing a required context carries a trigger `paths:`
//      or `paths-ignore:` filter, GitHub skips the whole workflow on a diff
//      that does not match, no check run is ever created, and the required
//      context stays Pending. That is the original deadlock; path filtering
//      has to happen inside the workflow instead.
//
//   4. If a workflow publishing a required context does not listen for
//      `ready_for_review`, a pull request opened as a draft and then marked
//      ready with no further pushes fires no event that workflow reacts to.
//      No check run is created, so the required contexts are never published
//      and the pull request is unmergeable with nothing visibly failing.
//
// This script fails the build on any of those. It is intentionally scoped to
// workflows that can report on a pull request, because those are the only ones
// that feed the merge gate; deploy and release workflows are free to reuse job
// names and to path-filter their triggers.
//
// Matrix job names are expanded against the real `strategy.matrix` values
// rather than matched as a wildcard pattern. Comparing a template such as
// "Go tests (${{ matrix.module }})" loosely would defeat both checks above: a
// second job named literally "Go tests (edge-api)" would never collide with
// the template string, and dropping a leg from the matrix would leave the
// corresponding required context with a producer that only appears to exist.
//
// Run: node .github/ci/lint-workflow-check-names.mjs

import { readFileSync, readdirSync } from 'node:fs';
import { join } from 'node:path';
import { parse } from 'yaml';

const WORKFLOW_DIR = '.github/workflows';
const PROTECTION_FILE = '.github/branch-protection-main.json';
const PR_EVENTS = ['pull_request', 'pull_request_target', 'merge_group'];
const EXPRESSION = /\$\{\{([^}]*)\}\}/g;

const errors = [];

// `on:` parses to the boolean true under YAML 1.1, so read the raw key set
// rather than trusting the parsed key name.
function onBlock(workflow) {
  return workflow?.on ?? workflow?.[true] ?? workflow?.True;
}

function triggers(workflow) {
  const on = onBlock(workflow);
  if (typeof on === 'string') return [on];
  if (Array.isArray(on)) return on;
  if (on && typeof on === 'object') return Object.keys(on);
  return [];
}

// Trigger-level path filters, as `event.key` strings.
function pathFilters(workflow) {
  const on = onBlock(workflow);
  if (!on || typeof on !== 'object' || Array.isArray(on)) return [];
  return Object.entries(on).flatMap(([event, config]) =>
    config && typeof config === 'object' && !Array.isArray(config)
      ? ['paths', 'paths-ignore'].filter((key) => key in config).map((key) => `${event}.${key}`)
      : [],
  );
}

// The `pull_request` activity types a workflow must react to before it can be
// trusted to publish a required context, and the ones it is missing. GitHub
// defaults to opened, synchronize and reopened when `types:` is omitted, and
// none of those fire when a draft pull request is marked ready for review with
// no further pushes, so `ready_for_review` has to be listed explicitly.
// Workflows that never declare a `pull_request` trigger are out of scope.
const REQUIRED_PR_TYPES = ['opened', 'synchronize', 'reopened', 'ready_for_review'];

function missingPullRequestTypes(workflow) {
  const on = onBlock(workflow);
  if (!on || typeof on !== 'object' || Array.isArray(on) || !('pull_request' in on)) return [];
  const config = on.pull_request;
  const declared =
    config && typeof config === 'object' && !Array.isArray(config) && Array.isArray(config.types)
      ? config.types
      : [];
  return REQUIRED_PR_TYPES.filter((type) => !declared.includes(type));
}

// The concrete matrix legs GitHub will run, so a templated job name can be
// expanded into the exact set of check names it publishes. Handles the two
// shapes in use: bare axes (cartesian product, minus `exclude:`) and an
// `include:`-only matrix, where every entry is a leg of its own.
function matrixLegs(job) {
  const matrix = job?.strategy?.matrix;
  if (!matrix || typeof matrix !== 'object') return [{}];

  const include = Array.isArray(matrix.include) ? matrix.include : [];
  const axes = Object.entries(matrix).filter(([key]) => key !== 'include' && key !== 'exclude');
  if (axes.length === 0) return include.length > 0 ? include.map((leg) => ({ ...leg })) : [{}];

  let legs = axes.reduce(
    (combos, [key, values]) =>
      combos.flatMap((combo) =>
        (Array.isArray(values) ? values : [values]).map((value) => ({ ...combo, [key]: value })),
      ),
    [{}],
  );

  const exclude = Array.isArray(matrix.exclude) ? matrix.exclude : [];
  legs = legs.filter(
    (leg) => !exclude.some((entry) => Object.entries(entry).every(([key, value]) => leg[key] === value)),
  );

  // An `include:` entry extends every compatible leg, or adds a leg of its own
  // when it is compatible with none.
  for (const entry of include) {
    const extend = legs.filter((leg) =>
      Object.entries(entry).every(([key, value]) => !(key in leg) || leg[key] === value),
    );
    if (extend.length > 0) for (const leg of extend) Object.assign(leg, entry);
    else legs.push({ ...entry });
  }

  return legs;
}

// Expand a job `name:` into the concrete check names it can publish. Only
// `matrix.*` is resolvable here; anything else (github.*, env.*, a function
// call) is reported so the guard never claims to have verified a name it
// cannot compute.
function expandName(template, legs) {
  if (!template.includes('${{')) return { names: [template], unresolved: [] };

  const names = new Set();
  const unresolved = new Set();
  for (const leg of legs) {
    names.add(
      template.replace(EXPRESSION, (whole, expression) => {
        const key = expression.trim().replace(/^matrix\./, '');
        if (expression.trim().startsWith('matrix.') && key in leg) return String(leg[key]);
        unresolved.add(whole);
        return whole;
      }),
    );
  }
  return { names: [...names], unresolved: [...unresolved] };
}

// The check name GitHub publishes for a job is its `name:` when present and
// its job id otherwise.
function checkNames(workflow) {
  return Object.entries(workflow?.jobs ?? {}).map(([id, job]) => {
    const template = typeof job?.name === 'string' ? job.name : id;
    const { names, unresolved } = expandName(template, matrixLegs(job));
    return {
      id,
      template,
      names,
      unresolved,
      condition: typeof job?.if === 'string' ? job.if : '',
    };
  });
}

const prWorkflows = readdirSync(WORKFLOW_DIR)
  .filter((f) => f.endsWith('.yml') || f.endsWith('.yaml'))
  .map((f) => ({ file: join(WORKFLOW_DIR, f), doc: parse(readFileSync(join(WORKFLOW_DIR, f), 'utf8')) }))
  .filter(({ doc }) => triggers(doc).some((t) => PR_EVENTS.includes(t)));

if (prWorkflows.length === 0) {
  errors.push(`no pull-request workflows found under ${WORKFLOW_DIR}; the guard would pass vacuously`);
}

const producers = new Map();
const jobs = new Map();
for (const { file, doc } of prWorkflows) {
  for (const job of checkNames(doc)) {
    const source = `${file}#${job.id}`;
    jobs.set(source, { ...job, file });
    for (const name of job.names) {
      if (!producers.has(name)) producers.set(name, []);
      producers.get(name).push(source);
    }
  }
}

// ---- Check 0: every check name must be computable ----------------------
for (const [source, job] of jobs) {
  if (job.unresolved.length > 0) {
    errors.push(
      `job ${source} has name "${job.template}", which contains ${job.unresolved.join(', ')}. ` +
        'Only matrix values can be expanded, so this guard cannot tell which check name the job ' +
        'publishes. Use a literal name, or move the varying part into the matrix.',
    );
  }
}

// ---- Check 1: one producer per check name ------------------------------
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

for (const context of required) {
  const found = producers.get(context) ?? [];

  if (found.length === 0) {
    errors.push(
      `required check "${context}" from ${PROTECTION_FILE} is not published by any pull-request job. ` +
        'Update the workflow job name and branch protection in the same change.',
    );
    continue;
  }

  if (found.length > 1) {
    errors.push(
      `required check "${context}" from ${PROTECTION_FILE} is published by ${found.length} jobs: ` +
        `${found.join(', ')}. Any one of them can satisfy the merge gate, so the slowest real suite ` +
        'can be stood in for by the fastest stub.',
    );
  }

  for (const source of found) {
    const { condition, file } = jobs.get(source) ?? { condition: '', file: '' };

    // ---- Check 3: a required job must always report a conclusion ------
    // A job with a plain job-level `if:` can be skipped, and whether branch
    // protection accepts a `skipped` conclusion for a required context is
    // exactly the thing that cannot be tested before merging. Required jobs
    // therefore carry `if: always()` and gate their individual steps, so the
    // job always concludes success or failure on its own merits.
    if (!/\balways\s*\(\s*\)/.test(condition)) {
      errors.push(
        `job ${source} publishes required check "${context}" but its job-level if is ` +
          `"${condition || '(none)'}". A required job must use \`if: always()\` and gate its steps, ` +
          'otherwise it can be skipped and the merge gate depends on how branch protection treats a skipped check.',
      );
    }

    // ---- Check 4: no trigger path filter on a required workflow -------
    // GitHub skips the entire workflow when a trigger path filter does not
    // match the diff, and a skipped workflow never creates its check runs, so
    // the required context stays Pending and blocks the merge forever. Path
    // filtering for a required workflow has to happen inside the workflow.
    const doc = prWorkflows.find((w) => w.file === file)?.doc;
    const filters = pathFilters(doc);
    if (filters.length > 0) {
      errors.push(
        `${file} publishes required check "${context}" but path-filters its triggers ` +
          `(${filters.join(', ')}). A workflow skipped by a trigger path filter never creates its ` +
          'check runs, so the required context stays Pending. Filter inside the workflow instead.',
      );
    }

    // ---- Check 5: a required workflow must run on ready_for_review ----
    // Failure mode: a pull request opened as a draft and then marked ready
    // for review with no further pushes. None of the default activity types
    // fire on that transition, so the workflow never runs, zero required
    // checks are published, and the pull request blocks forever with nothing
    // visibly red.
    const missingTypes = missingPullRequestTypes(doc);
    if (missingTypes.length > 0) {
      errors.push(
        `${file} publishes required check "${context}" but its pull_request trigger does not react to ` +
          `${missingTypes.join(', ')}. List all of ${REQUIRED_PR_TYPES.join(', ')} under ` +
          '`on.pull_request.types`, otherwise a draft marked ready for review with no further pushes ' +
          'publishes no check runs at all and the merge gate can never be satisfied.',
      );
    }
  }
}

if (errors.length > 0) {
  console.error('Merge-gate integrity check FAILED:');
  for (const e of [...new Set(errors)]) console.error(`  - ${e}`);
  process.exit(1);
}

console.log(
  `Merge-gate integrity OK: ${producers.size} check names across ${prWorkflows.length} pull-request workflows, ` +
    `${required.length} required contexts each published by exactly one always()-running job in a ` +
    'workflow with no trigger path filter that reacts to every pull_request type the merge gate needs.',
);
