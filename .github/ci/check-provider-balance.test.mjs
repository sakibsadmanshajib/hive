// .github/ci/check-provider-balance.test.mjs
//
// Regression guard for issue #1411's provider balance alarm, and for its
// wiring.
//
// The defect: the OpenRouter account that every paid route in the live
// catalog draws on ran down to 1.46 USD of 10 purchased, and nothing anywhere
// read the balance on any schedule. The first signal available was a paid
// request failing. The check under test is the thing that makes that
// approach visible in advance, so its own negative controls have to be
// observed rather than reasoned about:
//
//   1. a healthy balance with a long runway passes;
//   2. a runway under the threshold FAILS, and the failure says how to fix it;
//   3. the threshold boundary is exact, on both sides;
//   4. a first run with no stored sample still produces a verdict, from the
//      provider's own weekly usage figure, rather than reporting green
//      because it has nothing to compare against;
//   5. an unreachable, erroring or malformed API FAILS. A monitor that reports
//      healthy when it could not read the number is worse than no monitor,
//      because it actively asserts a state nothing verified;
//   6. a missing credential FAILS for the same reason;
//   7. a balance under the absolute floor FAILS even when the measured burn
//      says the runway is long, since a burn rate measured over a quiet
//      window says nothing about the next busy hour;
//   8. the check is actually wired into a scheduled lane, and that lane
//      persists the sample it writes. Without those last assertions, every
//      test above could pass over a check nothing runs, or over one that
//      throws its history away between runs and is permanently blind to
//      account-wide burn, which is the same silent absence in a new costume.
//
// Run: node .github/ci/check-provider-balance.test.mjs

