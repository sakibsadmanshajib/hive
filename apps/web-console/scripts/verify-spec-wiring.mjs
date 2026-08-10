#!/usr/bin/env node
// Guard for issue #813, camouflage shape 14 in docs/TESTING-STANDARD.md.
//
// A Playwright spec that no workflow runs emits no signal at all. It does not
// skip, there is no variable to blame, and `npx playwright test` locally runs
// it, so the hole is invisible to anyone working on the specs.
//
// Wiring is resolved by asking Playwright what each workflow invocation
// collects. Never by matching spec filenames against workflow text: that is
// wrong in both directions, because a filename in a COMMENT runs nothing and
// an invocation selecting by --project names no filename at all. Both errors
// were present in the first version of this guard, which reported 6 wired and
// 27 dark where the truth was 15 and 18. See shape 15 in the standard.
//
// The invocations are discovered from the workflows rather than listed here.
// A hardcoded list goes stale the moment somebody wires a spec, which is
// exactly when this guard has to be right.

import { execFileSync } from "node:child_process";
import { readFileSync, readdirSync, statSync, existsSync } from "node:fs";
import { join, relative, dirname } from "node:path";
import { fileURLToPath } from "node:url";

const appDir = dirname(dirname(fileURLToPath(import.meta.url)));
const repoRoot = dirname(dirname(appDir));
const workflowsDir = join(repoRoot, ".github", "workflows");
const allowlistPath = join(appDir, "tests", "dark-spec-allowlist.json");

// The owui config chooses its testMatch from credential env vars and collects
// zero files without them. Placeholders answer the question this guard asks,
// which is whether the workflow runs the spec when it has its secrets.
const collectEnv = {
	...process.env,
	OWUI_E2E_EMAIL: "spec-wiring-guard",
	OWUI_E2E_PASSWORD: "spec-wiring-guard",
	SUPABASE_OAUTH_CLIENT_ID: "spec-wiring-guard",
	SUPABASE_OAUTH_CLIENT_SECRET: "spec-wiring-guard",
};

const failures = [];

function walk(dir, out = []) {
	if (!existsSync(dir)) return out;
	for (const entry of readdirSync(dir)) {
		const full = join(dir, entry);
		if (statSync(full).isDirectory()) walk(full, out);
		else out.push(full);
	}
	return out;
}

const rel = (abs) => relative(appDir, abs).split("\\").join("/");

// Every shell line a workflow runs. Handles both `run: cmd` and `run: |`
// blocks. Comment lines are dropped, which is the whole false-positive half of
// the original bug: a spec named only in a comment is not run by anything.
function workflowShellLines() {
	const lines = [];
	for (const file of walk(workflowsDir).filter((f) => f.endsWith(".yml") || f.endsWith(".yaml"))) {
		const raw = readFileSync(file, "utf8").split("\n");
		for (let i = 0; i < raw.length; i++) {
			const block = /^(\s*)-?\s*run:\s*[|>][-+]?\s*$/.exec(raw[i]);
			if (block) {
				const outer = block[1].length;
				for (let j = i + 1; j < raw.length; j++) {
					if (raw[j].trim() === "") continue;
					const indent = raw[j].length - raw[j].trimStart().length;
					if (indent <= outer) break;
					lines.push(raw[j].trim());
				}
				continue;
			}
			const inline = /^\s*-?\s*run:\s+(\S.*)$/.exec(raw[i]);
			if (inline) lines.push(inline[1].trim());
		}
	}
	return lines.filter((l) => !l.startsWith("#"));
}

const pkgScripts = JSON.parse(readFileSync(join(appDir, "package.json"), "utf8")).scripts ?? {};
// Only scripts that actually start the runner. `e2e:phase-19:verify-collection`
// shells a collection guard, which finds files without running them, so it is
// correctly not an invocation.
const playwrightScripts = new Set(
	Object.keys(pkgScripts).filter((k) => /(^|\s)playwright\s+test(\s|$)/.test(pkgScripts[k])),
);

// Discover every way a workflow starts Playwright, as an argv the guard can
// re-run. Nothing is hardcoded, so wiring a spec is picked up on the next run.
function discoverInvocations() {
	const found = new Map();
	for (const line of workflowShellLines()) {
		const direct = /(?:npx\s+)?playwright\s+test\b(.*)$/.exec(line);
		if (direct && !/playwright\s+(install|install-deps)/.test(line)) {
			const args = direct[1]
				.split(/\s+/)
				.filter(Boolean)
				.filter((a) => !a.startsWith("--reporter") && !/^[|&;>]/.test(a));
			found.set(`playwright test ${args.join(" ")}`, { argv: ["playwright", "test", ...args] });
			continue;
		}
		for (const m of line.matchAll(/npm\s+run\s+([A-Za-z0-9:_.-]+)/g)) {
			if (playwrightScripts.has(m[1])) {
				found.set(`npm run ${m[1]}`, { script: m[1] });
			}
		}
	}
	return [...found.entries()].map(([id, v]) => ({ id, ...v }));
}

