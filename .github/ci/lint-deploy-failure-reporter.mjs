#!/usr/bin/env node
// Deploy tracking-issue reporter guard (issue #1416).
//
// deploy-demo-box.yml files ONE issue per outage, deduped on an open issue
// carrying a single label, and comments "Still failing" on it for every later
// red run. That design is deliberate and it has a hard requirement attached:
// the issue has to close when the workflow recovers. While it is open, a
// genuinely new failure with a completely different cause is downgraded to a
// one-line comment on a body describing the old one, with no guidance and no
// new-issue notification. The workflow's own comments already name that trap
// twice (on agent-workspace-coverage, and in post-deploy-verify.yml's reason
// for not sharing the label).
//
// Until PR for #1416 there was no close path at all. Issue #1416 was filed on
// 2026-08-29 for a disk-floor refusal, the cause merged four hours later, and
// the issue stayed open through twenty-one consecutive green runs still saying
// the migration had failed. It was read as the top demo blocker that evening
// while every merge that day had in fact deployed.
//
// This script fails the build if the close path is removed, disarmed, or
// allowed to drift out of step with the file path. It is a shape check, not a
// behaviour test: nothing here can execute a github-script block. The two
// things it can prove are the two things that actually broke, absence and
// asymmetry.
//
// Run: node .github/ci/lint-deploy-failure-reporter.mjs

import { readFileSync } from 'node:fs';
import { parse } from 'yaml';

// argv override so this guard can be pointed at a deliberately broken copy and
// shown to actually fail. A check nobody has ever seen go red is a check that
// might not be able to.
const WORKFLOW_FILE = process.argv[2] ?? '.github/workflows/deploy-demo-box.yml';
const FAILURE_JOB = 'report-failure';
const RECOVERY_JOB = 'report-recovered';

const errors = [];
const fail = (msg) => errors.push(msg);

const workflow = parse(readFileSync(WORKFLOW_FILE, 'utf8'));
const jobs = workflow?.jobs ?? {};

// Every `script:` body in a job, joined. The reporter jobs are one step each
// today; joining rather than indexing keeps this from breaking the moment one
// of them grows a checkout step in front of the script.
function scriptText(job) {
  return (job?.steps ?? [])
    .map((step) => step?.with?.script)
    .filter((s) => typeof s === 'string')
    .join('\n');
}

function needsList(job) {
  const needs = job?.needs;
  if (typeof needs === 'string') return [needs];
  return Array.isArray(needs) ? [...needs] : [];
}

const failureJob = jobs[FAILURE_JOB];
const recoveryJob = jobs[RECOVERY_JOB];

if (!failureJob) {
  fail(`${WORKFLOW_FILE} has no \`${FAILURE_JOB}\` job, so a red deploy on main files nothing.`);
}
if (!recoveryJob) {
  fail(
    `${WORKFLOW_FILE} has no \`${RECOVERY_JOB}\` job. Without it the tracking issue ` +
      `${FAILURE_JOB} files never closes, and the next failure with a different cause is ` +
      `downgraded to a comment on a stale body. That is issue #1416 exactly.`,
  );
}

if (failureJob && recoveryJob) {
  const failureIf = String(failureJob.if ?? '');
  const recoveryIf = String(recoveryJob.if ?? '');
  if (!failureIf.includes('failure()')) {
    fail(`${FAILURE_JOB}'s \`if:\` no longer tests failure(), so it will not file on a red run: ${failureIf || '(absent)'}`);
  }
  if (!recoveryIf.includes('success()')) {
    fail(`${RECOVERY_JOB}'s \`if:\` no longer tests success(), so it will not close on a green run: ${recoveryIf || '(absent)'}`);
  }

  // The load-bearing assertion. If the recovery job watches fewer jobs than
  // the failure job, a green run can close the very issue the untracked job's
  // failure just filed; if it watches more, a job the filer ignores can hold
  // the issue open forever. Either direction reopens the swallowing behaviour.
  const failureNeeds = needsList(failureJob).sort();
  const recoveryNeeds = needsList(recoveryJob).sort();
  if (failureNeeds.join(',') !== recoveryNeeds.join(',')) {
    fail(
      `${FAILURE_JOB} and ${RECOVERY_JOB} must watch the same jobs, but ` +
        `${FAILURE_JOB} needs [${failureNeeds.join(', ')}] and ${RECOVERY_JOB} needs ` +
        `[${recoveryNeeds.join(', ')}].`,
    );
  }
  if (failureNeeds.length === 0) {
    fail(`${FAILURE_JOB} has an empty \`needs:\`, so failure() has no ancestor jobs to test.`);
  }

  // Same label on both sides, read out of the scripts rather than assumed:
  // filing under one label and closing under another is silent, and looks
  // exactly like a working pair.
  const labelOf = (text) => text.match(/const label = '([^']+)'/)?.[1];
  const failureLabel = labelOf(scriptText(failureJob));
  const recoveryLabel = labelOf(scriptText(recoveryJob));
  if (!failureLabel || !recoveryLabel) {
    fail(
      `could not read a \`const label = '...'\` out of both reporter scripts ` +
        `(${FAILURE_JOB}: ${failureLabel ?? 'none'}, ${RECOVERY_JOB}: ${recoveryLabel ?? 'none'}). ` +
        `This guard cannot confirm they agree.`,
    );
  } else if (failureLabel !== recoveryLabel) {
    fail(
      `${FAILURE_JOB} files under '${failureLabel}' but ${RECOVERY_JOB} closes '${recoveryLabel}', ` +
        `so nothing it files is ever closed.`,
    );
  }

  const recoveryScript = scriptText(recoveryJob);
  if (!/issues\.update/.test(recoveryScript) || !/state: 'closed'/.test(recoveryScript)) {
    fail(
      `${RECOVERY_JOB} no longer closes anything: its script has no issues.update call setting ` +
        `state: 'closed'. Commenting without closing leaves the dedupe trap in place.`,
    );
  }
  if (!/issues\.create\b/.test(scriptText(failureJob))) {
    fail(`${FAILURE_JOB}'s script no longer calls issues.create, so an outage files no tracking issue.`);
  }
}

if (errors.length > 0) {
  for (const e of errors) console.error(`error: ${e}`);
  console.error(`\n${errors.length} problem(s) in ${WORKFLOW_FILE}'s failure reporter.`);
  process.exit(1);
}

console.log(
  `${WORKFLOW_FILE}: ${FAILURE_JOB} and ${RECOVERY_JOB} agree on label, needs and polarity.`,
);
