// .github/ci/check-offbox-backup-freshness.test.mjs
//
// Regression guard for issue #1491's staleness check, and for its wiring.
//
// The defect was that the off-box half of the demo box backup wrote nothing
// anywhere, so its silence was indistinguishable from its success and a
// six-day gap went unnoticed while every other signal stayed green. The check
// under test is the thing that makes that gap loud, so its own negative
// controls have to be observed rather than reasoned about:
//
//   1. a fresh marker passes;
//   2. a marker older than the threshold FAILS, and the failure names
//      scripts/pull-box-backups.sh so the reader knows what to run;
//   3. an absent marker FAILS, rather than reading as "unknown, assume fine";
//   4. a malformed or future-dated marker FAILS, for the same reason;
//   5. the check is actually wired into a scheduled lane. Without this last
//      assertion, deleting the workflow job would leave every test above
//      passing over a check that nothing runs, which is the same silent
//      absence in a new costume.
//
// Run: node .github/ci/check-offbox-backup-freshness.test.mjs

import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { readFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

const HERE = dirname(fileURLToPath(import.meta.url));
const SCRIPT = join(HERE, 'check-offbox-backup-freshness.mjs');
const REPO_ROOT = join(HERE, '..', '..');
const WATCHDOG = join(REPO_ROOT, '.github', 'workflows', 'deploy-drift-watchdog.yml');

// Fixed "now" so no case depends on the wall clock: 2026-08-29T22:00:00Z, the
// evening the six-day gap was found, which is why the literal dates below read
// as they do.
const NOW_EPOCH = 1_788_040_800;
const iso = (epochSeconds) => new Date(epochSeconds * 1000).toISOString().replace(/\.\d{3}Z$/, 'Z');
const hoursAgo = (h) => iso(NOW_EPOCH - h * 3600);

function run(marker, extraEnv = {}) {
  // env is built explicitly rather than spread from process.env, so a
  // LAST_OFFBOX_BACKUP_PULL that happens to be exported in the caller's shell
  // cannot leak in and turn the absent-marker case green.
  const env = { PATH: process.env.PATH, OFFBOX_PULL_NOW_EPOCH: String(NOW_EPOCH), ...extraEnv };
  if (marker !== undefined) env.LAST_OFFBOX_BACKUP_PULL = marker;
  return spawnSync('node', [SCRIPT], { encoding: 'utf8', env });
}

const failures = [];
function check(name, fn) {
  try {
    fn();
    console.log(`ok   ${name}`);
  } catch (err) {
    failures.push(name);
    console.error(`FAIL ${name}: ${err.message}`);
  }
}

check('a marker from one hour ago passes', () => {
  const r = run(`pulled_at=${hoursAgo(1)} newest_day=2026-08-29`);
  assert.equal(r.status, 0, `expected exit 0, got ${r.status}. stderr: ${r.stderr}`);
  assert.match(r.stdout, /^OK:/m);
});

check('a marker from 71 hours ago still passes (a weekend must not cry wolf)', () => {
  const r = run(`pulled_at=${hoursAgo(71)} newest_day=2026-08-26`);
  assert.equal(r.status, 0, `expected exit 0, got ${r.status}. stderr: ${r.stderr}`);
});

check('a fresh pull over a box that stopped publishing FAILS', () => {
  // The fail-open with no attacker and no malice: the box stops producing new
  // daily sets, somebody keeps running the pull, every run transfers nothing,
  // verifies the days already there, and refreshes pulled_at. Checking only
  // pulled_at would report OK over a copy that is weeks behind.
  const r = run(`pulled_at=${hoursAgo(1)} newest_day=2026-08-10`);
  assert.equal(r.status, 1, `expected exit 1, got ${r.status}. stdout: ${r.stdout}`);
  assert.match(r.stderr, /newest day is 2026-08-10/);
  assert.match(r.stderr, /scripts\/pull-box-backups\.sh/);
});

check('a marker with no newest_day FAILS', () => {
  const r = run(`pulled_at=${hoursAgo(1)}`);
  assert.equal(r.status, 1, `expected exit 1, got ${r.status}. stdout: ${r.stdout}`);
  assert.match(r.stderr, /no usable newest_day/);
});

check('a malformed newest_day FAILS', () => {
  const r = run(`pulled_at=${hoursAgo(1)} newest_day=last-tuesday`);
  assert.equal(r.status, 1, `expected exit 1, got ${r.status}. stdout: ${r.stdout}`);
  assert.match(r.stderr, /no usable newest_day/);
});

check('a marker older than the 72 hour threshold FAILS and names the fix', () => {
  const r = run(`pulled_at=${hoursAgo(80)} newest_day=2026-08-26`);
  assert.equal(r.status, 1, `expected exit 1, got ${r.status}. stdout: ${r.stdout}`);
  assert.match(r.stderr, /STALE:/);
  assert.match(r.stderr, /scripts\/pull-box-backups\.sh/);
});

check('the six day gap that actually happened FAILS', () => {
  const r = run(`pulled_at=${hoursAgo(6 * 24)} newest_day=2026-08-23`);
  assert.equal(r.status, 1, `expected exit 1, got ${r.status}. stdout: ${r.stdout}`);
  assert.match(r.stderr, /scripts\/pull-box-backups\.sh/);
});

check('an absent marker FAILS rather than reading as unknown', () => {
  const r = run(undefined);
  assert.equal(r.status, 1, `expected exit 1, got ${r.status}. stdout: ${r.stdout}`);
  assert.match(r.stderr, /has ever been recorded/);
  assert.match(r.stderr, /scripts\/pull-box-backups\.sh/);
});

check('an empty marker FAILS', () => {
  const r = run('   ');
  assert.equal(r.status, 1, `expected exit 1, got ${r.status}. stdout: ${r.stdout}`);
  assert.match(r.stderr, /has ever been recorded/);
});

check('a marker with no pulled_at FAILS', () => {
  const r = run('newest_day=2026-08-29');
  assert.equal(r.status, 1, `expected exit 1, got ${r.status}. stdout: ${r.stdout}`);
  assert.match(r.stderr, /does not carry a pulled_at/);
});

check('a prose pulled_at FAILS', () => {
  const r = run('pulled_at=yesterday-ish newest_day=2026-08-29');
  assert.equal(r.status, 1, `expected exit 1, got ${r.status}. stdout: ${r.stdout}`);
  assert.match(r.stderr, /STALE:/);
  assert.match(r.stderr, /scripts\/pull-box-backups\.sh/);
});

check('a Z-suffixed but impossible pulled_at FAILS', () => {
  // Carries the UTC designator, so it gets past that guard and has to be
  // caught by Date.parse returning NaN.
  const r = run('pulled_at=2026-99-99T99:99:99Z newest_day=2026-08-29');
  assert.equal(r.status, 1, `expected exit 1, got ${r.status}. stdout: ${r.stdout}`);
  assert.match(r.stderr, /not a parseable timestamp/);
});

check('a future-dated marker FAILS instead of reading as fresh', () => {
  const r = run(`pulled_at=${hoursAgo(-48)} newest_day=2026-09-01`);
  assert.equal(r.status, 1, `expected exit 1, got ${r.status}. stdout: ${r.stdout}`);
  assert.match(r.stderr, /in the future/);
});

check('the threshold is configurable and enforced at the configured value', () => {
  const marker = `pulled_at=${hoursAgo(5)} newest_day=2026-08-29`;
  assert.equal(run(marker, { OFFBOX_PULL_STALE_AFTER_HOURS: '6' }).status, 0);
  assert.equal(run(marker, { OFFBOX_PULL_STALE_AFTER_HOURS: '4' }).status, 1);
});

check('a nonsense threshold FAILS rather than silently defaulting', () => {
  const r = run(`pulled_at=${hoursAgo(1)}`, { OFFBOX_PULL_STALE_AFTER_HOURS: 'soon' });
  assert.equal(r.status, 1, `expected exit 1, got ${r.status}. stdout: ${r.stdout}`);
  assert.match(r.stderr, /FATAL/);
});

check('a hex or exponential threshold FAILS rather than silently meaning something else', () => {
  // Number('0x10') is 16 and Number('1e3') is 1000, so without a decimal-only
  // guard an operator typo configures a threshold nobody chose.
  const marker = `pulled_at=${hoursAgo(20)} newest_day=2026-08-29`;
  for (const bad of ['0x10', '1e3', '0b1010', ' 72 ']) {
    const r = run(marker, { OFFBOX_PULL_STALE_AFTER_HOURS: bad });
    assert.equal(r.status, 1, `threshold '${bad}' should be rejected, got exit ${r.status}`);
    assert.match(r.stderr, /FATAL/);
  }
});

check('a timestamp with no UTC designator FAILS rather than being read as local time', () => {
  // Date.parse treats an offset-less date-time as LOCAL time, so this marker
  // would mean different things on two runners in different zones.
  const r = run('pulled_at=2026-08-29T22:32:51 newest_day=2026-08-29');
  assert.equal(r.status, 1, `expected exit 1, got ${r.status}. stdout: ${r.stdout}`);
  assert.match(r.stderr, /no UTC designator/);
});

check('an explicit numeric offset is accepted', () => {
  const iso8601WithOffset = new Date((NOW_EPOCH - 3600) * 1000)
    .toISOString()
    .replace(/\.\d{3}Z$/, '+00:00');
  const r = run(`pulled_at=${iso8601WithOffset} newest_day=2026-08-29`);
  assert.equal(r.status, 0, `expected exit 0, got ${r.status}. stderr: ${r.stderr}`);
});

check('two pulled_at values FAIL rather than one being picked silently', () => {
  const r = run(`pulled_at=${hoursAgo(1)} pulled_at=${hoursAgo(200)} newest_day=2026-08-29`);
  assert.equal(r.status, 1, `expected exit 1, got ${r.status}. stdout: ${r.stdout}`);
  assert.match(r.stderr, /2 pulled_at values/);
});

check('deploy-drift-watchdog.yml actually runs this check on its schedule', () => {
  const yml = readFileSync(WATCHDOG, 'utf8');
  assert.match(
    yml,
    /node \.github\/ci\/check-offbox-backup-freshness\.mjs/,
    'the scheduled lane no longer invokes check-offbox-backup-freshness.mjs, so the ' +
      'staleness of the off-box backup copy is unobserved again',
  );
  assert.match(
    yml,
    /LAST_OFFBOX_BACKUP_PULL:\s*\$\{\{\s*vars\.LAST_OFFBOX_BACKUP_PULL\s*\}\}/,
    'the scheduled lane no longer passes the LAST_OFFBOX_BACKUP_PULL marker to the check, ' +
      'so the check would see an empty marker on every run',
  );
  assert.match(
    yml,
    /schedule:/,
    'deploy-drift-watchdog.yml no longer runs on a schedule, so nothing periodically ' +
      'evaluates the off-box backup marker',
  );
  // The job-level `if:` is its own way for this check to disappear silently: a
  // gate that no longer admits `schedule` leaves the job permanently skipped,
  // which reports nothing at all rather than failing.
  const job = /^ {2}offbox-backup-staleness:\n(?: {4}.*\n| *\n)*/m.exec(yml)?.[0] ?? '';
  assert.notEqual(job, '', 'the offbox-backup-staleness job is gone from deploy-drift-watchdog.yml');
  assert.match(
    job,
    /github\.event_name == 'schedule'/,
    "the offbox-backup-staleness job's own if: no longer admits the schedule event, so it would " +
      'be skipped on every scheduled run and report nothing',
  );
});

if (failures.length > 0) {
  console.error(`\n${failures.length} check(s) failed: ${failures.join(', ')}`);
  process.exit(1);
}
console.log('\nall off-box backup freshness checks passed');
