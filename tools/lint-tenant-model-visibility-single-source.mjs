// tools/lint-tenant-model-visibility-single-source.mjs
// Keep per-tenant model entitlement to one decider.
//
// The original defect: the visibility rules were implemented in the catalog
// listing query only, so the model list a tenant saw and the models a tenant
// could actually invoke were decided in different places, and the inference
// path had no decision at all. The fix routes every verdict through
// catalog.AliasVisibleToTenant.
//
// This lint blocks the recurrence: any new SQL or Go that reads
// tenant_model_visibility outside the catalog package would be a second copy of
// the entitlement rules, free to drift from the first. Enforcement stays inside
// apps/control-plane/internal/catalog/, and callers that need a verdict ask for
// one (Repository.IsAliasVisibleToTenant / routing.TenantEntitlements).

import { readFileSync } from 'node:fs';
import { execSync } from 'node:child_process';

// Files allowed to reference the table at all.
const ALLOWLIST = [
  'apps/control-plane/internal/catalog/',  // the single source: query, predicate, admin CRUD
  'supabase/migrations/',                  // schema
  'tools/lint-tenant-model-visibility-single-source.mjs',
];

const TABLE = 'tenant_model_visibility';
const DIR_RE = /^(apps|packages)\//;
const EXT_RE = /\.(go|ts|tsx|sql)$/;

const files = execSync('git ls-files', { encoding: 'utf8' })
  .split('\n')
  .filter(Boolean)
  .filter(f => DIR_RE.test(f) && EXT_RE.test(f));

let violations = 0;
for (const file of files) {
  if (ALLOWLIST.some(p => file.startsWith(p))) continue;
  const lines = readFileSync(file, 'utf8').split('\n');
  lines.forEach((line, i) => {
    if (!line.includes(TABLE)) return;
    // Comments may name the table; only code that touches it is a violation.
    const trimmed = line.trim();
    if (trimmed.startsWith('//') || trimmed.startsWith('*') || trimmed.startsWith('--')) return;
    console.error(
      `${file}:${i + 1}: ${TABLE} may only be read in apps/control-plane/internal/catalog/ — ` +
      `ask for a verdict via Repository.IsAliasVisibleToTenant instead of re-implementing the rules`,
    );
    violations++;
  });
}

if (violations > 0) {
  console.error(`\n${violations} tenant-model-visibility lint violation(s).`);
  process.exit(1);
}
console.log('lint-tenant-model-visibility-single-source: PASS');
