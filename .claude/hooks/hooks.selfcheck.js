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
// decision-citation-check.js resolves .wolf/decisions.md relative to
// CLAUDE_PROJECT_DIR or the edited file's directory, so the guards are run
// from the repo root with that variable pinned. Without this the result
// depends on the directory the self-check happened to be invoked from.
const REPO_ROOT = path.resolve(HOOKS_DIR, '..', '..');

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

// Real ids pulled from the live ledger: one that exists, and ones whose entry
// opens with a dead-status marker. Reading them instead of hardcoding them
// keeps the decision-citation cases correct as the ledger grows. All are null
// on a checkout with no ledger, and the cases that need them are skipped
// rather than failing.
const LEDGER_LINES = (() => {
  try {
    return require('fs')
      .readFileSync(path.join(REPO_ROOT, '.wolf', 'decisions.md'), 'utf8')
      .split('\n')
      .map(l => /^-\s*(D-\d{3})\s*\|(.*)$/.exec(l))
      .filter(Boolean);
  } catch (e) {
    return [];
  }
})();
const DEAD_WORDS = /\b(REVOKED|SUPERSEDED|AMENDED|RETIRED|MOOT)\b/;
const LIVE_DECISION_ID = (LEDGER_LINES.find(m => !DEAD_WORDS.test(m[2].slice(0, 80))) || [])[1] || null;
const REVOKED_DECISION_ID = (LEDGER_LINES.find(m => /\bREVOKED\b/.test(m[2].slice(0, 80))) || [])[1] || null;
// This ledger also retires entries with RETIRED (D-028) and MOOT (D-029).
// Both were invisible to the guard's warn arm until the vocabulary grew.
const OTHERWISE_DEAD_ID = (LEDGER_LINES.find(m => /\b(RETIRED|MOOT)\b/.test(m[2].slice(0, 80))) || [])[1] || null;

