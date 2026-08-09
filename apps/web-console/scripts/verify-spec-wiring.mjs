#!/usr/bin/env node
// Guard for issue #813, camouflage shape 14 in docs/TESTING-STANDARD.md.
//
// A Playwright spec that no workflow runs emits no signal at all. It does not
// skip, there is no environment variable to blame, and `npx playwright test`
// locally runs it, so the hole is invisible to anyone working on the specs.
//
// Wiring is resolved by asking Playwright what each workflow invocation
// actually collects, never by matching filenames against the workflow text.
// A filename match is wrong in both directions: it counts a spec named only in
// a COMMENT as wired, and it misses every spec selected by --project, which is
// how owui-nightly.yml invokes eleven of them. Both errors were present in the
// first version of this guard.

import { execFileSync } from "node:child_process";
import { readFileSync, readdirSync, statSync, existsSync } from "node:fs";
import { join, relative, dirname } from "node:path";
import { fileURLToPath } from "node:url";

const appDir = dirname(dirname(fileURLToPath(import.meta.url)));
const repoRoot = dirname(dirname(appDir));
const allowlistPath = join(appDir, "tests", "dark-spec-allowlist.json");
const OWUI_CONFIG = "e2e/phase-19/owui/playwright.owui.config.ts";

// The owui config picks its testMatch from credential env vars, so without
// them every owui project collects zero files and this guard would call eleven
// wired specs dark. Placeholders answer the question the guard is actually
// asking, which is whether the workflow runs the spec when it has its secrets.
const collectEnv = {
	...process.env,
	OWUI_E2E_EMAIL: "spec-wiring-guard",
	OWUI_E2E_PASSWORD: "spec-wiring-guard",
	SUPABASE_OAUTH_CLIENT_ID: "spec-wiring-guard",
	SUPABASE_OAUTH_CLIENT_SECRET: "spec-wiring-guard",
};

// Every way a workflow starts Playwright. `marker` is the exact line that must
// still be present in `workflow`, so deleting an invocation fails this guard
// loudly instead of silently reclassifying the specs it ran. Where a workflow
// calls an npm script, the guard runs that same script, so the args can never
// drift apart from the ones CI uses.
const INVOCATIONS = [
	{
		id: "ci.yml web-e2e explicit paths",
		workflow: ".github/workflows/ci.yml",
		marker:
			"run: npx playwright test tests/e2e/unauth.spec.ts tests/e2e/auth-shell.spec.ts tests/e2e/profile-completion.spec.ts tests/e2e/console-workspace-admin.spec.ts",
		args: [
			"tests/e2e/unauth.spec.ts",
			"tests/e2e/auth-shell.spec.ts",
			"tests/e2e/profile-completion.spec.ts",
			"tests/e2e/console-workspace-admin.spec.ts",
		],
	},
	{
		id: "owui-nightly.yml e2e:owui",
		workflow: ".github/workflows/owui-nightly.yml",
		marker: "npm run e2e:owui",
		script: "e2e:owui",
	},
	{
		id: "owui-nightly.yml e2e:owui:perf",
		workflow: ".github/workflows/owui-nightly.yml",
		marker: "npm run e2e:owui:perf",
		script: "e2e:owui:perf",
	},
];

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

const specFiles = [join(appDir, "tests", "e2e"), join(appDir, "e2e")]
	.flatMap((root) => walk(root))
	.filter((f) => f.endsWith(".spec.ts"))
	.map(rel)
	.sort();

if (specFiles.length === 0) {
	console.error("spec wiring guard FAILED: enumerated zero spec files, the walk is broken");
	process.exit(1);
}

function workflowStillInvokes(inv) {
	const p = join(repoRoot, inv.workflow);
	if (!existsSync(p)) return false;
	return readFileSync(p, "utf8")
		.split("\n")
		.some((line) => line.trim() === inv.marker);
}

// Ask Playwright what this invocation collects. The JSON reporter carries the
// absolute file of every collected spec, which is the only answer that agrees
// with what the runner will actually execute.
function collect(inv) {
	const listArgs = ["--list", "--reporter=json"];
	const [cmd, argv] = inv.script
		? ["npm", ["run", inv.script, "--", ...listArgs]]
		: ["npx", ["playwright", "test", ...inv.args, ...listArgs]];

	let out;
	try {
		out = execFileSync(cmd, argv, {
			cwd: appDir,
			env: collectEnv,
			encoding: "utf8",
			maxBuffer: 64 * 1024 * 1024,
			stdio: ["ignore", "pipe", "pipe"],
		});
	} catch (err) {
		failures.push(`invocation failed to list, treat as broken not empty: ${inv.id}`);
		return [];
	}

	const start = out.indexOf("{");
	if (start < 0) {
		failures.push(`invocation produced no JSON report: ${inv.id}`);
		return [];
	}

	// spec.file is relative to config.rootDir, which differs per config: the
	// owui config roots at its own directory. Resolving against it is what makes
	// the two configs comparable.
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

const wired = new Set();
for (const inv of INVOCATIONS) {
	if (!workflowStillInvokes(inv)) {
		failures.push(`invocation is no longer in its workflow, update this guard: ${inv.id}`);
		continue;
	}
	const collected = collect(inv);
	if (collected.length === 0) {
		// Zero collected is the phase-19 testMatch failure. Silence here would
		// report the specs as dark rather than the selector as broken.
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

// Validate the allowlist before trusting it. An entry with no reason or no
// owner is a silent skip wearing a justification.
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
	console.error("Run the spec from a workflow, or add a justified entry to");
	console.error("  apps/web-console/tests/dark-spec-allowlist.json");
	console.error("Allowlist entries are debt and need a reason, an owner and an issue.");
	process.exit(1);
}

console.log(
	`spec wiring guard: OK, ${wired.size}/${specFiles.length} spec files are run by a workflow` +
		(allowed.size > 0 ? `, ${allowed.size} allowlisted as known debt` : ""),
);
