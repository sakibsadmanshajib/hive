#!/usr/bin/env node
// PreToolUse hook: refuses a write that cites a decision id which is not in
// `.wolf/decisions.md`.
//
// Why this exists. Semantic memory in this repo is authoritative
// (`.claude/skills/memory-layers.md`), and agents cite it constantly, but
// nothing ever checked a citation. On 2026-07-30 an agent killed at its
// session limit left behind two retention migrations whose 14 day window was
// justified by citing a `.wolf/decisions.md` D-030 that did not exist then.
// The resuming agent grepped, found nothing, and replaced the rationale; a
// person looking is the only reason it was caught (native memory:
// `feedback_killed_agent_work_verification`). D-030 has since been minted for
// an unrelated decision, which is precisely why a fabricated id is so hard to
// spot after the fact: grep the same string today and it resolves. A written
// rule saying "verify your citations" was already in force and did not stop
// it, so this is the mechanical version: the check runs on the write itself,
// whether or not the agent read the rule.
//
// What this actually stops, stated plainly, because the fabrications this repo
// has seen are not equally catchable and a reader will otherwise assume all of
// them are covered:
//
// - A `D-NNN` with no ledger entry, inside a Write, Edit or MultiEdit:
//   BLOCKED. That is the 2026-07-30 case, judged against the ledger state of
//   the day it happens.
// - An "owner ruling <date>" naming a date that appears nowhere in the ledger:
//   WARNS, never blocks, deliberately. The ruling may be real and merely
//   unrecorded, which is a reason to record it, not a reason to refuse a write.
// - A dispatch brief that misstates the ledger (claiming it tops out ten
//   entries below where it does, say): OUT OF SCOPE entirely. A brief is an
//   Agent prompt, not a file write, so a PreToolUse hook on Write, Edit and
//   MultiEdit never sees it. Nothing here fires on that class at all.
//
// SKIPS `.wolf/decisions.md` itself (where new ids are minted) and
// `.claude/hooks/` (this guard and its self-check carry deliberate fake ids as
// fixtures). Both entry points below apply the same skip list.
//
// BYPASS: content carrying the literal `citation-check: allow-unknown-ids` is
// not audited. Recording a fabricated id is mandatory work here (a buglog
// entry after every fixed bug, `.claude/rules/openwolf.md`), and a post-mortem
// or review analysing one has the same need, so the guard has to leave a
// deliberate, greppable way to write the id down. Without one the reachable
// workaround is a Bash heredoc, which skips every write-path guard and teaches
// the routing-around this gate exists to stop. The marker also covers a real
// entry that landed on main after this checkout was cut and cannot be
// refreshed in place.
//
// Staleness. Every builder here works in its own worktree, carrying whatever
// `.wolf/decisions.md` its branch point had, so a correct citation of an entry
// minted since can look fabricated. The ledger is therefore merged from every
// `.wolf/decisions.md` on the path from the edited file up to the filesystem
// root, which picks up the canonical checkout's fresher copy whenever the
// worktree sits under it (`<repo>/.claude/worktrees/<name>`, the layout this
// repo uses). What that still cannot reach is handled by wording: a refusal
// never claims to know the highest id that exists, only the highest it can
// see, and it says to refresh from main rather than substitute a lower id that
// happens to exist. Substitution is what a confident wrong number invites, and
// it produces exactly the fabrication this guard exists to stop.
//
// Also runnable directly, for text a Write never passes through (a PR body,
// an existing file, a report):
//
//     node .claude/hooks/decision-citation-check.js --check FILE...
//     ... | node .claude/hooks/decision-citation-check.js --check
//
// Exit 2 means at least one citation is unverifiable. The parser and the skip
// list live here once and both entry points call them, so the CLI and the gate
// cannot disagree about what counts as a valid citation or an exempt file.

const fs = require('fs');
const path = require('path');

// The ledger's format is `- D-NNN | decision | source | date` (see the header
// of .wolf/decisions.md). Ids are always three digits there, so requiring
// three digits is what keeps stray matches like a "D-1" in prose out.
const LEDGER_ENTRY = /^-\s*(D-\d{3})\s*\|(.*)$/;
// A bare \b treats a hyphen as a word boundary, so an identifier like
// `part-D-123-abc` would read as a citation and be blocked. Excluding word
// characters and hyphens on both sides keeps the match to a standalone id.
// The `d` is accepted in either case and normalised to upper before the ledger
// lookup, so a lowercase `d-030` cannot slip a fabricated id past the check.
// Measured before allowing it: the repo contains no lowercase `d-NNN` token at
// all, so this adds no new class of false refusal.
const CITATION = /(?<![\w-])[Dd]-\d{3}(?![\w-])/g;
const OWNER_RULING = /owner\s+(?:ruling|decision|ruled|decided)[^.\n]{0,40}?(\d{4}-\d{2}-\d{2})/gi;
// A ledger line marks its own entry dead in the first stretch after the pipe
// (`- D-013 | REVOKED 2026-08-11 by D-036`). Later occurrences describe some
// OTHER thing the entry superseded, which is not a reason to warn about
// citing this entry, so only the opening stretch is examined.
const STATUS_WINDOW = 80;
// Five words, not three: this ledger also retires an entry with RETIRED
// (D-028) and MOOT (D-029), both of which are as final as REVOKED and neither
// of which produced a warning before.
const DEAD_STATUS = /\b(REVOKED|SUPERSEDED|AMENDED|RETIRED|MOOT)\b/;
const BYPASS_MARKER = 'citation-check: allow-unknown-ids';