function run(hook, payload) {
  const res = spawnSync(process.execPath, [path.join(HOOKS_DIR, hook)], {
    input: JSON.stringify(payload),
    encoding: 'utf8',
    cwd: REPO_ROOT,
    env: { ...process.env, CLAUDE_PROJECT_DIR: REPO_ROOT },
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

// decision-citation-check: a real citation passes, a fabricated one is
// blocked. The ids the cases use are read out of the ledger at runtime rather
// than hardcoded, so appending to .wolf/decisions.md can never turn this
// self-check red on its own. This runs over all three write shapes, including
// claude-multiedit, because the guard read only the top-level `edits` array
// and so exited clean on every Claude Code MultiEdit, which is the same
// defect the secrets scanner had in #1333.
for (const shape of ['claude', 'claude-multiedit', 'cursor']) {
  if (LIVE_DECISION_ID) {
    // Clean first, so a block below can never be a pre-existing failure in
    // disguise.
    cases.push({ name: `decision-citation clean (${shape})`, hook: 'decision-citation-check.js',
      payload: edit(shape, 'docs/note.md', `Per ${LIVE_DECISION_ID}, the ledger is authoritative.\n`),
      code: 0, absent: ['BLOCKED', 'CITATION WARNING'] });
  }
  cases.push(
    { name: `decision-citation block fabricated id (${shape})`, hook: 'decision-citation-check.js',
      payload: edit(shape, 'docs/note.md', 'Per D-999, we already ruled on this.\n'),
      code: 2, present: ['BLOCKED', 'D-999', 'highest id visible here'] },
    // Case is normalised before the ledger lookup, so lowercasing an id is
    // not a way around the block.
    { name: `decision-citation blocks a lowercase fabricated id (${shape})`, hook: 'decision-citation-check.js',
      payload: edit(shape, 'docs/note.md', 'Per d-999, we already ruled on this.\n'),
      code: 2, present: ['BLOCKED', 'D-999'] },
    { name: `decision-citation warn unrecorded owner ruling (${shape})`, hook: 'decision-citation-check.js',
      payload: edit(shape, 'docs/note.md', 'This follows an owner ruling on 1999-01-01.\n'),
      code: 0, present: ['CITATION WARNING', '1999-01-01'], absent: ['BLOCKED'] },
    // The ledger is where new ids are minted, so a not-yet-known id in that
    // file is the normal case, not a fabrication.
    { name: `decision-citation skips the ledger itself (${shape})`, hook: 'decision-citation-check.js',
      payload: edit(shape, '.wolf/decisions.md', '- D-999 | a brand new decision | owner | 2026-08-28\n'),
      code: 0, absent: ['BLOCKED'] },
    // Recording a fabricated id is mandatory work here (the buglog entry after
    // every fixed bug), so the documented marker has to let that text through.
    { name: `decision-citation honours the bypass marker (${shape})`, hook: 'decision-citation-check.js',
      payload: edit(shape, 'docs/note.md', 'citation-check: allow-unknown-ids\nThe agent cited D-999, which never existed.\n'),
      code: 0, present: ['Citation audit skipped'], absent: ['BLOCKED'] },
    // A hyphen is a word boundary, so a bare \b read the middle of an
    // identifier as a citation and blocked on it.
    { name: `decision-citation ignores an id inside an identifier (${shape})`, hook: 'decision-citation-check.js',
      payload: edit(shape, 'docs/note.md', 'The fixture is named part-D-999-abc in the harness.\n'),
      code: 0, absent: ['BLOCKED'] },
  );
  if (REVOKED_DECISION_ID) {
    cases.push({ name: `decision-citation warn revoked id (${shape})`, hook: 'decision-citation-check.js',
      payload: edit(shape, 'docs/note.md', `Still bound by ${REVOKED_DECISION_ID}.\n`),
      code: 0, present: ['CITATION WARNING', REVOKED_DECISION_ID], absent: ['BLOCKED'] });
  }
  if (OTHERWISE_DEAD_ID) {
    cases.push({ name: `decision-citation warn retired/moot id (${shape})`, hook: 'decision-citation-check.js',
      payload: edit(shape, 'docs/note.md', `Still bound by ${OTHERWISE_DEAD_ID}.\n`),
      code: 0, present: ['CITATION WARNING', OTHERWISE_DEAD_ID], absent: ['BLOCKED'] });
  }
}

// Two payload shapes that no harness is documented to send, pinned anyway
// because this guard fails silently when it fails at all: it exits 0 with no
// output, which is byte-identical to a clean scan. Both were raised in the
// pre-merge review of issue #1333.
cases.push(
  // A payload carrying BOTH a content field and an edits array. Under a
  // first-truthy-wins fallback chain the array is never reached, so a secret
  // in the edits hides behind an innocuous content field.
  { name: 'secrets-scanner scans edits even when content is also present', hook: 'secrets-scanner.js',
    payload: {
      hook_event_name: 'PreToolUse',
      tool_name: 'MultiEdit',
      tool_input: {
        file_path: 'src/example.ts',
        content: 'export const count = 1;\n',
        edits: [{ old_string: 'placeholder', new_string: `const k = "${FAKE_AWS_KEY}";\n` }],
      },
    },
    code: 2, present: ['BLOCKED', 'AWS access key'] },

  // A new_string that is not a string. Default coercion turns an object into
  // "[object Object]", which hides whatever it holds from every pattern.
  { name: 'secrets-scanner scans a non-string new_string', hook: 'secrets-scanner.js',
    payload: {
      hook_event_name: 'PreToolUse',
      tool_name: 'MultiEdit',
      tool_input: {
        file_path: 'src/example.ts',
        edits: [{ old_string: 'placeholder', new_string: { key: FAKE_AWS_KEY } }],
      },
    },
    code: 2, present: ['BLOCKED', 'AWS access key'] },
);

// The same three shapes against decision-citation-check, which resolves its
// content the same way and would fail the same way. The third pins the case
// the single-edit helper above cannot reach: a fabrication in an edit that is
// not the first one in the array.
cases.push(
  { name: 'decision-citation scans edits even when content is also present', hook: 'decision-citation-check.js',
    payload: {
      hook_event_name: 'PreToolUse',
      tool_name: 'MultiEdit',
      tool_input: {
        file_path: 'docs/note.md',
        content: 'A line with no citation in it.\n',
        edits: [{ old_string: 'placeholder', new_string: 'Per D-999, we already ruled on this.\n' }],
      },
    },
    code: 2, present: ['BLOCKED', 'D-999'] },

  { name: 'decision-citation scans a non-string new_string', hook: 'decision-citation-check.js',
    payload: {
      hook_event_name: 'PreToolUse',
      tool_name: 'MultiEdit',
      tool_input: {
        file_path: 'docs/note.md',
        edits: [{ old_string: 'placeholder', new_string: { note: 'Per D-999, we already ruled on this.' } }],
      },
    },
    code: 2, present: ['BLOCKED', 'D-999'] },

  { name: 'decision-citation scans every edit, not just the first', hook: 'decision-citation-check.js',
    payload: {
      hook_event_name: 'PreToolUse',
      tool_name: 'MultiEdit',
      tool_input: {
        file_path: 'docs/note.md',
        edits: [
          { old_string: 'a', new_string: 'An edit with no citation in it.\n' },
          { old_string: 'b', new_string: 'Per D-999, we already ruled on this.\n' },
        ],
      },
    },
    code: 2, present: ['BLOCKED', 'D-999'] },
);

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
