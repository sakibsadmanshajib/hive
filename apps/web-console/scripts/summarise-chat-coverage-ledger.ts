// Recomputes a recorded ledger's summary block with the gate's own summarise().
//
// A ledger holds every per-control result, so its summary is a pure function
// of what is already in the file. That matters because the identity figure
// (distinct control identities, rather than raw instances that move with how
// many chat rows the account happens to hold) was added after the recorded run
// was taken, and the honest way to state it for that run is to derive it with
// the same code the gate uses, in the open, rather than to quote a number
// somebody worked out by hand.
//
// Usage, from apps/web-console:
//   npx vite-node scripts/summarise-chat-coverage-ledger.ts -- <ledger.json> [--write]
//
// Without --write it prints the recomputed summary and changes nothing.
import { readFileSync, writeFileSync } from "node:fs";
import { resolve } from "node:path";

import { summarise, type Result } from "../e2e/chat-coverage/lib";

function readResults(value: unknown): Result[] {
  if (typeof value !== "object" || value === null) throw new Error("ledger is not an object");
  const results = new Map(Object.entries(value)).get("results");
  if (!Array.isArray(results)) throw new Error("ledger carries no results array");
  return results.map((entry, index) => {
    if (typeof entry !== "object" || entry === null) {
      throw new Error(`result ${index} is not an object`);
    }
    const field = new Map(Object.entries(entry));
    const proof = field.get("proof");
    return {
      key: String(field.get("key") ?? ""),
      surface: String(field.get("surface") ?? ""),
      name: String(field.get("name") ?? ""),
      role: String(field.get("role") ?? ""),
      proven: field.get("proven") === true,
      proof: proof === "not-fired" ? "not-fired" : proof === "none" ? "none" : toProof(proof),
      detail: String(field.get("detail") ?? ""),
    };
  });
}

const PROOFS = [
  "navigate",
  "network",
  "dom",
  "overlay",
  "download",
  "filechooser",
  "value",
  "persisted",
  "disabled-with-reason",
] as const;

function toProof(value: unknown): Result["proof"] {
  const match = PROOFS.find((name) => name === value);
  return match ?? "none";
}

const args = process.argv.slice(2).filter((a) => a !== "--");
const file = resolve(args.find((a) => !a.startsWith("--")) ?? "");
const write = args.includes("--write");

const ledger: unknown = JSON.parse(readFileSync(file, "utf8"));
const summary = summarise(readResults(ledger));
// eslint-disable-next-line no-console
console.log(
  `${summary.identitiesProven}/${summary.identities} identities ` +
    `(${(summary.identityRatio * 100).toFixed(1)}%), ` +
    `${summary.proven}/${summary.total} instances ` +
    `(${(summary.ratio * 100).toFixed(1)}%), ${summary.deferred} not fired`,
);

if (write) {
  if (typeof ledger !== "object" || ledger === null) throw new Error("ledger is not an object");
  const next = { ...ledger, summary };
  writeFileSync(file, JSON.stringify(next, null, 2) + "\n");
  // eslint-disable-next-line no-console
  console.log(`wrote the recomputed summary into ${file}`);
}
