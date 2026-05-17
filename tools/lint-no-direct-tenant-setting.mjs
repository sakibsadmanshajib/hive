#!/usr/bin/env node

import { execSync } from 'child_process';
import { readFileSync } from 'fs';

const DIR_RE = /^(apps|packages|deploy|tools|supabase)\//;
const EXT_RE = /\.(go|tsx?|jsx?|mjs|cjs|sql|ya?ml)$/;

const files = execSync('git ls-files', { encoding: 'utf8' })
  .split('\n')
  .filter(Boolean)
  .filter(f => DIR_RE.test(f) && EXT_RE.test(f));

const violations = files.filter(f => {
  let content;
  try {
    content = execSync(`git show HEAD:${f}`, { encoding: 'utf8' });
  } catch {
    try {
      content = readFileSync(f, 'utf8');
    } catch {
      return false;
    }
  }
  return /tenant_settings/.test(content);
});

if (violations.length > 0) {
  console.error('❌ lint-no-direct-tenant-setting: FAIL');
  violations.forEach(f => console.error(`  ${f}`));
  process.exit(1);
} else {
  console.log('✓ lint-no-direct-tenant-setting: PASS');
}
