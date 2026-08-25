#!/usr/bin/env node
// Self-check for the guards in this directory.
//
// Each guard is fed both harness payload shapes, because the two differ:
// Claude Code nests the tool arguments under `tool_input`, while Cursor's
// blocking hooks put `command` (beforeShellExecution) or `file_path` plus an
// `edits` array (file edits) at the top level. Both harnesses stamp
// `hook_event_name`, so that field cannot be used to tell them apart.
//
// Both harnesses also treat exit code 2 as a block, so the guards keep a
// single output contract: plain text on stdout, exit 2 to deny.
//
// The clean case for a guard always runs before its tripping case, so a
// block can never be a pre-existing failure wearing a mutation's name.
//
// Run: node .claude/hooks/hooks.selfcheck.js

const { spawnSync } = require('child_process');
const path = require('path');

const HOOKS_DIR = __dirname;

// Synthetic, never a real credential. Assembled at runtime so the literal
// never appears in this file, which the secrets scanner reads on write.
const FAKE_AWS_KEY = 'AKIA' + 'A'.repeat(16);
const FAKE_GENERIC_KEY = 'k'.repeat(20);

function run(hook, payload) {
  const res = spawnSync(process.execPath, [path.join(HOOKS_DIR, hook)], {
    input: JSON.stringify(payload),
    encoding: 'utf8',
  });
  return { code: res.status, out: (res.stdout || '').trim() };
}

// shape: 'claude' nests under tool_input, 'cursor' is flat.
const shell = (shape, command) => shape === 'claude'
  ? { hook_event_name: 'PreToolUse', tool_name: 'Bash', tool_input: { command } }
  : { hook_event_name: 'beforeShellExecution', command, cwd: '/tmp' };

const edit = (shape, file_path, text) => shape === 'claude'
  ? { hook_event_name: 'PreToolUse', tool_name: 'Write', tool_input: { file_path, content: text } }
  : { hook_event_name: 'afterFileEdit', file_path, edits: [{ old_string: '', new_string: text }] };

const cases = [];
for (const shape of ['claude', 'cursor']) {
  cases.push(
    // bash-safety: clean first, then the hard block, then a warning.
    { name: `bash-safety clean (${shape})`, hook: 'bash-safety.js',
      payload: shell(shape, 'ls -la'), code: 0, absent: ['BLOCKED', 'WARNING'] },
    { name: `bash-safety block killall node (${shape})`, hook: 'bash-safety.js',
      payload: shell(shape, 'killall node'), code: 2, present: ['BLOCKED'] },
    { name: `bash-safety warn force push (${shape})`, hook: 'bash-safety.js',
      payload: shell(shape, 'git push --force origin main'), code: 0, present: ['WARNING: Force push'] },

    // commit-guard: warn only, never blocks.
    { name: `commit-guard clean (${shape})`, hook: 'commit-guard.js',
      payload: shell(shape, 'git status'), code: 0, absent: ['COMMIT GUARD'] },
    { name: `commit-guard warn on commit (${shape})`, hook: 'commit-guard.js',
      payload: shell(shape, "git commit -m 'stuff happened'"), code: 0, present: ['COMMIT GUARD'] },

    // secrets-scanner: clean first, then the hard block, then a warning.
    { name: `secrets-scanner clean (${shape})`, hook: 'secrets-scanner.js',
      payload: edit(shape, 'src/example.ts', 'export const count = 1;\n'), code: 0, absent: ['BLOCKED', 'WARNING'] },
    { name: `secrets-scanner block aws key (${shape})`, hook: 'secrets-scanner.js',
      payload: edit(shape, 'src/example.ts', `const k = "${FAKE_AWS_KEY}";\n`), code: 2, present: ['BLOCKED', 'AWS access key'] },
    { name: `secrets-scanner warn api_key assignment (${shape})`, hook: 'secrets-scanner.js',
      payload: edit(shape, 'src/example.ts', `const apiKey = "${FAKE_GENERIC_KEY}";\n`), code: 0, present: ['SECRET WARNING', 'Possible API key assignment'], absent: ['BLOCKED'] },
  );
}

let failed = 0;
for (const c of cases) {
  const { code, out } = run(c.hook, c.payload);
  const problems = [];
  if (code !== c.code) problems.push(`exit ${code}, want ${c.code}`);
  for (const s of c.present || []) if (!out.includes(s)) problems.push(`missing ${JSON.stringify(s)}`);
  for (const s of c.absent || []) if (out.includes(s)) problems.push(`unexpected ${JSON.stringify(s)}`);

  if (problems.length) {
    failed++;
    console.log(`FAIL  ${c.name}: ${problems.join('; ')}`);
    console.log(`      stdout: ${out || '(empty)'}`);
  } else {
    console.log(`ok    ${c.name}  [exit ${code}] ${out ? out.split('\n')[0].slice(0, 72) : '(no output)'}`);
  }
}

console.log(`\n${cases.length - failed}/${cases.length} passed`);
process.exit(failed ? 1 : 0);
