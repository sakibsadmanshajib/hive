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
// typed. So this is not a display bug, and a DOM assertion would not catch it.
//
// What is asserted here is the string the editor ends up holding, which is the
// string the send path serializes. The repo has no DOM harness for the chat
// frontend (see composer-size-guard.test.ts for the same constraint), so
// rather than mount an editor this types through the input-rule pipeline
// directly. ProseMirror's model and state are pure JavaScript; only the view
// needs a DOM.
//
// The rule set under test is keyed on the MODULE each extension is imported
// from, not on the identifier it is bound to, so re-importing the extension
// under another name (`import SmartText from '@tiptap/extension-typography'`)
// still makes the typing cases fail. An earlier version of this guard keyed on
// the literal name `Typography` and was demonstrably blind to that rename.

const COMPOSER = '../components/common/RichTextInput.svelte';

const source = (): string =>
	readFileSync(fileURLToPath(new URL(COMPOSER, import.meta.url)), 'utf8');

const stripLineComments = (s: string): string => s.replace(/^\s*\/\/.*$/gm, '');

// The `richText ? [ ... ] : []` branch of the editor's extension array, which
// is the branch the composer runs: `richText` defaults to true and every
// consumer passes `$settings?.richTextInput ?? true`.
//
// Anchored on the ARRAY branch specifically. `...(richText` appears three
// times in this component, and the first two are object spreads inside
// `StarterKit.configure`, so matching the first occurrence lands in the wrong
// place and silently scans the wrong text.
const richTextExtensions = (): string => {
	const src = source();
	const opens = [...src.matchAll(/\.\.\.\(richText\s*\?\s*(\[|\{)/g)].filter(
		(m) => m[1] === '['
	);
	if (opens.length !== 1) {
		throw new Error(`expected exactly one "...(richText ? [" branch, found ${opens.length}`);
	}
	const start = opens[0].index ?? -1;
	const end = src.indexOf('...(suggestions', start);
	if (start === -1 || end === -1) {
		throw new Error('rich text extension array not found in RichTextInput.svelte');
	}
	return src.slice(start, end);
};

// Every identifier the component imports, mapped to the module it came from.
// Handles default imports, named imports and `as` aliases, which is what makes
// the guard below independent of what an extension is called locally.
const importedBindings = (src: string): Map<string, string> => {
	const bindings = new Map<string, string>();
	for (const match of src.matchAll(/import\s+([^;]+?)\s+from\s+['"]([^'"]+)['"]\s*;/g)) {
		const clause = match[1].trim();
		const module = match[2];

		const named = clause.match(/\{([^}]*)\}/);
		if (named) {
			for (const part of named[1].split(',')) {
				const [original, alias] = part.split(/\s+as\s+/).map((s) => s.trim());
				if (original) bindings.set(alias || original, module);
			}
		}

		const beforeBrace = clause.split('{')[0].replace(/,\s*$/, '').trim();
		if (beforeBrace && /^[A-Za-z_$][\w$]*$/.test(beforeBrace)) {
			bindings.set(beforeBrace, module);
		}
	}
	return bindings;
};

// The modules actually registered in the rich text branch. An identifier only
// counts when the component imported it, which keeps local option keys and
// bare words out of the result.
const richTextModules = (): string[] => {
	const bindings = importedBindings(source());
	const body = stripLineComments(richTextExtensions());
	const modules = new Set<string>();
	for (const match of body.matchAll(/[A-Za-z_$][\w$]*/g)) {
		const module = bindings.get(match[0]);
		if (module) modules.add(module);
	}
	return [...modules];
};

// Modules whose input rules rewrite text the user typed. Keyed by module so a
// rename cannot slip past.
const REWRITING_MODULES = new Set(['@tiptap/extension-typography']);

// Extensions reviewed and accepted in the rich text branch. This is an
// allowlist rather than a denylist on purpose: a NEW extension that happens to
// rewrite input fails this test until someone looks at it, which a denylist
// naming only Typography would not do.
const REVIEWED_MODULES = new Set([
	'@tiptap/extension-code-block-lowlight',
	'@tiptap/extension-table',
	'@tiptap/extension-list'
]);

// All twenty-two rules @tiptap/extension-typography registers. Each rewrites
// the text buffer, not its presentation, which is why the mutation survives
// into the request body.
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

// The text-rewriting rules the composer actually registers today, derived from
// the component rather than hardcoded.
const composerRules = (): InputRule[] =>
	richTextModules().some((module) => REWRITING_MODULES.has(module)) ? typographyRules() : [];

const schema = new Schema({
	nodes: {
		doc: { content: 'block+' },
		paragraph: { group: 'block', content: 'text*', toDOM: () => ['p', 0] },
		code_block: {
			group: 'block',
			content: 'text*',
			code: true,
			marks: '',
			toDOM: () => ['pre', ['code', 0]]
		},
		text: {}
	},
	marks: {}
});

// `textInputRule`'s handler reads only `state`, `range` and `match`. The rest
// of the handler context is the tiptap CommandManager surface, which cannot be
// constructed without a live Editor, so the call is narrowed to the three
// properties that are actually read rather than mocked with values that would
// have to lie about their own type.
type HandlerProps = Pick<Parameters<InputRule['handler']>[0], 'state' | 'range' | 'match'>;

const applyHandler = (rule: InputRule, props: HandlerProps): void | null =>
	(rule.handler as (p: HandlerProps) => void | null)(props);

// Types `text` one character at a time and returns what the editor holds
// afterwards. The matching, the range arithmetic and the dispatch condition
// mirror `run` in @tiptap/core's InputRule module, and the replacement itself
// is the library's own `textInputRule` handler rather than a reimplementation.
// The first test below pins this harness against the corruption measured on
// the deployed box, so a harness that stopped reproducing it would fail rather
// than quietly report success.
const typeInto = (text: string, rules: InputRule[], block: 'paragraph' | 'code_block' = 'paragraph'): string => {
	let state = EditorState.create({
		schema,
		doc: schema.node('doc', null, [schema.node(block)])
	});

	for (const char of text) {
		const { from, to } = state.selection;
		const $from = state.doc.resolve(from);
		let matched = false;

		// Input rules never run inside a code block. That exemption is why a
		// formed fence was already byte-faithful before this fix, and the
		// code_block case below exercises it rather than assuming it.
		if (!$from.parent.type.spec.code) {
			const textBefore = getTextContentFromNodes($from) + char;

			for (const rule of rules) {
				if (matched) break;
				if (!(rule.find instanceof RegExp)) continue;

				const match = rule.find.exec(textBefore);
				if (!match) continue;

				const tr = state.tr;
				const chainable = createChainableState({ state, transaction: tr });
				const range = { from: from - (match[0].length - char.length), to };

				if (applyHandler(rule, { state: chainable, range, match }) === null) continue;
				if (!tr.steps.length) continue;

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
	"SELECT * FROM t WHERE name = 'ada';"
];

describe('composer delivers literal input (#1399)', () => {
	it('reproduces the corruption measured on the deployed box', () => {
		// Guards the guard. If this harness could not mangle text, the
		// byte-identity case below would pass for the wrong reason. The two
		// exact strings are the ones captured live against commit c9e1419b.
		expect(typeInto('it\'s a "test" -- ok', typographyRules())).toBe('it’s a “test” — ok');
		expect(typeInto('git push --force', typographyRules())).toBe('git push —force');

		// And the rules the issue's own suggested fix would have left enabled.
		expect(typeInto('if (a != b) { return; }', typographyRules())).toBe('if (a ≠ b) { return; }');
		expect(typeInto('func f() -> int', typographyRules())).toBe('func f() → int');
	});

	it('leaves every corpus line byte for byte as typed', () => {
		for (const line of CORPUS) {
			expect(typeInto(line, composerRules())).toBe(line);
		}
	});

	it('leaves a formed code block byte for byte as typed, rules or not', () => {
		// R2: this held before the fix and must not regress. Asserted against
		// the full rule set, so it is the code block exemption being proven and
		// not merely the absence of rules.
		const line = 'x = "it\'s" -- 1 != 2';
		expect(typeInto(line, typographyRules(), 'code_block')).toBe(line);
		expect(typeInto(line, composerRules(), 'code_block')).toBe(line);
	});

	it('transmits a smart character the user deliberately typed', () => {
		// The fix removes a rewriter; it must not add one. A user who types an
		// em dash, an ellipsis glyph or a curly quote still gets it delivered.
		const typed = 'a — b … “q”';
		expect(typeInto(typed, composerRules())).toBe(typed);
	});

	it('registers no text rewriting extension, under any import name', () => {
		const registered = richTextModules();
		for (const module of registered) {
			expect(REWRITING_MODULES.has(module)).toBe(false);
		}
		// Allowlist, so a newly added extension has to be looked at rather than
		// silently inheriting a pass because it is not the one module named above.
		expect(registered.filter((module) => !REVIEWED_MODULES.has(module))).toEqual([]);
	});
});
