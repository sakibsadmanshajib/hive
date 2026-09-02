#!/usr/bin/env node
// OpenRouter balance and runway check (issue #1411).
//
// ── The defect this exists to catch ──────────────────────────────────────
//
// On 2026-08-29 the OpenRouter account this product's paid routes draw on
// held 1.59 USD of 10 purchased. Four days later it held 1.46 and was still
// draining. Nothing in this repository read that number on any schedule:
// scripts/report-free-pool-health.py probes only the free pool, which is
// unaffected by the balance, and ci.yml's out-of-credit classifier is by
// construction downstream of a job that has already gone red. The first
// available signal was a paid request failing in front of whoever was
// watching, which for a demo is the worst possible place to learn it.
//
// ── Why days of runway, not a dollar floor ───────────────────────────────
//
// A fixed dollar threshold is meaningless without the spend rate. 1.46 USD is
// a fortnight at the rate this account has averaged and an afternoon during a
// heavy demo. So the primary threshold is DAYS OF RUNWAY at the measured burn,
// which is the number a human actually needs: how long they have to act.
//
// ── Where the burn rate comes from, and why from two sources ─────────────
//
//   1. The provider's own key-scoped `usage_weekly`, divided by seven. Always
//      available, including on the very first run, and smooth. It is the
//      answer to "what do you do when there is no previous sample": you still
//      produce a runway, from this, and say that is where it came from.
//   2. The account-wide delta between `total_usage` now and the oldest stored
//      sample inside the burn window. Exact, and reacts to a spike within one
//      run interval rather than diluting it across a week.
//
// The larger of the two wins, deliberately. They disagree exactly when a quiet
// week is followed by a busy day, and in that case the weekly average is the
// dangerous number: it would report a comfortable runway right through the
// demo that empties the account. Taking the max means the alarm can fire early
// and cannot fire late.
//
// The window `usage_weekly` covers is not documented as rolling or as
// calendar anchored, and the figures the endpoint returns are consistent with
// either reading. That ambiguity matters in one direction only: if it resets
// on a fixed weekday, source 1 under-reads for a day or so after the reset.
// Source 2 is what covers that, which is a second reason not to rely on the
// provider's figure alone once a sample exists.
//
// Source 1 is scoped to the key this runs with; source 2 is account-wide. They
// agree today, because that key's `usage` equals the account's `total_usage`.
// If a second key is ever added they will not, so the check compares them and
// says so in its output rather than silently under-reading account burn.
//
// ── The floor is a backstop, not the threshold ───────────────────────────
//
// A burn rate measured over a quiet window says nothing about the next busy
// hour, and at zero measured burn the runway is arithmetically infinite. So a
// small absolute floor sits underneath the runway test. It is not the main
// threshold and is deliberately low.
//
// ── Unreadable is not healthy ────────────────────────────────────────────
//
// Every failure to read the number exits non-zero: an HTTP error, an
// unparseable body, a body missing the fields, an absent credential, a
// misconfigured threshold. A monitor that reports green when it could not
// reach the API is worse than no monitor, because it actively asserts a state
// nothing verified. The retry ladder below exists so that a single transient
// blip does not open a critical issue, not to soften that rule.
//
// Run: node .github/ci/check-provider-balance.mjs
// Reads:  OPENROUTER_API_KEY                     required, never printed
//         OPENROUTER_API_BASE                    default https://openrouter.ai/api/v1
//         PROVIDER_BALANCE_MIN_RUNWAY_DAYS       default 7
//         PROVIDER_BALANCE_MIN_USD               default 2
//         PROVIDER_BALANCE_BURN_WINDOW_DAYS      default 3
//         PROVIDER_BALANCE_STATE_FILE            default provider-balance-state.json
//         PROVIDER_BALANCE_FETCH_ATTEMPTS        default 3
//         PROVIDER_BALANCE_NOW_EPOCH             test hook: unix seconds for "now"
// Exits:  0 healthy, 1 low or undeterminable.

import { readFileSync, writeFileSync } from 'node:fs';

const REMEDY =
  'Top up the account at https://openrouter.ai/settings/credits. Every paid route in ' +
  'deploy/litellm/config.yaml draws on this one balance, including the deepseek pair, the ' +
  'document vision route, the upstream-actual auto route and the paid leg of the RAG ' +
  'embedding cascade. That file declares no chat fallback on purpose (one alias, one price), ' +
  'so at zero balance those routes refuse outright rather than degrading to another model.';

