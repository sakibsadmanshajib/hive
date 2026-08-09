#!/usr/bin/env node
// Guard for issue #813, camouflage shape 14 in docs/TESTING-STANDARD.md.
//
// A Playwright spec that no workflow names emits no signal at all. It does not
// skip, there is no environment variable to blame, and `npx playwright test`
// locally runs it, so the hole is invisible to anyone working on the specs.
// Seven spec files had never run in CI when this guard was written, including
// the one written to enforce what was at the time a regulatory constraint.
//
// This guard fails when a spec file exists that no file under .github/ names.
// Deliberate exceptions live in dark-spec-allowlist.json and are debt, not
// decisions: each one needs a reason, an owner and an open tracking issue, and
// the guard fails on an entry that has gone stale, so the list cannot rot into
// a permanent excuse.
//
// Usage: node scripts/verify-spec-wiring.mjs   (run from apps/web-console)

import { readFileSync, readdirSync, statSync, existsSync } from "node:fs";
import { join, relative, basename, dirname } from "node:path";
import { fileURLToPath } from "node:url";

const appDir = dirname(dirname(fileURLToPath(import.meta.url)));
const repoRoot = dirname(dirname(appDir));
const githubDir = join(repoRoot, ".github");
const allowlistPath = join(appDir, "tests", "dark-spec-allowlist.json");

// Directories holding Playwright specs. Both are walked recursively, so a spec
// added in a new subdirectory is covered without editing this list.
const specRoots = [join(appDir, "tests", "e2e"), join(appDir, "e2e")];

function walk(dir, out = []) {
	if (!existsSync(dir)) return out;
	for (const entry of readdirSync(dir)) {
		const full = join(dir, entry);
		if (statSync(full).isDirectory()) walk(full, out);
		else out.push(full);
	}
	return out;
}

const specFiles = specRoots
	.flatMap((root) => walk(root))
	.filter((f) => f.endsWith(".spec.ts"))
	.map((f) => relative(appDir, f).split("\\").join("/"))
	.sort();

if (specFiles.length === 0) {
	// A walk that finds nothing would report perfect wiring. That is the same
	// zero-collection failure the phase-19 guard exists to catch.
	console.error("spec wiring guard FAILED: enumerated zero spec files, the walk is broken");
	process.exit(1);
}

// Everything under .github/ concatenated. A spec counts as wired when some
// workflow names its path or its basename: the web-e2e job invokes Playwright
// by explicit file path, and other jobs invoke directories.
const githubText = walk(githubDir)
	.map((f) => readFileSync(f, "utf8"))
	.join("\n");

const isWired = (spec) =>
	githubText.includes(spec) || githubText.includes(basename(spec));

const allowlist = existsSync(allowlistPath)
	? JSON.parse(readFileSync(allowlistPath, "utf8"))
	: { entries: [] };
const entries = allowlist.entries ?? [];
const failures = [];

// Validate the allowlist before trusting it. An entry with an empty reason or
// no owner is a silent skip wearing a justification, which is the shape this
// guard exists to prevent.
const allowed = new Set();
for (const entry of entries) {
	const where = entry.spec ?? "(entry with no spec field)";
	if (!entry.spec || !specFiles.includes(entry.spec)) {
		failures.push(`allowlist entry names a spec that does not exist: ${where}`);
		continue;
	}
	if (!entry.reason || entry.reason.trim().length < 25) {
		failures.push(`allowlist entry needs a reason of at least 25 characters: ${where}`);
	}
	if (!entry.owner) {
		failures.push(`allowlist entry needs an owner: ${where}`);
	}
	if (!Number.isInteger(entry.issue)) {
		failures.push(`allowlist entry needs a tracking issue number: ${where}`);
	}
	if (isWired(entry.spec)) {
		failures.push(`allowlist entry is stale, this spec is wired now, remove it: ${where}`);
	}
	allowed.add(entry.spec);
}

const dark = specFiles.filter((s) => !isWired(s) && !allowed.has(s));
for (const spec of dark) {
	failures.push(`spec file is named by no workflow, so it has never run: ${spec}`);
}

if (failures.length > 0) {
	console.error("spec wiring guard FAILED");
	for (const f of failures) console.error(`  ${f}`);
	console.error("");
	console.error("Wire the spec into a workflow, or add a justified entry to");
	console.error("  apps/web-console/tests/dark-spec-allowlist.json");
	console.error("Allowlist entries are debt and need a reason, an owner and an issue.");
	process.exit(1);
}

const wiredCount = specFiles.length - allowed.size;
console.log(
	`spec wiring guard: OK, ${wiredCount}/${specFiles.length} spec files are named by a workflow` +
		(allowed.size > 0 ? `, ${allowed.size} allowlisted as known debt` : ""),
);
