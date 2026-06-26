// tools/lint-no-direct-audit-write.mjs
// Block direct INSERT/UPDATE/DELETE against public.audit_log outside the
// internal/audit package. SELECTs are allowed (auditor queries).

import { readFileSync } from 'node:fs';
import { execSync } from 'node:child_process';

const ALLOWLIST_DIRS = [
  'apps/control-plane/internal/audit/',
  // auditverifier owns chain-replay tests that bootstrap rows directly,
  // and edge-api's chat dispatch performs the per-tenant chain-head write
  // synchronously so the SSE response can carry the audit id. Neither
  // can route through internal/audit.Log without a circular import.
  'apps/control-plane/internal/auditverifier/',
  'apps/edge-api/internal/chat/audit.go',
  // auditarchive is the sanctioned PHIPA retention-deletion path: it DELETEs
  // already-archived rows after writing an immutable manifest. This is
  // lifecycle management, not an audit-event write; routing through
  // internal/audit.Log would create a circular dependency and is not applicable.
  'apps/control-plane/internal/auditarchive/',
  'supabase/migrations/',
  'tools/lint-no-direct-audit-write.mjs',
];

const FORBIDDEN = [
  // schema-qualified
  /\binsert\s+into\s+public\.audit_log\b/i,
  /\bupdate\s+public\.audit_log\b/i,
  /\bdelete\s+from\s+public\.audit_log\b/i,
  // unqualified — search_path could resolve these to public.audit_log
  /\binsert\s+into\s+audit_log\b/i,
  /\bupdate\s+audit_log\b/i,
  /\bdelete\s+from\s+audit_log\b/i,
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
          console.error(`${file}:${i + 1}: forbidden direct write to audit_log — use internal/audit.Log`);
          violations++;
        }
      });
    }
  }
}

if (violations > 0) {
  console.error(`\n${violations} audit-write lint violation(s).`);
  process.exit(1);
}
console.log('lint-no-direct-audit-write: PASS');