// Paths where an unknown id is the normal case rather than a fabrication.
// Both entry points call this, so the CLI cannot block on a file the gate
// exempts.
function isExemptPath(filePath) {
  const normalized = String(filePath || '').split(path.sep).join('/');
  // The ledger is where ids are minted, so a new id there is by definition
  // not yet in the ledger being read.
  if (normalized.endsWith('.wolf/decisions.md')) return true;
  // This guard and its self-check carry fake ids as fixtures.
  return normalized.includes('.claude/hooks/');
}

// Every ledger from startDir up to the filesystem root, nearest first.
function findLedgers(startDir) {
  const found = [];
  let dir = path.resolve(startDir);
  for (;;) {
    const candidate = path.join(dir, '.wolf', 'decisions.md');
    if (fs.existsSync(candidate)) found.push(candidate);
    const parent = path.dirname(dir);
    if (parent === dir) return found;
    dir = parent;
  }
}

// Merged ledger: { entries: Map<id, text-after-the-pipe>, text, sources }.
// The nearest ledger wins on a conflict, because an entry amended on this
// branch describes this branch's state better than an outer checkout's copy.
function loadLedger(startDir) {
  const entries = new Map();
  const sources = [];
  let text = '';
  for (const p of findLedgers(startDir)) {
    let raw;
    try {
      raw = fs.readFileSync(p, 'utf8');
    } catch (e) {
      continue;
    }
    sources.push(p);
    // The newline matters: without it a ledger with no trailing newline fuses
    // its last line onto the next ledger's first, and `text` is what the
    // owner-ruling date lookup searches.
    text += raw + '\n';
    for (const line of raw.split('\n')) {
      const m = LEDGER_ENTRY.exec(line);
      if (m && !entries.has(m[1])) entries.set(m[1], m[2]);
    }
  }
  return { entries, text, sources };
}

function highestId(entries) {
  let max = 0;
  for (const id of entries.keys()) max = Math.max(max, Number(id.slice(2)));
  return max ? `D-${String(max).padStart(3, '0')}` : '(none)';
}

// Returns { missing: [id], dead: [{id, status}], unrecordedRulings: [date] }.
function auditCitations(text, ledger) {
  const missing = new Set();
  const dead = [];
  const seenDead = new Set();

  for (const raw of text.match(CITATION) || []) {
    const cited = raw.toUpperCase();
    if (!ledger.entries.has(cited)) {
      missing.add(cited);
      continue;
    }
    if (seenDead.has(cited)) continue;
    const status = DEAD_STATUS.exec(ledger.entries.get(cited).slice(0, STATUS_WINDOW));
    if (status) {
      dead.push({ id: cited, status: status[1] });
      seenDead.add(cited);
    }
  }

  const unrecordedRulings = new Set();
  let m;
  OWNER_RULING.lastIndex = 0;
  while ((m = OWNER_RULING.exec(text)) !== null) {
    if (!ledger.text.includes(m[1])) unrecordedRulings.add(m[1]);
  }

  return { missing: [...missing], dead, unrecordedRulings: [...unrecordedRulings] };
}

// Emits the human-facing lines. Returns the exit code.
function report(label, audit, entries) {
  for (const { id, status } of audit.dead) {
    console.log(
      `CITATION WARNING in ${label}: ${id} is marked ${status} in .wolf/decisions.md. ` +
      `Read its entry before relying on it; a retired decision is history, not a current rule.`
    );
  }
  for (const date of audit.unrecordedRulings) {
    console.log(
      `CITATION WARNING in ${label}: cites an owner ruling dated ${date}, but that date ` +
      `appears nowhere in .wolf/decisions.md. Record the ruling as a new decision first, ` +
      `or cite what the owner actually said instead of a ledger entry that does not exist.`
    );
  }
  if (audit.missing.length === 0) return 0;
  console.log(
    `BLOCKED: ${label} cites ${audit.missing.join(', ')}, which ${audit.missing.length > 1 ? 'are' : 'is'} ` +
    `not in any .wolf/decisions.md this checkout can see (highest id visible here: ${highestId(entries)}, ` +
    `which is not necessarily the highest that exists). ` +
    `If the entry is real and landed on main after this checkout was cut, refresh .wolf/decisions.md ` +
    `from main and re-read the entry. Do NOT substitute a lower id that happens to exist: that is the ` +
    `fabrication this check exists to stop. If you are deliberately writing about an id that does not ` +
    `exist here, such as the buglog entry or post-mortem recording a fabricated one, put the literal ` +
    `"${BYPASS_MARKER}" in the text. Fabricated decision citations have already shipped in this repo.`
  );
  return 2;
}

