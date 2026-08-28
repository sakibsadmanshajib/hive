// .github/ci/newest-covered-commit.test.mjs
// Regression guard for the deploy-drift-watchdog grace-window defect: PR
// #1249 and PR #1246 merged 8 seconds apart, illustrating that an unrelated,
// uncovered commit landing right after a genuinely stale covered commit must
// not reset the apparent freshness. Before this script existed, the workflow
// read main's tip commit date for the freshness check, so a fresh unrelated
// commit could mask an old, un-deployed covered change. This builds a
// throwaway git fixture that reproduces exactly that shape: an old covered
// commit followed by a recent uncovered commit, then proves the script picks
// the OLD covered commit, not the recent HEAD.
//
// Run: node .github/ci/newest-covered-commit.test.mjs

import assert from 'node:assert/strict';
import { execFileSync, spawnSync } from 'node:child_process';
import { mkdtempSync, mkdirSync, writeFileSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join, dirname } from 'node:path';
import { fileURLToPath } from 'node:url';

const HERE = dirname(fileURLToPath(import.meta.url));
const SCRIPT = join(HERE, 'newest-covered-commit.mjs');
const REPO_ROOT = join(HERE, '..', '..');

function git(cwd, args, env = {}) {
  return execFileSync('git', args, {
    cwd,
    encoding: 'utf8',
    env: { ...process.env, ...env },
  }).trim();
}

function commit(cwd, relPath, content, isoDate) {
  const full = join(cwd, relPath);
  mkdirSync(dirname(full), { recursive: true });
  writeFileSync(full, content);
  git(cwd, ['add', relPath]);
  git(cwd, ['commit', '-q', '-m', `test: ${relPath}`], {
    GIT_AUTHOR_DATE: isoDate,
    GIT_COMMITTER_DATE: isoDate,
  });
  return git(cwd, ['rev-parse', 'HEAD']);
}

// Uses REAL deploy-demo-box.yml paths (apps/edge-api/** is covered, a
// top-level doc is not) rather than a fabricated filter, so this test proves
// the script against the actual filter it will run against in CI.
const root = mkdtempSync(join(tmpdir(), 'newest-covered-commit-fixture-'));
try {
  git(root, ['init', '-q']);
  git(root, ['config', 'user.email', 'test@example.com']);
  git(root, ['config', 'user.name', 'Test']);

  const baseSha = commit(root, 'README.md', 'base\n', '2026-08-01T00:00:00Z');

  // Covered, old: apps/edge-api/** is in deploy-demo-box.yml's real paths
  // filter. Dated an hour before the head commit so it sits outside any
  // 15-minute grace window relative to head's own timestamp.
  const oldCoveredSha = commit(root, 'apps/edge-api/main.go', 'package main\n', '2026-08-28T10:00:00Z');

  // Uncovered, recent: a top-level doc is not in the filter at all. Dated
  // two minutes after the covered commit -- this is the commit the pre-fix
  // workflow read the date from (main's tip), and reading it wrongly makes
  // a genuinely stale covered change look fresh.
  const headSha = commit(root, 'NOTES.md', 'unrelated\n', '2026-08-28T10:58:00Z');

  const result = spawnSync('node', [SCRIPT, baseSha, headSha], {
    cwd: REPO_ROOT, // needs the real deploy-demo-box.yml to read the filter
    encoding: 'utf8',
    env: { ...process.env, GIT_DIR: join(root, '.git'), GIT_WORK_TREE: root },
  });

  assert.equal(result.status, 0, `expected exit 0, got ${result.status}. stderr:\n${result.stderr}`);
  const newestCoveredSha = result.stdout.trim();

  assert.equal(
    newestCoveredSha,
    oldCoveredSha,
    `expected the OLD covered commit (${oldCoveredSha}), got ${newestCoveredSha} ` +
      `(head was ${headSha}). This is the defect: the freshness check must not ` +
      `use main's tip when a newer, uncovered commit landed after a stale covered one.`,
  );
  assert.notEqual(
    newestCoveredSha,
    headSha,
    'the script must not fall back to head_sha -- that is exactly the bug being fixed',
  );

  // Prove the numbers: the OLD covered commit is >= 15 minutes before head's
  // own timestamp, so a freshness check keyed on head (the pre-fix behavior)
  // would have read this range as "fresh" and masked the drift, while a
  // check keyed on the OLD covered commit correctly reads it as stale.
  const oldDate = new Date(git(root, ['log', '-1', '--format=%cI', oldCoveredSha]));
  const headDate = new Date(git(root, ['log', '-1', '--format=%cI', headSha]));
  const ageMinutesIfHeadWereUsedAsNow = (headDate.getTime() - oldDate.getTime()) / 60000;
  assert.ok(
    ageMinutesIfHeadWereUsedAsNow >= 15,
    `fixture invariant broken: old covered commit must be >= 15 minutes before head, got ${ageMinutesIfHeadWereUsedAsNow}`,
  );

  console.log('newest-covered-commit.test: PASS');
} finally {
  rmSync(root, { recursive: true, force: true });
}