function fatal(message) {
  console.error(`FATAL: ${message}`);
  console.error('');
  console.error(
    'This is a failure to READ the balance, not a healthy balance. It is reported as a ' +
      'failure because a monitor that goes green when it could not reach the API asserts a ' +
      'state nothing verified.',
  );
  process.exit(1);
}

// Plain decimal only, checked before Number(). Number() also accepts '0x10'
// (16), '1e3' and '0b1010', so an operator typo could silently configure a
// threshold nothing intended, and a monitor running at a threshold nobody
// chose is barely better than no monitor.
function positiveNumberEnv(name, fallback, { allowZero = false } = {}) {
  const raw = process.env[name] ?? String(fallback);
  const ok = /^\d+(\.\d+)?$/.test(raw);
  const value = ok ? Number(raw) : NaN;
  if (!Number.isFinite(value) || (allowZero ? value < 0 : value <= 0)) {
    fatal(`${name} is '${raw}', which is not a ${allowZero ? 'non-negative' : 'positive'} decimal number`);
  }
  return value;
}

const apiKey = (process.env.OPENROUTER_API_KEY ?? '').trim();
if (apiKey === '') {
  fatal(
    'OPENROUTER_API_KEY is unset or empty, so the balance cannot be read at all. An absent ' +
      'credential is never "skip this check": it is the check being blind.',
  );
}

const base = (process.env.OPENROUTER_API_BASE ?? 'https://openrouter.ai/api/v1').replace(/\/+$/, '');
const minRunwayDays = positiveNumberEnv('PROVIDER_BALANCE_MIN_RUNWAY_DAYS', 7);
const minUsd = positiveNumberEnv('PROVIDER_BALANCE_MIN_USD', 2, { allowZero: true });
const windowDays = positiveNumberEnv('PROVIDER_BALANCE_BURN_WINDOW_DAYS', 3);
const attempts = positiveNumberEnv('PROVIDER_BALANCE_FETCH_ATTEMPTS', 3);
const stateFile = process.env.PROVIDER_BALANCE_STATE_FILE ?? 'provider-balance-state.json';
const nowSec = process.env.PROVIDER_BALANCE_NOW_EPOCH
  ? positiveNumberEnv('PROVIDER_BALANCE_NOW_EPOCH', 0)
  : Math.floor(Date.now() / 1000);

const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

// Retried, because one transient 5xx or reset connection would otherwise open
// a priority:critical issue about a balance that is fine, and a monitor that
// cries wolf gets muted. Exhausting the ladder is still fatal.
async function getJson(path) {
  let last = '';
  for (let attempt = 1; attempt <= attempts; attempt += 1) {
    try {
      const res = await fetch(`${base}${path}`, {
        headers: { authorization: `Bearer ${apiKey}` },
        signal: AbortSignal.timeout(20_000),
      });
      const text = await res.text();
      if (!res.ok) {
        // The body is not echoed. OpenRouter's error bodies are short and
        // harmless today, but this text reaches a public issue and a public
        // run log, and the request that produced it carried a credential.
        last = `HTTP ${res.status} from ${path}`;
      } else {
        try {
          const parsed = JSON.parse(text);
          if (parsed?.data && typeof parsed.data === 'object') return parsed.data;
          last = `${path} returned a body with no data object`;
        } catch {
          last = `${path} returned a body that is not JSON`;
        }
      }
    } catch (err) {
      // undici reports a refused connection as a bare TypeError whose real
      // code hides one or two levels down, and "TypeError" in an alert body
      // tells the reader nothing about whether the network or the endpoint is
      // at fault.
      const code =
        err?.cause?.code ??
        err?.cause?.errors?.[0]?.code ??
        err?.cause?.message ??
        err?.code ??
        err?.name ??
        'unknown error';
      last = `${path} could not be reached: ${code}`;
    }
    if (attempt < attempts) await sleep(500 * attempt);
  }
  fatal(`could not read the OpenRouter balance after ${attempts} attempt(s). Last error: ${last}`);
}

function num(value) {
  return typeof value === 'number' && Number.isFinite(value) ? value : null;
}

const credits = await getJson('/credits');
const key = await getJson('/key');

const totalCredits = num(credits.total_credits);
const totalUsage = num(credits.total_usage);
if (totalCredits === null || totalUsage === null) {
  fatal(
    '/credits returned no usable total_credits and total_usage pair, so the remaining balance ' +
      'cannot be computed. The endpoint contract may have changed.',
  );
}
const remaining = totalCredits - totalUsage;