import assert from 'node:assert/strict';
import { spawn } from 'node:child_process';
import { createServer } from 'node:http';
import { mkdtempSync, readFileSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

const HERE = dirname(fileURLToPath(import.meta.url));
const SCRIPT = join(HERE, 'check-provider-balance.mjs');
const REPO_ROOT = join(HERE, '..', '..');
const WATCHER = join(REPO_ROOT, '.github', 'workflows', 'provider-balance-watch.yml');

// Fixed "now" so no case depends on the wall clock: 2026-09-02T18:00:00Z, the
// day the balance was measured at 1.46 USD, which is why the literals below
// read as they do.
const NOW_EPOCH = 1_788_458_400;
const DAY = 86_400;

// A stub OpenRouter. Every case drives the script against this rather than the
// real API, so the suite is offline, deterministic, and able to produce the
// error shapes the real endpoint will not produce on demand.
let stub = { credits: null, key: null, status: 200, body: null };
const server = createServer((req, res) => {
  if (stub.body !== null) {
    res.writeHead(stub.status, { 'content-type': 'application/json' });
    res.end(stub.body);
    return;
  }
  if (stub.status !== 200) {
    res.writeHead(stub.status, { 'content-type': 'application/json' });
    res.end(JSON.stringify({ error: { message: 'upstream is unhappy' } }));
    return;
  }
  const payload = req.url.startsWith('/key') ? stub.key : stub.credits;
  res.writeHead(200, { 'content-type': 'application/json' });
  res.end(JSON.stringify({ data: payload }));
});
await new Promise((resolve) => server.listen(0, '127.0.0.1', resolve));
const BASE = `http://127.0.0.1:${server.address().port}`;

let stateDir = mkdtempSync(join(tmpdir(), 'balance-state-'));
let stateFile = join(stateDir, 'state.json');

// Async, deliberately. The stub above serves from THIS process's event loop,
// and spawnSync blocks it, so a synchronous spawn would deadlock: the child's
// request could not be answered until the child exited. Every request would
// then time out and every case would go red with a network error, which reads
// exactly like the script being broken.
function run({ credits, key, status = 200, body = null, state, env = {} } = {}) {
  stub = { credits: credits ?? null, key: key ?? null, status, body };
  stateDir = mkdtempSync(join(tmpdir(), 'balance-state-'));
  stateFile = join(stateDir, 'state.json');
  if (state !== undefined) writeFileSync(stateFile, JSON.stringify(state));
  // env is built explicitly rather than spread from process.env, so an
  // OPENROUTER_API_KEY exported in the caller's shell cannot leak in and turn
  // the missing-credential case green.
  const base = {
    PATH: process.env.PATH,
    OPENROUTER_API_KEY: 'test-key-not-a-real-credential',
    OPENROUTER_API_BASE: BASE,
    PROVIDER_BALANCE_STATE_FILE: stateFile,
    PROVIDER_BALANCE_NOW_EPOCH: String(NOW_EPOCH),
    // One attempt: the retry ladder exists so a single transient blip does not
    // open a critical issue, and waiting it out in every error case here would
    // add seconds to the suite for nothing.
    PROVIDER_BALANCE_FETCH_ATTEMPTS: '1',
  };
  for (const [k, v] of Object.entries(env)) {
    if (v === undefined) delete base[k];
    else base[k] = v;
  }
  return new Promise((resolve) => {
    const child = spawn('node', [SCRIPT], { env: base });
    let stdout = '';
    let stderr = '';
    child.stdout.on('data', (chunk) => {
      stdout += chunk;
    });
    child.stderr.on('data', (chunk) => {
      stderr += chunk;
    });
    child.on('close', (status) => resolve({ status, stdout, stderr }));
  });
}

// A healthy account: 10 purchased, 1.50 spent, and a weekly figure implying a
// tenth of a dollar a day.
const healthyCredits = { total_credits: 10, total_usage: 1.5 };
const healthyKey = { usage: 1.5, usage_daily: 0.1, usage_weekly: 0.7, usage_monthly: 1.5 };

const failures = [];
async function check(name, fn) {
  try {
    await fn();
    console.log(`ok   ${name}`);
  } catch (err) {
    failures.push(name);
    console.error(`FAIL ${name}: ${err.message}`);
  }
}

await check('a healthy balance with a long runway passes and prints the number', async () => {
  const r = await run({ credits: healthyCredits, key: healthyKey });
  assert.equal(r.status, 0, `expected exit 0, got ${r.status}. stderr: ${r.stderr}`);
  assert.match(r.stdout, /^OK:/m);
  assert.match(r.stdout, /8\.50 USD remaining/);
  // 8.50 remaining at 0.10/day is 85 days.
  assert.match(r.stdout, /85\.0 days/);
});

await check('a runway under the threshold FAILS and names the remedy', async () => {
  // The state on 2026-09-02: 1.459 USD left, burning about a tenth of a dollar
  // a day, so about a fortnight at that rate but under a 30-day threshold.
  const r = await run({
    credits: { total_credits: 10, total_usage: 8.540654392 },
    key: { usage: 8.540654392, usage_daily: 0.05, usage_weekly: 0.7, usage_monthly: 2 },
    env: { PROVIDER_BALANCE_MIN_RUNWAY_DAYS: '30' },
  });
  assert.equal(r.status, 1, `expected exit 1, got ${r.status}. stdout: ${r.stdout}`);
  assert.match(r.stderr, /LOW:/);
  assert.match(r.stderr, /1\.46 USD remaining/);
  assert.match(r.stderr, /openrouter\.ai\/settings\/credits/);
});

await check('the runway threshold boundary is exact on both sides', async () => {
  // 7.00 remaining at 1.00/day is exactly 7 days: at the threshold, not under
  // it, so it passes. A hair less fails. An off-by-one here is the difference
  // between a monitor that fires a day early forever and one that never fires.
  const atThreshold = await run({
    credits: { total_credits: 10, total_usage: 3 },
    key: { usage: 3, usage_daily: 1, usage_weekly: 7, usage_monthly: 7 },
    env: { PROVIDER_BALANCE_MIN_RUNWAY_DAYS: '7' },
  });
  assert.equal(atThreshold.status, 0, `expected exit 0 at exactly 7 days, got ${atThreshold.status}. stderr: ${atThreshold.stderr}`);

  const underThreshold = await run({
    credits: { total_credits: 10, total_usage: 3.01 },
    key: { usage: 3.01, usage_daily: 1, usage_weekly: 7, usage_monthly: 7 },
    env: { PROVIDER_BALANCE_MIN_RUNWAY_DAYS: '7' },
  });
  assert.equal(underThreshold.status, 1, `expected exit 1 just under 7 days, got ${underThreshold.status}. stdout: ${underThreshold.stdout}`);
});

await check('a first run with no stored sample still produces a runway, from the weekly figure', async () => {
  const r = await run({
    credits: { total_credits: 10, total_usage: 9.3 },
    key: { usage: 9.3, usage_daily: 0.2, usage_weekly: 1.4, usage_monthly: 4 },
    env: { PROVIDER_BALANCE_MIN_RUNWAY_DAYS: '7' },
  });
  // 0.70 remaining at 1.4/7 = 0.20 per day is 3.5 days.
  assert.equal(r.status, 1, `expected exit 1, got ${r.status}. stdout: ${r.stdout}`);
  assert.match(r.stderr, /3\.5 days/);
  assert.match(r.stderr, /weekly/i, 'the verdict must say which burn source it used');
  assert.match(r.stderr, /no stored sample|first run/i);
});

await check('the stored sample is used when it implies a faster burn than the weekly average', async () => {
  // The spike case, and the reason the stored sample exists at all: the
  // provider's weekly average says 85 days while the last two days say four.
  // A monitor that averaged them, or that took the kinder number, would report
  // a comfortable runway through the demo day that empties the account.
  const r = await run({
    credits: { total_credits: 10, total_usage: 8 },
    key: { usage: 8, usage_daily: 0.5, usage_weekly: 0.7, usage_monthly: 3 },
    state: { samples: [{ t: NOW_EPOCH - 2 * DAY, usage: 7 }] },
    env: { PROVIDER_BALANCE_MIN_RUNWAY_DAYS: '7' },
  });
  // 2.00 remaining, 1.00 spent over 2 days = 0.50/day = 4 days.
  assert.equal(r.status, 1, `expected exit 1, got ${r.status}. stdout: ${r.stdout}`);
  assert.match(r.stderr, /4\.0 days/);
  assert.match(r.stderr, /account/i, 'the verdict must say the account-wide delta drove it');
});

await check('the run always writes a sample for the next run to compare against', async () => {
  const r = await run({ credits: healthyCredits, key: healthyKey });
  assert.equal(r.status, 0, `expected exit 0, got ${r.status}. stderr: ${r.stderr}`);
  const written = JSON.parse(readFileSync(stateFile, 'utf8'));
  assert.equal(written.samples.at(-1).usage, 1.5);
  assert.equal(written.samples.at(-1).t, NOW_EPOCH);
});

await check('a sample older than the window is pruned rather than averaging a spike away', async () => {
  const r = await run({
    credits: healthyCredits,
    key: healthyKey,
    state: {
      samples: [
        { t: NOW_EPOCH - 30 * DAY, usage: 0 },
        { t: NOW_EPOCH - 1 * DAY, usage: 1.4 },
      ],
    },
    env: { PROVIDER_BALANCE_BURN_WINDOW_DAYS: '2' },
  });
  assert.equal(r.status, 0, `expected exit 0, got ${r.status}. stderr: ${r.stderr}`);
  const written = JSON.parse(readFileSync(stateFile, 'utf8'));
  assert.equal(written.samples.length, 2, 'the 30-day-old sample should not have been kept');
  assert.equal(written.samples[0].t, NOW_EPOCH - 1 * DAY);
});

await check('a sample from the future, or one showing usage going backwards, is discarded', async () => {
  // total_usage only ever increases. A sample above the current figure means a
  // clock problem or an account reset, and extrapolating from it would produce
  // a negative burn and a runway of infinity: a false green by arithmetic.
  const r = await run({
    credits: healthyCredits,
    key: healthyKey,
    state: {
      samples: [
        { t: NOW_EPOCH - 1 * DAY, usage: 9.9 },
        { t: NOW_EPOCH + 5 * DAY, usage: 0 },
      ],
    },
  });
  assert.equal(r.status, 0, `expected exit 0, got ${r.status}. stderr: ${r.stderr}`);
  assert.doesNotMatch(r.stdout, /-\d/, 'a negative burn or runway reached the output');
});

await check('a balance under the absolute floor FAILS even with a long measured runway', async () => {
  // Burn measured over a quiet window says nothing about the next busy hour,
  // so the floor is a backstop under the runway test rather than the main
  // threshold. 0.50 left with no measurable spend is still an empty tank.
  const r = await run({
    credits: { total_credits: 10, total_usage: 9.5 },
    key: { usage: 9.5, usage_daily: 0, usage_weekly: 0, usage_monthly: 0 },
    env: { PROVIDER_BALANCE_MIN_RUNWAY_DAYS: '7', PROVIDER_BALANCE_MIN_USD: '2' },
  });
  assert.equal(r.status, 1, `expected exit 1, got ${r.status}. stdout: ${r.stdout}`);
  assert.match(r.stderr, /floor/i);
  assert.match(r.stderr, /0\.50 USD remaining/);
});

await check('no measurable burn above the floor passes, and says the runway is unknown', async () => {
  const r = await run({
    credits: { total_credits: 10, total_usage: 1 },
    key: { usage: 1, usage_daily: 0, usage_weekly: 0, usage_monthly: 0 },
  });
  assert.equal(r.status, 0, `expected exit 0, got ${r.status}. stderr: ${r.stderr}`);
  assert.match(r.stdout, /no measurable/i);
});

await check('an HTTP 500 from the provider FAILS rather than reporting healthy', async () => {
  const r = await run({ status: 500 });
  assert.equal(r.status, 1, `expected exit 1, got ${r.status}. stdout: ${r.stdout}`);
  assert.match(r.stderr, /FATAL:/);
  assert.match(r.stderr, /500/);
  assert.doesNotMatch(r.stdout, /^OK:/m);
});

await check('a 401 from the provider FAILS and does not echo the credential', async () => {
  const r = await run({ status: 401 });
  assert.equal(r.status, 1, `expected exit 1, got ${r.status}. stdout: ${r.stdout}`);
  assert.match(r.stderr, /401/);
  assert.doesNotMatch(r.stderr + r.stdout, /test-key-not-a-real-credential/);
});

await check('a malformed body FAILS rather than reporting healthy', async () => {
  const r = await run({ body: 'not json at all' });
  assert.equal(r.status, 1, `expected exit 1, got ${r.status}. stdout: ${r.stdout}`);
  assert.match(r.stderr, /FATAL:/);
});

await check('a well-formed body missing the balance fields FAILS', async () => {
  const r = await run({ body: JSON.stringify({ data: { something_else: 1 } }) });
  assert.equal(r.status, 1, `expected exit 1, got ${r.status}. stdout: ${r.stdout}`);
  assert.match(r.stderr, /FATAL:/);
});

await check('an unreachable API FAILS rather than reporting healthy', async () => {
  // A high loopback port nothing listens on refuses immediately, so this is a
  // real connection error rather than a timeout and the suite stays fast. Not
  // port 1: undici rejects that from its blocked-port list before it ever
  // reaches the network, which would exercise a different code path.
  const r = await run({ credits: healthyCredits, key: healthyKey, env: { OPENROUTER_API_BASE: 'http://127.0.0.1:45999' } });
  assert.equal(r.status, 1, `expected exit 1, got ${r.status}. stdout: ${r.stdout}`);
  assert.match(r.stderr, /FATAL:/);
});

await check('a missing credential FAILS rather than being skipped', async () => {
  const r = await run({ credits: healthyCredits, key: healthyKey, env: { OPENROUTER_API_KEY: undefined } });
  assert.equal(r.status, 1, `expected exit 1, got ${r.status}. stdout: ${r.stdout}`);
  assert.match(r.stderr, /OPENROUTER_API_KEY/);
});

await check('a non-numeric threshold FAILS rather than running at a threshold nobody chose', async () => {
  const r = await run({ credits: healthyCredits, key: healthyKey, env: { PROVIDER_BALANCE_MIN_RUNWAY_DAYS: '0x10' } });
  assert.equal(r.status, 1, `expected exit 1, got ${r.status}. stdout: ${r.stdout}`);
  assert.match(r.stderr, /PROVIDER_BALANCE_MIN_RUNWAY_DAYS/);
});

await check('an unreadable state file is treated as a first run, not as a crash', async () => {
  // The cache is best effort. A corrupt restore must degrade to the weekly
  // figure, not take the whole alarm offline.
  const r = await run({ credits: healthyCredits, key: healthyKey, state: 'corrupt' });
  assert.equal(r.status, 0, `expected exit 0, got ${r.status}. stderr: ${r.stderr}`);
  assert.match(r.stdout, /^OK:/m);
});

await check('provider-balance-watch.yml actually runs this check on a schedule', async () => {
  const yml = readFileSync(WATCHER, 'utf8');
  assert.match(
    yml,
    /node \.github\/ci\/check-provider-balance\.mjs/,
    'the scheduled lane no longer invokes check-provider-balance.mjs, so the provider ' +
      'balance is unobserved again',
  );
  assert.match(
    yml,
    /OPENROUTER_API_KEY:\s*\$\{\{\s*secrets\.OPENROUTER_API_KEY\s*\}\}/,
    'the lane no longer passes the OPENROUTER_API_KEY secret, so every run would fail on a ' +
      'missing credential instead of reading a balance',
  );
  assert.match(yml, /schedule:/, 'provider-balance-watch.yml no longer runs on a schedule');
  const job = /^ {2}balance:\n(?: {4}.*\n| *\n)*/m.exec(yml)?.[0] ?? '';
  assert.notEqual(job, '', 'the balance job is gone from provider-balance-watch.yml');
  assert.match(
    job,
    /github\.event_name == 'schedule'/,
    "the balance job's own if: no longer admits the schedule event, so it would be skipped on " +
      'every scheduled run and report nothing',
  );
});

await check('the scheduled lane persists the sample between runs', async () => {
  const yml = readFileSync(WATCHER, 'utf8');
  assert.match(
    yml,
    /actions\/cache\/restore@/,
    'nothing restores the previous sample, so every run is a first run and the account-wide ' +
      'burn rate can never be measured',
  );
  assert.match(
    yml,
    /actions\/cache\/save@/,
    'nothing saves the sample this run wrote, so the next run has nothing to compare against',
  );
});

await check('the alarm reaches a human rather than only a red square in the Actions tab', async () => {
  const yml = readFileSync(WATCHER, 'utf8');
  assert.match(
    yml,
    /gh issue create/,
    'the lane files no tracking issue, so the only signal is a failed scheduled run that ' +
      'nobody is subscribed to. This repository already has a documented case of a monitor ' +
      'sitting quietly red for days',
  );
  assert.match(
    yml,
    /gh issue close/,
    'nothing closes the tracking issue on recovery, so a stale open issue would suppress the ' +
      'next alert forever (the defect issue #1416 records)',
  );
  assert.match(yml, /issues:\s*write/, 'the lane cannot file an issue without issues: write');
});

if (failures.length > 0) {
  server.close();
  console.error(`\n${failures.length} check(s) failed: ${failures.join(', ')}`);
  process.exit(1);
}
server.close();
console.log('\nall provider balance checks passed');
