#!/usr/bin/env node
// Lists files changed between two git refs that deploy-demo-box.yml's own
// `on.push.paths` filter would have triggered on. Built for
// deploy-drift-watchdog.yml (issue #1238): that workflow needs to tell a
// commit range with NO covered changes (a docs-only merge, correctly never
// deploying) apart from one that should have triggered a deploy and did not.
//
// Reuses lint-deploy-paths-filter.mjs's own `pushPaths`/`isCovered` instead
// of re-deriving the glob rules: this is the second caller of "does the
// filter cover this file", and the two must never drift apart, since a
// filter this script judges as covering some path but the CI lint judges
// as NOT covering it (or vice versa) would make the drift watchdog and the
// paths-filter lint disagree about the same commit.
//
// Usage: node .github/ci/list-covered-deploy-changes.mjs <base-sha> <head-sha>
// Prints one covered, changed path per line to stdout. Empty output (exit 0)
// means no path in the diff is covered by the filter -- not an error, the
// caller decides what that means.

import { execFileSync } from 'node:child_process';
import { readFileSync } from 'node:fs';
import { parse } from 'yaml';
import { WORKFLOW_FILE, pushPaths, isCovered } from './lint-deploy-paths-filter.mjs';

const [, , baseSha, headSha] = process.argv;
if (!baseSha || !headSha) {
  console.error('usage: node .github/ci/list-covered-deploy-changes.mjs <base-sha> <head-sha>');
  process.exit(2);
}

const workflow = parse(readFileSync(WORKFLOW_FILE, 'utf8'));
const filters = pushPaths(workflow);
if (filters.length === 0) {
  console.error(`${WORKFLOW_FILE}: could not find on.push.paths -- refusing to guess.`);
  process.exit(2);
}

// --name-only over `git diff`, not `git log`: this asks one question, "what
// changed between these two trees", the same question deploy-demo-box.yml's
// own push-paths trigger asks about a push. `-z` avoids surprises from an
// exotic (quoted/escaped) filename since this repo has none today; NUL
// splitting costs nothing and is correct either way.
let diffOutput;
try {
  diffOutput = execFileSync('git', ['diff', '--name-only', '-z', `${baseSha}..${headSha}`], {
    encoding: 'utf8',
  });
} catch (err) {
  console.error(`git diff ${baseSha}..${headSha} failed: ${err.message}`);
  process.exit(2);
}

const changed = diffOutput.split('\0').filter(Boolean);
const covered = changed.filter((path) => isCovered(path, filters));
for (const path of covered) console.log(path);
