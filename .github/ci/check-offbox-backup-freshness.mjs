#!/usr/bin/env node
// Off-box backup pull staleness check (issue #1491).
//
// ── The defect this exists to catch ──────────────────────────────────────
//
// The demo box's backup has two halves with opposite observability. The
// on-box half (scripts/backup-box.sh, hive-box-backup.timer) writes a status
// file, posts to Alertmanager on failure, and alerts on its own staleness, so
// its health is visible. The off-box half (scripts/pull-box-backups.sh) is a
// manual command on a dev machine that wrote nothing anywhere, so its silence
// was indistinguishable from its success.
//
// On 2026-08-29 the on-box half was perfectly healthy, sixteen consecutive
// `OK ... files=4` lines, and the off-box copy was six days stale. Every
// signal was green and the real disaster posture was one disk failure away
// from losing everything after 2026-08-23. The observable half's health had
// silently stood in as evidence for a property only the unobservable half
// guaranteed.
//
// ── Where the marker lives, and why not on the box ───────────────────────
//
// The marker is the repository variable LAST_OFFBOX_BACKUP_PULL, written by
// scripts/pull-box-backups.sh with `gh variable set` after, and only after,
// that script prints `OK: all pulled days verified`.
//
// Issue #1491 suggested recording it on the box instead. That reads better
// and cannot actually be checked. Nothing that runs on a schedule can see a
// file on the box: the hosted runners have no SSH path to it (see
// external-uptime-probe.yml and post-deploy-verify.yml on that point), and
// the two workflows that DO run on the box's self-hosted runner are triggered
// by deploys and labels, never by a schedule. The remaining candidate, a new
// check inside scripts/backup-box.sh run by the box's own hourly cron, would
// be inert on merge: the box runs an installed copy under
// /home/sakib/hive-backups/bin that a human copies there by hand, so merging
// a check into the repository copy changes nothing about what actually runs.
// A repository variable is the one store the writing machine can reach and a
// scheduled lane can read, and merging this makes the check live immediately.
//
// ── Threshold ────────────────────────────────────────────────────────────
//
// 72 hours, overridable with OFFBOX_PULL_STALE_AFTER_HOURS for tests.
//
// Too tight is the real risk, not too loose. The writing machine is a laptop
// that is off for stretches, and a check that goes red every weekend gets
// muted, which recreates exactly the unobserved gap this exists to close
// (external-uptime-probe.yml and deploy-drift-watchdog.yml both record the
// same reasoning about crying wolf). A Friday-evening pull followed by a
// Monday-morning one is about 63 hours, so an ordinary weekend stays silent.
//
// Too loose is bounded by what the alarm is protecting. The box keeps 14
// daily sets, so a 72-hour-old off-box copy still means every missing day is
// recoverable from the box itself; the alarm fires while the exposure is
// still only "concentrated on one disk", not "gone". And 72 is well inside
// the six days that actually elapsed unnoticed.
//
// The asymmetry that settles it: clearing this alarm costs one command on
// about 6.7 MB per day, seconds of work. A cheap remedy justifies the tighter
// threshold, because a marginal firing costs almost nothing and a missed one
// costs the database.
//
// ── Absent marker is STALE ───────────────────────────────────────────────
//
// Never "unknown, assume fine". An unset variable, an unparseable value and a
// timestamp in the future all fail, because each of them means the same
// thing: nothing here proves a verified off-box copy exists.
//
// Run: node .github/ci/check-offbox-backup-freshness.mjs
// Reads:  LAST_OFFBOX_BACKUP_PULL          the marker (empty when unset)
//         OFFBOX_PULL_STALE_AFTER_HOURS    threshold, default 72
//         OFFBOX_PULL_NOW_EPOCH            test hook: unix seconds for "now"
// Exits:  0 fresh, 1 stale or undeterminable.

const REMEDY =
  'Run scripts/pull-box-backups.sh on the dev machine that holds the off-box copy. ' +
  'It pulls the encrypted artifacts, verifies their checksums, and records the marker ' +
  'this check reads. Nothing else writes that marker.';

const marker = (process.env.LAST_OFFBOX_BACKUP_PULL ?? '').trim();

const rawHours = process.env.OFFBOX_PULL_STALE_AFTER_HOURS ?? '72';
const hours = Number(rawHours);
if (!Number.isFinite(hours) || hours <= 0) {
  console.error(`FATAL: OFFBOX_PULL_STALE_AFTER_HOURS is '${rawHours}', which is not a positive number`);
  process.exit(1);
}

const rawNow = process.env.OFFBOX_PULL_NOW_EPOCH;
let nowMs = Date.now();
if (rawNow !== undefined && rawNow !== '') {
  const parsed = Number(rawNow);
  if (!Number.isFinite(parsed)) {
    console.error(`FATAL: OFFBOX_PULL_NOW_EPOCH is '${rawNow}', which is not a number`);
    process.exit(1);
  }
  nowMs = parsed * 1000;
}

function stale(headline) {
  console.error(`STALE: ${headline}`);
  console.error('');
  console.error(REMEDY);
  process.exit(1);
}

if (marker === '') {
  stale(
    'no verified off-box backup pull has ever been recorded (repository variable ' +
      'LAST_OFFBOX_BACKUP_PULL is unset or empty). An absent marker is treated as stale, ' +
      'never as "unknown, assume fine": the demo box may hold the only copy of the ' +
      'database, the chat history and the stored objects.',
  );
}

// Format written by scripts/pull-box-backups.sh:
//   pulled_at=2026-08-29T22:40:11Z newest_day=2026-08-29
const pulledAt = /(?:^|\s)pulled_at=(\S+)/.exec(marker)?.[1];
const newestDay = /(?:^|\s)newest_day=(\S+)/.exec(marker)?.[1] ?? '(not recorded)';

if (!pulledAt) {
  stale(
    `the marker does not carry a pulled_at timestamp, so it proves nothing. ` +
      `Value seen: '${marker}'.`,
  );
}

const pulledMs = Date.parse(pulledAt);
if (Number.isNaN(pulledMs)) {
  stale(`the marker's pulled_at value '${pulledAt}' is not a parseable timestamp.`);
}

const ageMs = nowMs - pulledMs;
const ageHours = ageMs / 3_600_000;

// A future timestamp is not freshness. It means a wrong clock or a hand-edited
// variable, and either way the marker is asserting a state nothing verified.
if (ageMs < 0) {
  stale(
    `the marker claims the last off-box pull happened in the future (pulled_at=${pulledAt}), ` +
      'so it cannot be trusted as evidence that a pull happened at all.',
  );
}

if (ageHours > hours) {
  stale(
    `the last verified off-box backup pull was ${ageHours.toFixed(1)} hours ago ` +
      `(pulled_at=${pulledAt}, newest day pulled ${newestDay}), older than the ${hours}-hour ` +
      'threshold. Everything the box has produced since then exists on exactly one disk.',
  );
}

console.log(
  `OK: last verified off-box backup pull was ${ageHours.toFixed(1)} hours ago ` +
    `(pulled_at=${pulledAt}, newest day pulled ${newestDay}, threshold ${hours} hours).`,
);
