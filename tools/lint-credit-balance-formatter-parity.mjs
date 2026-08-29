#!/usr/bin/env node
/*
 * The credit balance formatter exists twice and must behave once.
 *
 * `formatUsdBalanceFromCredits` renders the money a customer has left. It
 * lives in the Next.js console (apps/web-console/lib/format/credits.ts) and
 * again in the SvelteKit chat front end
 * (vendor/open-webui/src/lib/hive/credits.ts). The two are separate builds
 * with separate dependency trees and no shared module between them, so the
 * only thing that has ever kept them equal is a comment asking the next
 * author to keep them equal by hand.
 *
 * That has already failed twice. The chat copy shipped with the PRICE
 * formatter's round-to-nearest and told customers they held ten dollars they
 * could not spend (#1345), and the low-credit boundary contradicted the pill
 * beside it (#1344). Both were one build drifting from the other while each
 * build's own tests stayed green, because each build's tests only ever see
 * its own copy.
 *
 * Neither test suite can see both files: the console image copies
 * apps/web-console and not vendor/, and the chat image copies
 * vendor/open-webui and not apps/. So the guard lives here, at the repo root,
 * where both files are readable, and runs as its own CI step beside the other
 * tools/lint-*.mjs checks.
 *
 * What is compared is the function source with comments and whitespace
 * removed and quote style normalised, so the two may keep their own
 * formatting (tabs and single quotes in the chat build, spaces and double
 * quotes in the console) and their own commentary, and nothing else. A
 * changed constant, a changed rounding mode, a changed branch or an added
 * early return fails here.
 */

import { readFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

const repoRoot = join(dirname(fileURLToPath(import.meta.url)), '..');

/**
 * Every declaration that has to agree, by name. The formatter itself, plus
 * the two constants its output is built from: a rate change or a reworded
 * sub-cent bound in one build and not the other is the same class of
 * divergence as a rounding change.
 */
const SHARED = ['CREDITS_PER_USD', 'SUB_CENT_BALANCE', 'formatUsdBalanceFromCredits'];

const SOURCES = [
	'apps/web-console/lib/format/credits.ts',
	'vendor/open-webui/src/lib/hive/credits.ts'
];

/** Strip block comments, then line comments. Neither file quotes a `//`. */
const stripComments = (src) => src.replace(/\/\*[\s\S]*?\*\//g, '').replace(/\/\/[^\n]*/g, '');

/**
 * Quote style, whitespace and trailing commas are per build by design: the
 * console is formatted by Biome with trailing commas and the chat front end
 * by its own Prettier config without them. Behaviour is not per build.
 * Normalising the three leaves exactly the tokens that decide what gets
 * rendered.
 */
const normalise = (src) =>
	stripComments(src)
		.replace(/'/g, '"')
		.replace(/\s+/g, '')
		.replace(/,(?=[}\])])/g, '');

/**
 * The declaration's own source, brace matched rather than regex matched, so a
 * nested block or an object literal inside the body cannot end the extraction
 * early. Constants end at their semicolon instead, since they open no brace.
 */
const declaration = (src, name) => {
	const fn = `export function ${name}(`;
	const constant = `export const ${name} `;
	const fnAt = src.indexOf(fn);
	if (fnAt === -1) {
		const constantAt = src.indexOf(constant);
		if (constantAt === -1) {
			return null;
		}
		const end = src.indexOf(';', constantAt);
		return end === -1 ? null : src.slice(constantAt, end + 1);
	}
	let depth = 0;
	let seenBrace = false;
	for (let i = fnAt; i < src.length; i += 1) {
		if (src[i] === '{') {
			depth += 1;
			seenBrace = true;
		} else if (src[i] === '}') {
			depth -= 1;
			if (seenBrace && depth === 0) {
				return src.slice(fnAt, i + 1);
			}
		}
	}
	return null;
};

const failures = [];
const files = SOURCES.map((rel) => ({ rel, src: readFileSync(join(repoRoot, rel), 'utf8') }));

for (const name of SHARED) {
	const found = files.map((file) => ({ ...file, text: declaration(file.src, name) }));
	const missing = found.filter((file) => file.text === null);
	if (missing.length > 0) {
		for (const file of missing) {
			failures.push(`${file.rel}: no exported \`${name}\` found`);
		}
		continue;
	}
	const [first, ...rest] = found;
	for (const other of rest) {
		if (normalise(first.text) !== normalise(other.text)) {
			failures.push(
				`\`${name}\` differs between ${first.rel} and ${other.rel}.\n` +
					`  These two are hand-synced twins on the money path: change both, or\n` +
					`  neither. Comments, indentation and quote style may differ; the code\n` +
					`  that decides what a customer sees may not.\n` +
					`  ${first.rel}:\n${normalise(first.text)}\n` +
					`  ${other.rel}:\n${normalise(other.text)}`
			);
		}
	}
}

if (failures.length > 0) {
	console.error('credit balance formatter parity check failed:\n');
	for (const failure of failures) {
		console.error(`  - ${failure}\n`);
	}
	process.exit(1);
}

console.log(
	`credit balance formatter parity: ${SHARED.length} declarations agree across ${SOURCES.length} builds`
);
