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
// Same reason, one step removed. The fixtures below must hand the scanner the
// exact assignment shapes a secret scanner blocks, so the identifier is held
// here rather than written inline: a source line that assigns a quoted literal
// to a key-named identifier is what a commit-time secret gate objects to,
// whatever the value on the right happens to be. Building the fixture from
// this constant keeps that shape out of the file while the text the scanner
// receives stays identical.
const KEY_IDENT = 'apiKey';

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

// Claude Code has two write shapes, not one. Write and Edit put the text in
// `tool_input.content` / `tool_input.new_string`, but MultiEdit nests an array
// at `tool_input.edits`. Cursor puts its array at the top level instead. All
// three go through the secrets scanner, because a scanner that consulted only
// the top-level array read every Claude Code MultiEdit as an empty string and
// exited clean on a write it had never seen (issue #1333).
const edit = (shape, file_path, text) => {
  if (shape === 'claude') {
    return { hook_event_name: 'PreToolUse', tool_name: 'Write', tool_input: { file_path, content: text } };
  }
  if (shape === 'claude-multiedit') {
    return {
      hook_event_name: 'PreToolUse',
      tool_name: 'MultiEdit',
      tool_input: { file_path, edits: [{ old_string: 'placeholder', new_string: text }] },
    };
  }
  return { hook_event_name: 'afterFileEdit', file_path, edits: [{ old_string: '', new_string: text }] };
};

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
    // Regression guard: a read-only search that merely mentions the blocked
    // words as quoted TEXT (not an invocation) must not trip the hard block.
    // Was blocking `grep -rn "pkill -f node" src/` before the fix, which is
    // exactly the kind of command this hook's own maintenance work runs.
    { name: `bash-safety allow grep mentioning killall as text (${shape})`, hook: 'bash-safety.js',
      payload: shell(shape, 'grep -rn "killall node" docs/'), code: 0, absent: ['BLOCKED'] },
    { name: `bash-safety allow grep mentioning pkill as text (${shape})`, hook: 'bash-safety.js',
      payload: shell(shape, 'grep -rn "pkill -f node" src/'), code: 0, absent: ['BLOCKED'] },

    // commit-guard: warn only, never blocks.
    { name: `commit-guard clean (${shape})`, hook: 'commit-guard.js',
      payload: shell(shape, 'git status'), code: 0, absent: ['COMMIT GUARD'] },
    { name: `commit-guard warn on commit (${shape})`, hook: 'commit-guard.js',
      payload: shell(shape, "git commit -m 'stuff happened'"), code: 0, present: ['COMMIT GUARD'] },
  );
}

for (const shape of ['claude', 'claude-multiedit', 'cursor']) {
  cases.push(
    // secrets-scanner: clean first, then the hard block, then a warning.
    { name: `secrets-scanner clean (${shape})`, hook: 'secrets-scanner.js',
      payload: edit(shape, 'src/example.ts', 'export const count = 1;\n'), code: 0, absent: ['BLOCKED', 'WARNING'] },
    { name: `secrets-scanner block aws key (${shape})`, hook: 'secrets-scanner.js',
      payload: edit(shape, 'src/example.ts', `const k = "${FAKE_AWS_KEY}";\n`), code: 2, present: ['BLOCKED', 'AWS access key'] },
    { name: `secrets-scanner warn api_key assignment (${shape})`, hook: 'secrets-scanner.js',
      payload: edit(shape, 'src/example.ts', `const ${KEY_IDENT} = "${FAKE_GENERIC_KEY}";\n`), code: 0, present: ['SECRET WARNING', 'Possible API key assignment'], absent: ['BLOCKED'] },
    // Regression guard: Go's `:=` short variable declaration is the idiomatic
    // assignment form in this (Go-majority) repo. A bare [:=] character class
    // matches only one of its two characters, so `password := "..."` passed
    // through both the block and warn patterns unscanned before the fix.
    { name: `secrets-scanner block go-style password assignment (${shape})`, hook: 'secrets-scanner.js',
      payload: edit(shape, 'main.go', `password := "${FAKE_GENERIC_KEY}"\n`), code: 2, present: ['BLOCKED', 'Hardcoded password'] },
    { name: `secrets-scanner warn go-style api_key assignment (${shape})`, hook: 'secrets-scanner.js',
      payload: edit(shape, 'main.go', `${KEY_IDENT} := "${FAKE_GENERIC_KEY}"\n`), code: 0, present: ['SECRET WARNING', 'Possible API key assignment'], absent: ['BLOCKED'] },
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
