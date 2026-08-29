#!/usr/bin/env node
// Finds the newest commit between two refs whose own diff touches a path
// deploy-demo-box.yml's on.push.paths filter covers. deploy-drift-watchdog.yml
// uses THIS commit's date for its grace-window freshness check, not main's
// tip commit's date: reading the tip's date lets an unrelated, uncovered
// commit that lands shortly after a genuinely stale covered commit reset the
// apparent freshness and mask real drift. PR #1249 and PR #1246 merging 8
// seconds apart on 2026-08-28 is exactly the shape that exposes this.
//
// Reuses lint-deploy-paths-filter.mjs's own pushPaths/isCovered, same as
// list-covered-deploy-changes.mjs, so all three scripts agree about what
// "covered" means for the same commit.
//
// Usage: node .github/ci/newest-covered-commit.mjs <base-sha> <head-sha>
// Prints the newest covered commit's full SHA to stdout on success (exit 0).
// Exit 1 if no commit in range touches a covered path -- callers should only
// run this after confirming list-covered-deploy-changes.mjs found at least
// one covered path in the same range. Exit 2 on usage or git/parse errors.

import { execFileSync } from 'node:child_process';
import { readFileSync } from 'node:fs';
import { parse } from 'yaml';
import { WORKFLOW_FILE, pushPaths, isCovered } from './lint-deploy-paths-filter.mjs';

const [, , baseSha, headSha] = process.argv;
if (!baseSha || !headSha) {
  console.error('usage: node .github/ci/newest-covered-commit.mjs <base-sha> <head-sha>');
  process.exit(2);
}

// Defense in depth: deploy-drift-watchdog.yml already validates both values
// against this same shape before they ever reach this script (its "Resolve
// base and head commits" step is the only real caller today), but this
// script does not get to assume that stays true forever. Without this, a
// value starting with `-` would be handed to `git log` as a single argv
// element with no `--` separator, so git would parse it as an option
// instead of a revision.
const SHA_RE = /^[0-9a-f]{7,40}$/;
for (const [name, value] of [
  ['base-sha', baseSha],
  ['head-sha', headSha],
]) {
  if (!SHA_RE.test(value)) {
    console.error(`${name} '${value}' is not a valid git SHA (7-40 lowercase hex chars)`);
    process.exit(2);
  }
}

const workflow = parse(readFileSync(WORKFLOW_FILE, 'utf8'));
const filters = pushPaths(workflow);
if (filters.length === 0) {
  console.error(`${WORKFLOW_FILE}: could not find on.push.paths -- refusing to guess.`);
  process.exit(2);
}

// Newest-first (git log's default order): the first commit here whose own
// diff touches a covered path is the newest covered commit in range.
let commits;
try {
  commits = execFileSync('git', ['log', '--format=%H', `${baseSha}..${headSha}`], {
    encoding: 'utf8',
  })
    .split('\n')
    .filter(Boolean);
} catch (err) {
  console.error(`git log ${baseSha}..${headSha} failed: ${err.message}`);
  process.exit(2);
}

// `-m`: without it, `git diff-tree` prints nothing at all for a merge
// commit (ambiguous against which parent), so a merge commit that itself
// introduced a covered change would be silently skipped by this loop.
// `--root`: without it, the same "prints nothing" happens for a commit with
// no parent at all (a repository's very first commit). Neither should ever
// actually appear between two successive baselines on this repo's real main
// history (squash-only merge policy; base_sha is itself always a later
// commit than any repo's root), but the consequence if one ever does is
// worse than a plain miss: the caller (deploy-drift-watchdog.yml) hard-fails
// on this script's exit 1 under `set -euo pipefail`, its "Verdict" step has
// no `if: always()` guard so a failed prior step skips it too, the alert
// output never gets set, and "File or update the tracking issue" (gated on
// that output) never fires either. That is the exact silent-failure shape
// this watchdog exists to catch, reproduced inside the watchdog itself, so
// both flags are worth carrying even though today's real callers should
// never exercise either path.
for (const sha of commits) {
  let changed;
  try {
    changed = execFileSync('git', ['diff-tree', '-m', '--root', '--no-commit-id', '--name-only', '-r', '-z', sha], {
      encoding: 'utf8',
    })
      .split('\0')
      .filter(Boolean);
  } catch (err) {
    console.error(`git diff-tree ${sha} failed: ${err.message}`);
    process.exit(2);
  }
  if (changed.some((path) => isCovered(path, filters))) {
    console.log(sha);
    process.exit(0);
  }
}

console.error(`no commit between ${baseSha}..${headSha} touches a covered path`);
process.exit(1);
