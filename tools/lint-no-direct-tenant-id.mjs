// tools/lint-no-direct-tenant-id.mjs
// Block Go handlers that take tenant_id from the request body, query string,
// or header. tenant_id must always come from the resolved auth context via
// auth.TenantID(ctx) so RLS, RBAC, and audit cannot be spoofed.

import { readFileSync } from 'node:fs';
import { execSync } from 'node:child_process';

const ALLOWLIST_DIRS = [
  'apps/control-plane/internal/tenants/',         // tenant-switch handler reads from body deliberately
  'apps/control-plane/internal/signup/',          // webhook receives user_id (not tenant_id) from Supabase
  'apps/control-plane/internal/tenant/settings/', // LISTEN/NOTIFY payload, not HTTP request
  'apps/edge-api/internal/auth/',                 // ctx writers
  'supabase/migrations/',
  'tools/lint-no-direct-tenant-id.mjs',
];

const FORBIDDEN = [
  /\.FormValue\(\s*"tenant_id"\s*\)/,
  /\.Get\(\s*"X-Tenant-Id"\s*\)/i,
  /r\.URL\.Query\(\)\.Get\(\s*"tenant_id"\s*\)/,
  /json:"tenant_id"/,    // a Go struct receiving tenant_id from the wire is a smell
];

const DIR_RE = /^(apps|packages)\//;
const EXT_RE = /\.go$/;

const files = execSync('git ls-files', { encoding: 'utf8' })
  .split('\n')
  .filter(Boolean)
  .filter(f => DIR_RE.test(f) && EXT_RE.test(f));

let violations = 0;
for (const file of files) {
  if (ALLOWLIST_DIRS.some(p => file.startsWith(p))) continue;
  const text = readFileSync(file, 'utf8');
  for (const re of FORBIDDEN) {
    if (re.test(text)) {
      const lines = text.split('\n');
      lines.forEach((line, i) => {
        if (re.test(line)) {
          console.error(`${file}:${i + 1}: forbidden direct tenant_id read — use auth.TenantID(ctx)`);
          violations++;
        }
      });
    }
  }
}

if (violations > 0) {
  console.error(`\n${violations} tenant-id lint violation(s).`);
  process.exit(1);
}
console.log('lint-no-direct-tenant-id: PASS');