function runCli(argv) {
  const files = argv.slice(argv.indexOf('--check') + 1);
  const ledger = loadLedger(process.cwd());
  if (ledger.entries.size === 0) {
    console.log('No .wolf/decisions.md found; nothing to check against.');
    process.exit(0);
  }

  const readStdin = () => new Promise(resolve => {
    let buf = '';
    process.stdin.setEncoding('utf8');
    process.stdin.on('data', c => buf += c);
    process.stdin.on('end', () => resolve(buf));
  });

  const check = (label, text) => {
    if (text.includes(BYPASS_MARKER)) {
      console.log(`Skipped ${label}: carries the ${BYPASS_MARKER} marker.`);
      return 0;
    }
    return report(label, auditCitations(text, ledger), ledger.entries);
  };

  if (files.length === 0) {
    readStdin().then(text => process.exit(check('stdin', text)));
    return;
  }
  let worst = 0;
  for (const f of files) {
    if (isExemptPath(f)) {
      console.log(`Skipped ${f}: exempt path (the ledger itself, or a hooks fixture).`);
      continue;
    }
    let text;
    try {
      text = fs.readFileSync(f, 'utf8');
    } catch (e) {
      // A path that cannot be read is a usage error, not a crash, and not a
      // pass either: reporting it green would be the quiet absence this repo
      // keeps getting burned by.
      console.log(`Cannot read ${f}: ${e.message}`);
      worst = 2;
      continue;
    }
    worst = Math.max(worst, check(f, text));
  }
  console.log(
    `Checked ${files.length} file(s) against ${ledger.sources.length} ledger(s) ` +
    `(highest id visible: ${highestId(ledger.entries)}).`
  );
  process.exit(worst);
}

function runHook() {
  let input = '';
  const stdinTimeout = setTimeout(() => process.exit(0), 5000);
  process.stdin.setEncoding('utf8');
  process.stdin.on('data', chunk => input += chunk);
  process.stdin.on('end', () => {
    clearTimeout(stdinTimeout);
    try {
      const data = JSON.parse(input);
      const ti = data.tool_input || {};
      const filePath = ti.file_path || data.file_path || '';
      // Payload handling is deliberately identical to secrets-scanner.js,
      // which had this defect first and fixed it in #1333. Claude Code nests a
      // MultiEdit's array at `tool_input.edits`; Cursor's flat file-edit
      // payload carries `edits` at the top level. Reading only the flat shape
      // meant every Claude Code MultiEdit resolved to empty content and exited
      // before auditing a single citation, while settings.json matched
      // MultiEdit and the skill claimed it was covered.
      const editsSource = Array.isArray(ti.edits) ? ti.edits
        : Array.isArray(data.edits) ? data.edits
        : [];
      // Nothing here trusts a payload field to be a string, because the
      // default coercion of an object is "[object Object]", which would hide
      // any citation nested inside it.
      const asText = v => (typeof v === 'string' ? v : v == null ? '' : JSON.stringify(v));
      const editsContent = editsSource.map(e => asText((e || {}).new_string)).join('\n');
      // Every source is audited rather than the first truthy one winning. A
      // fallback chain lets a payload carrying both a content field and an
      // edits array hide the array behind the field.
      const content = [asText(ti.content), asText(ti.new_string), editsContent]
        .filter(Boolean)
        .join('\n');
      if (!content) process.exit(0);
      if (isExemptPath(filePath)) process.exit(0);
      if (content.includes(BYPASS_MARKER)) {
        // Announced rather than silent. A bypass nobody can see in the
        // transcript is indistinguishable from a guard that never ran, and
        // any file documenting the literal (the memory-layers skill, say)
        // trips it too, which a reader should be told rather than left to
        // discover.
        console.log(
          `Citation audit skipped for ${filePath ? path.basename(filePath) : 'this write'}: ` +
          `content carries the ${BYPASS_MARKER} marker.`
        );
        process.exit(0);
      }

      const ledger = loadLedger(
        process.env.CLAUDE_PROJECT_DIR || (filePath ? path.dirname(filePath) : process.cwd())
      );
      if (ledger.entries.size === 0) process.exit(0);

      const label = filePath ? path.basename(filePath) : 'this write';
      process.exit(report(label, auditCitations(content, ledger), ledger.entries));
    } catch (e) {
      // A guard that cannot parse its input must not block the write.
      process.exit(0);
    }
  });
}

if (process.argv.includes('--check')) runCli(process.argv);
else runHook();