// Ask Playwright what this invocation collects. The JSON reporter carries the
// file of every collected spec, relative to config.rootDir, which differs per
// config: the owui config roots at its own directory.
function collect(inv) {
	const listArgs = ["--list", "--reporter=json"];
	const [cmd, argv] = inv.script
		? ["npm", ["run", inv.script, "--", ...listArgs]]
		: ["npx", [...inv.argv, ...listArgs]];

	let out;
	try {
		out = execFileSync(cmd, argv, {
			cwd: appDir,
			env: collectEnv,
			encoding: "utf8",
			maxBuffer: 64 * 1024 * 1024,
			stdio: ["ignore", "pipe", "pipe"],
		});
	} catch {
		failures.push(`invocation failed to list, broken not empty: ${inv.id}`);
		return [];
	}

	const start = out.indexOf("{");
	if (start < 0) {
		failures.push(`invocation produced no JSON report: ${inv.id}`);
		return [];
	}
	const report = JSON.parse(out.slice(start));
	const rootDir = report.config?.rootDir ?? appDir;
	const files = new Set();
	const visit = (suite) => {
		for (const child of suite.suites ?? []) visit(child);
		for (const spec of suite.specs ?? []) if (spec.file) files.add(join(rootDir, spec.file));
	};
	for (const suite of report.suites ?? []) visit(suite);
	return [...files];
}

const specFiles = [join(appDir, "tests", "e2e"), join(appDir, "e2e")]
	.flatMap((root) => walk(root))
	.filter((f) => f.endsWith(".spec.ts"))
	.map(rel)
	.sort();

if (specFiles.length === 0) {
	console.error("spec wiring guard FAILED: enumerated zero spec files, the walk is broken");
	process.exit(1);
}

const invocations = discoverInvocations();
if (invocations.length === 0) {
	console.error("spec wiring guard FAILED: found no Playwright invocation in any workflow");
	process.exit(1);
}

const wired = new Set();
for (const inv of invocations) {
	const collected = collect(inv);
	// Zero collected is the phase-19 testMatch bug. Silence here would report
	// the specs as dark rather than the selector as broken.
	if (collected.length === 0) {
		failures.push(`invocation collected zero spec files, its selector is broken: ${inv.id}`);
		continue;
	}
	for (const f of collected) {
		const r = rel(f);
		if (r.endsWith(".spec.ts")) wired.add(r);
	}
}

const allowlist = existsSync(allowlistPath)
	? JSON.parse(readFileSync(allowlistPath, "utf8"))
	: { entries: [] };

// Validate the allowlist before trusting it. An entry with no reason or owner
// is a silent skip wearing a justification.
const allowed = new Set();
for (const entry of allowlist.entries ?? []) {
	const where = entry.spec ?? "(entry with no spec field)";
	if (!entry.spec || !specFiles.includes(entry.spec)) {
		failures.push(`allowlist entry names a spec that does not exist: ${where}`);
		continue;
	}
	if (!entry.reason || entry.reason.trim().length < 25) {
		failures.push(`allowlist entry needs a reason of at least 25 characters: ${where}`);
	}
	if (!entry.owner) failures.push(`allowlist entry needs an owner: ${where}`);
	if (!Number.isInteger(entry.issue)) {
		failures.push(`allowlist entry needs a tracking issue number: ${where}`);
	}
	if (wired.has(entry.spec)) {
		failures.push(`allowlist entry is stale, this spec runs now, remove it: ${where}`);
	}
	allowed.add(entry.spec);
}

for (const spec of specFiles) {
	if (!wired.has(spec) && !allowed.has(spec)) {
		failures.push(`spec file is run by no workflow, so it has never run: ${spec}`);
	}
}

if (failures.length > 0) {
	console.error("spec wiring guard FAILED");
	for (const f of failures) console.error(`  ${f}`);
	console.error("");
	console.error(`Invocations discovered: ${invocations.map((i) => i.id).join("; ")}`);
	console.error("Run the spec from a workflow, or add a justified entry to");
	console.error("  apps/web-console/tests/dark-spec-allowlist.json");
	process.exit(1);
}

console.log(
	`spec wiring guard: OK, ${wired.size}/${specFiles.length} spec files are run by a workflow` +
		(allowed.size > 0 ? `, ${allowed.size} allowlisted as known debt` : ""),
);
console.log(`  invocations: ${invocations.map((i) => i.id).join("; ")}`);
