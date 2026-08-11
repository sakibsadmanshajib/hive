#!/usr/bin/env node
// Raises or lowers the chat-coverage surface floors, from a recorded ledger.
//
// WHY THIS IS A SEPARATE PROGRAM
// ------------------------------
// The sweep used to do this itself: COV_FLOORS=update rewrote surface-floors.json
// from the run in progress AND skipped checking the floors in that same pass.
// A bar that the run being measured can move is not a bar. It shipped exactly
// the failure it was built to prevent: three surfaces enumerated fewer controls
// than the previous run had (Interface 48 against 56, General 17 against 16,
// Audio 7 against 8), and the update wrote the smaller numbers in as the new
// baseline with no reason recorded anywhere.
//
// So updating a floor is now a deliberate act with a diff to review: run the
// sweep, look at the ledger it wrote, then run this against that ledger and
// commit the result with the reason in the commit message.
//
// A floor only ever RISES automatically. Lowering one means a surface renders
// less than it used to, which is either a regression to fix or a deliberate
// removal to justify, and both deserve a human typing --allow-lower and saying
// why in the commit message.
//
// Usage:
//   node scripts/update-chat-coverage-floors.mjs [ledger.json] [--allow-lower] [--dry-run]
//
// The default ledger is the one the last local sweep wrote:
//   apps/web-console/chat-coverage-report/coverage.run.json

import { readFileSync, writeFileSync } from "node:fs";
import { dirname, resolve, join } from "node:path";
import { fileURLToPath } from "node:url";

const HERE = dirname(fileURLToPath(import.meta.url));
const WEB_CONSOLE = resolve(HERE, "..");
const FLOOR_FILE = join(WEB_CONSOLE, "e2e/chat-coverage/surface-floors.json");
const DEFAULT_LEDGER = join(WEB_CONSOLE, "chat-coverage-report/coverage.run.json");

function parseArgs(argv) {
  const flags = new Set(argv.filter((a) => a.startsWith("--")));
  const positional = argv.filter((a) => !a.startsWith("--"));
  return {
    ledger: positional[0] ? resolve(positional[0]) : DEFAULT_LEDGER,
    allowLower: flags.has("--allow-lower"),
    dryRun: flags.has("--dry-run"),
  };
}

function readJson(file) {
  return JSON.parse(readFileSync(file, "utf8"));
}

function main(argv) {
  const { ledger, allowLower, dryRun } = parseArgs(argv);
  const run = readJson(ledger);

  if (!Array.isArray(run.swept)) {
    console.error(
      `${ledger} carries no \`swept\` array, so there is nothing to read a floor from. ` +
        "Only a ledger written by the current sweep records what each surface enumerated.",
    );
    process.exit(1);
  }
  if (run.partial) {
    console.error(
      `${ledger} is a partial run (surfaceFilter=${String(run.surfaceFilter)}). Floors are ` +
        "only updated from a full sweep, or the surfaces the slice skipped would keep " +
        "whatever stale number they carry while the rest move.",
    );
    process.exit(1);
  }
  if ((run.surfaceErrors ?? []).length > 0) {
    console.error(
      `${ledger} recorded ${run.surfaceErrors.length} surface error(s). A run that could not ` +
        "sweep everything is not evidence of how much a surface renders:\n  " +
        run.surfaceErrors.join("\n  "),
    );
    process.exit(1);
  }

  const floors = readJson(FLOOR_FILE);
  const next = { ...floors.surfaces };
  const raised = [];
  const lowered = [];
  const added = [];

  for (const entry of run.swept) {
    if (!entry || typeof entry.surface !== "string") continue;
    if (!Number.isInteger(entry.enumerated) || entry.enumerated <= 0) {
      // A surface that enumerated nothing measured nothing. A floor of zero is
      // a bar nothing can fail.
      continue;
    }
    const current = next[entry.surface];
    if (current === undefined) {
      added.push(`${entry.surface}: ${entry.enumerated}`);
      next[entry.surface] = entry.enumerated;
    } else if (entry.enumerated > current) {
      raised.push(`${entry.surface}: ${current} -> ${entry.enumerated}`);
      next[entry.surface] = entry.enumerated;
    } else if (entry.enumerated < current) {
      lowered.push(`${entry.surface}: ${current} -> ${entry.enumerated}`);
      if (allowLower) next[entry.surface] = entry.enumerated;
    }
  }

  if (lowered.length > 0 && !allowLower) {
    console.error(
      "refusing to lower a floor without --allow-lower. Each of these means the surface " +
        "renders fewer controls than it used to, which shrinks the denominator and inflates " +
        "coverage:\n  " +
        lowered.join("\n  ") +
        "\n\nFix the surface, or re-run with --allow-lower and say why in the commit message.",
    );
    process.exit(1);
  }

  const ordered = {};
  for (const key of Object.keys(next).sort()) ordered[key] = next[key];
  const body = JSON.stringify({ $comment: floors.$comment, surfaces: ordered }, null, 2) + "\n";

  for (const line of added) console.log(`ADDED    ${line}`);
  for (const line of raised) console.log(`RAISED   ${line}`);
  for (const line of lowered) console.log(`LOWERED  ${line}`);
  if (added.length + raised.length + lowered.length === 0) {
    console.log("no change: every swept surface matches its floor exactly.");
  }

  if (dryRun) {
    console.log("--dry-run: surface-floors.json not written.");
    return;
  }
  writeFileSync(FLOOR_FILE, body);
  console.log(`wrote ${FLOOR_FILE}. Commit it with the reason for each line above.`);
}

main(process.argv.slice(2));
