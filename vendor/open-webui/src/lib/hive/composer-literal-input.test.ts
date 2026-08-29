import { describe, expect, it } from 'vitest';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { Schema } from '@tiptap/pm/model';
import { EditorState } from '@tiptap/pm/state';
import { createChainableState, getTextContentFromNodes } from '@tiptap/core';
import type { InputRule } from '@tiptap/core';
import {
	closeDoubleQuote,
	closeSingleQuote,
	copyright,
	ellipsis,
	emDash,
	laquo,
	leftArrow,
	multiplication,
	notEqual,
	oneHalf,
	oneQuarter,
	openDoubleQuote,
	openSingleQuote,
	plusMinus,
	raquo,
	registeredTrademark,
	rightArrow,
	servicemark,
	superscriptThree,
	superscriptTwo,
	threeQuarters,
	trademark
} from '@tiptap/extension-typography';

// Regression guard for #1399. The chat composer rewrote characters as they
// were typed, and the rewritten string is what reached the model: the composer
// DOM, the outbound `POST /api/chat/completions` body and the rendered
// transcript all agreed with each other and all three differed from what was
// typed. So this is not a display bug and a DOM assertion would not catch it.
//
// What is asserted here is the string the editor ends up holding, which is the
// string the send path serializes. The repo has no DOM harness for the chat
// frontend (see composer-size-guard.test.ts for the same constraint), so
// rather than mount an editor this types through the input-rule pipeline
// directly. ProseMirror's model and state are pure JavaScript; only the view
// needs a DOM.
//
// The rule set under test is derived from RichTextInput.svelte itself, so
// re-adding the extension makes the byte-identity case fail through simulated
// typing rather than through a source match alone.

const COMPOSER = '../components/common/RichTextInput.svelte';

const source = (): string =>
	readFileSync(fileURLToPath(new URL(COMPOSER, import.meta.url)), 'utf8');

// The `richText ? [...] : []` branch of the editor's extension array. That
// branch is what the composer runs: `richText` defaults to true and every
// consumer passes `$settings?.richTextInput ?? true`.
const richTextExtensions = (): string => {
	const src = source();
	const start = src.indexOf('...(richText');
	const end = src.indexOf('...(suggestions', start);
	if (start === -1 || end === -1) {
		throw new Error('richText extension array not found in RichTextInput.svelte');
	}
	return src.slice(start, end);
};

// Every rule @tiptap/extension-typography registers. Each one rewrites the
// text buffer, not its presentation, which is why the mutation survives all
// the way to the request body.
const typographyRules = (): InputRule[] => [
	emDash(),
	ellipsis(),
	openDoubleQuote(),
	closeDoubleQuote(),
	openSingleQuote(),
	closeSingleQuote(),
	leftArrow(),
	rightArrow(),
	copyright(),
	trademark(),
	servicemark(),
	registeredTrademark(),
	oneHalf(),
	plusMinus(),
	notEqual(),
	laquo(),
	raquo(),
	multiplication(),
	superscriptTwo(),
	superscriptThree(),
	oneQuarter(),
	threeQuarters()
];

// The text-rewriting rules the composer actually registers today.
const composerRules = (): InputRule[] =>
	/\bTypography\b/.test(richTextExtensions()) ? typographyRules() : [];

const schema = new Schema({
	nodes: {
		doc: { content: 'paragraph+' },
		paragraph: { content: 'text*', toDOM: () => ['p', 0] },
		text: {}
	},
	marks: {}
});

// Types `text` one character at a time and returns what the editor holds
// afterwards. The matching, the range arithmetic and the dispatch condition
// mirror `run` in @tiptap/core's InputRule module, and the replacement itself
// is the library's own `textInputRule` handler rather than a reimplementation.
// The first test below pins this harness against the corruption measured on
// the deployed box, so a harness that stopped reproducing it would fail rather
// than quietly report success.
const typeInto = (text: string, rules: InputRule[]): string => {
	let state = EditorState.create({ schema });

	for (const char of text) {
		const { from, to } = state.selection;
		const $from = state.doc.resolve(from);
		let matched = false;

		// Input rules never run inside a code block. That exemption is why a
		// formed fence is already byte-faithful, and it is preserved here.
		if (!$from.parent.type.spec.code) {
			const textBefore = getTextContentFromNodes($from) + char;

			for (const rule of rules) {
				if (matched) break;
				if (!(rule.find instanceof RegExp)) continue;

				const match = rule.find.exec(textBefore);
				if (!match) continue;

				const matchedDocLength = match[0].length - char.length;
				if (matchedDocLength > 0) {
					const matchStartOffset = $from.parentOffset - matchedDocLength;
					if (
						matchStartOffset < 0 ||
						$from.parent.textBetween(matchStartOffset, $from.parentOffset) !==
							match[0].slice(0, matchedDocLength)
					) {
						continue;
					}
				}

				const tr = state.tr;
				const chainable = createChainableState({ state, transaction: tr });
				const range = { from: from - (match[0].length - char.length), to };
				const handled = rule.handler({ state: chainable, range, match } as never);

				if (handled === null || !tr.steps.length) continue;

				state = state.apply(tr);
				matched = true;
			}
		}

		if (!matched) {
			state = state.apply(state.tr.insertText(char, from, to));
		}
	}

	return state.doc.textContent;
};

// Code a user types as prose on a coding-assistant surface. Every line here
// exercises at least one rule in the set above.
const CORPUS = [
	'git push --force',
	'git commit -m "msg"',
	"it's a \"test\" -- ok",
	'if (a != b) { return; }',
	'func f() -> int',
	'const [x, ...rest] = arr;',
	'cout << x >> y;',
	'area = 2 * 3',
	'x^2 and 1/2 and (c) and +/- 3',
	'SELECT * FROM t WHERE name = \'ada\';'
];

describe('composer delivers literal input (#1399)', () => {
	it('reproduces the corruption measured on the deployed box', () => {
		// Guards the guard. If this harness could not mangle text, the
		// byte-identity case below would pass for the wrong reason. The two
		// exact strings are the ones captured live against commit c9e1419b.
		expect(typeInto("it's a \"test\" -- ok", typographyRules())).toBe(
			'it’s a “test” — ok'
		);
		expect(typeInto('git push --force', typographyRules())).toBe('git push —force');

		// And the rules the issue's own suggested fix would have left enabled.
		expect(typeInto('if (a != b) { return; }', typographyRules())).toBe(
			'if (a ≠ b) { return; }'
		);
		expect(typeInto('func f() -> int', typographyRules())).toBe('func f() → int');
	});

	it('leaves every corpus line byte for byte as typed', () => {
		for (const line of CORPUS) {
			expect(typeInto(line, composerRules())).toBe(line);
		}
	});

	it('transmits a smart character the user deliberately typed', () => {
		// The fix removes a rewriter; it must not add one. A user who types an
		// em dash, an ellipsis glyph or a curly quote still gets it delivered.
		const typed = 'a — b … “q”';
		expect(typeInto(typed, composerRules())).toBe(typed);
	});

	it('registers no Typography extension in the composer', () => {
		expect(richTextExtensions()).not.toMatch(/\bTypography\b/);
		// The import itself, not a bare mention: the extension array carries a
		// comment naming the package to say why it is absent.
		expect(source()).not.toMatch(/^\s*import .*'@tiptap\/extension-typography';/m);
	});
});