// ── Stored samples: best effort, never load-bearing for correctness ────────
// A corrupt or absent restore degrades to the weekly figure. It must not take
// the whole alarm offline, which would convert a partial signal into none.
let samples = [];
try {
  const parsed = JSON.parse(readFileSync(stateFile, 'utf8'));
  if (Array.isArray(parsed?.samples)) samples = parsed.samples;
} catch {
  samples = [];
}
const cutoff = nowSec - windowDays * 86_400;
samples = samples
  .filter(
    (s) =>
      num(s?.t) !== null &&
      num(s?.usage) !== null &&
      s.t <= nowSec && // a future sample means a clock problem, not history
      s.t >= cutoff &&
      s.usage <= totalUsage, // total_usage only ever rises; a higher one is an account reset
  )
  .sort((a, b) => a.t - b.t);

const oldest = samples[0];
// Under half an hour of separation the delta is noise: a handful of requests
// extrapolated to a day produces a runway figure nobody should act on.
const elapsedDays = oldest ? (nowSec - oldest.t) / 86_400 : 0;
const deltaBurn = oldest && elapsedDays >= 0.02 ? (totalUsage - oldest.usage) / elapsedDays : null;

const weeklyUsage = num(key.usage_weekly);
const weeklyBurn = weeklyUsage === null ? null : weeklyUsage / 7;

let burn = null;
let burnSource = '';
if (deltaBurn !== null && (weeklyBurn === null || deltaBurn >= weeklyBurn)) {
  burn = deltaBurn;
  burnSource = `account-wide usage delta over the last ${elapsedDays.toFixed(1)} days`;
} else if (weeklyBurn !== null) {
  burn = weeklyBurn;
  burnSource =
    deltaBurn === null
      ? "the provider's own weekly usage figure for this key (no usable stored sample: either this is a first run, the cache was evicted, or the newest sample is too recent to extrapolate from)"
      : "the provider's own weekly usage figure for this key";
}

const runwayDays = burn !== null && burn > 0 ? remaining / burn : null;

const notes = [];
const keyUsage = num(key.usage);
if (keyUsage !== null && totalUsage - keyUsage > 0.01) {
  notes.push(
    `NOTE: this key has spent ${keyUsage.toFixed(2)} of the account's ${totalUsage.toFixed(2)}, so ` +
      "the provider's weekly figure covers only part of the account's spend. The account-wide " +
      'delta above is the number to trust.',
  );
}

// Written before the verdict, so a run that ends red still leaves the next run
// a baseline. Best effort for the same reason the read is.
try {
  const next = [...samples, { t: nowSec, usage: totalUsage }].slice(-200);
  writeFileSync(stateFile, `${JSON.stringify({ samples: next }, null, 2)}\n`);
} catch (err) {
  notes.push(`NOTE: the sample for the next run could not be written to ${stateFile}: ${err.message}`);
}

const summary =
  `${remaining.toFixed(2)} USD remaining of ${totalCredits.toFixed(2)} purchased ` +
  `(${totalUsage.toFixed(2)} spent). ` +
  (burn !== null && burn > 0
    ? `Burn ${burn.toFixed(3)} USD/day from ${burnSource}, runway ${runwayDays.toFixed(1)} days.`
    : `No measurable burn from ${burnSource || 'any available source'}, so the runway is unknown.`);

function low(headline) {
  console.error(`LOW: ${headline}`);
  console.error(summary);
  for (const note of notes) console.error(note);
  console.error('');
  console.error(REMEDY);
  process.exit(1);
}

if (remaining < minUsd) {
  low(
    `the OpenRouter balance is ${remaining.toFixed(2)} USD, under the ${minUsd.toFixed(2)} USD floor. ` +
      'The floor sits under the runway test because a burn rate measured over a quiet window ' +
      'says nothing about the next busy hour.',
  );
}

if (runwayDays !== null && runwayDays < minRunwayDays) {
  low(
    `the OpenRouter balance has ${runwayDays.toFixed(1)} days of runway left at the current burn, ` +
      `under the ${minRunwayDays}-day threshold.`,
  );
}

console.log(`OK: ${summary} Threshold ${minRunwayDays} days, floor ${minUsd.toFixed(2)} USD.`);
for (const note of notes) console.log(note);
