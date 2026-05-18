#!/usr/bin/env node
// tools/soc2-coverage-report.mjs
//
// Reads tools/soc2-control-map.yaml + queries audit_log for the last N
// minutes, emits docs/compliance/SOC2-LOG-COVERAGE.md. Exits non-zero
// when any control has zero matching events in the window (CI gate
// when `--check` is passed).
//
// Env:
//   HIVE_TEST_DB_URL or SUPABASE_DB_URL  Postgres DSN (required)
//   SOC2_SINCE_MIN                       Window in minutes (default 60)

import { readFileSync, writeFileSync, mkdirSync } from 'node:fs';
import { dirname } from 'node:path';
import { parse as parseYAML } from 'yaml';
import pg from 'pg';

const SINCE_MIN = Number(process.env.SOC2_SINCE_MIN || 60);
const DB_URL = process.env.HIVE_TEST_DB_URL || process.env.SUPABASE_DB_URL;
const MODE = process.argv.includes('--check') ? 'check' : 'report';
const OUTPUT_PATH = 'docs/compliance/SOC2-LOG-COVERAGE.md';

if (!Number.isFinite(SINCE_MIN) || SINCE_MIN <= 0) {
  console.error('SOC2: SOC2_SINCE_MIN must be a positive finite number');
  process.exit(2);
}

if (!DB_URL) {
  console.error('SOC2: HIVE_TEST_DB_URL or SUPABASE_DB_URL required');
  process.exit(2);
}

const map = parseYAML(readFileSync('tools/soc2-control-map.yaml', 'utf8'));

const client = new pg.Client({ connectionString: DB_URL });
let res;
try {
  await client.connect();
  const since = `now() - interval '${SINCE_MIN} minutes'`;
  res = await client.query(
    `SELECT action, count(*)::int AS n
       FROM public.audit_log
      WHERE ts >= ${since}
      GROUP BY action`,
  );
} finally {
  await client.end();
}

const counts = Object.fromEntries(res.rows.map((r) => [r.action, r.n]));

const lines = [];
lines.push('# SOC 2 Type II — Audit Log Coverage Report');
lines.push('');
lines.push(`Window: last ${SINCE_MIN} min (generated ${new Date().toISOString()}).`);
lines.push('');
lines.push('| Control | Description | Action(s) | Hits |');
lines.push('| --- | --- | --- | --- |');

let missing = 0;
for (const [ctrl, def] of Object.entries(map.controls)) {
  const total = def.actions.reduce((s, a) => s + (counts[a] || 0), 0);
  if (total === 0) missing++;
  lines.push(
    `| **${ctrl}** | ${def.description} | ${def.actions.join(', ')} | ${total} |`,
  );
}

const body = lines.join('\n') + '\n';
mkdirSync(dirname(OUTPUT_PATH), { recursive: true });
writeFileSync(OUTPUT_PATH, body);

if (MODE === 'check' && missing > 0) {
  console.error(
    `SOC2: ${missing} control(s) have zero matching events in the last ${SINCE_MIN} min`,
  );
  process.exit(1);
}

console.log(
  `SOC2: report written to ${OUTPUT_PATH}; ${missing} control(s) missing in last ${SINCE_MIN} min.`,
);
