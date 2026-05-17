// tools/lint-no-direct-tenant-setting.mjs
// Block code paths that read or write public.tenant_settings without going
// through the internal/tenant/settings resolver. Mirrors the Phase 17
// lint-no-customer-usd.mjs pattern.

import { readFileSync } from 'node:fs';
import { execSync } from 'node:child_process';

const ALLOWLIST_DIRS = [
  'apps/control-plane/internal/tenant/settings/',
  'supabase/migrations/',
  'tools/lint-no-direct-tenant-setting.mjs',
];

// Narrow to actual SQL access patterns so the lint blocks real direct
// reads/writes and ignores comments, docs, identifier mentions, channel
// names (`tenant_settings_changed`), and unrelated symbols.
const FORBIDDEN = [
  /\bfrom\s+(public\.)?tenant_settings\b/i,
  /\binto\s+(public\.)?tenant_settings\b/i,
  /\bupdate\s+(public\.)?tenant_settings\b/i,
  /\bdelete\s+from\s+(public\.)?tenant_settings\b/i,
  /\bjoin\s+(public\.)?tenant_settings\b/i,
  /\busing\s+(public\.)?tenant_settings\b/i,
  /\breferences\s+(public\.)?tenant_settings\b/i,
];

const DIR_RE = /^(apps|packages|deploy|tools|supabase)\//;
const EXT_RE = /\.(go|tsx?|jsx?|mjs|cjs|sql|ya?ml)$/;

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
          console.error(`${file}:${i + 1}: forbidden direct access to tenant_settings — use internal/tenant/settings.Resolver`);
          violations++;
        }
      });
    }
  }
}

if (violations > 0) {
  console.error(`\n${violations} tenant-settings lint violation(s).`);
  process.exit(1);
}
console.log('lint-no-direct-tenant-setting: PASS');
