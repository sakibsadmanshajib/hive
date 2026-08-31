import { describe, expect, it } from 'vitest';
import { existsSync, readFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

// Regression guard for the design system's token bridge and for the focus
// indicator (issue #1597 batch A, and issue #1521).
//
// Source level rather than rendered, like composer-size-guard.test.ts: what
// this pins is CSS, and the repo has no visual regression harness for the chat
// front end, so the cheapest honest check is that the load bearing
// declarations are still present and still in the order the cascade needs.
//
// Every contrast figure quoted below was computed from the OKLCH values in
// packages/hive-tokens/tokens.css, converted to sRGB and run through the WCAG
// relative luminance formula. They are quoted so a future edit that changes a
// value has to re-measure rather than eyeball.

const here = dirname(fileURLToPath(import.meta.url));

const read = (rel: string): string => readFileSync(join(here, rel), 'utf8');

// The token stylesheet lives outside this front end, in packages/hive-tokens,
// because the React applications under apps/ consume the same file. The image
// build's in-place test run (npm run test:frontend in Dockerfile.open-webui,
// which is the genuine build-time gate) has it at the repository root, so it is
// found by walking upwards rather than by counting directory levels.
//
// scripts/test-owui-hive-frontend.sh runs from a scratch mirror rooted at src/,
// which cannot hold a path above its own root, so that script drops a copy
// beside src/ instead and the second candidate finds it. Neither candidate
// existing throws rather than skips: a guard that quietly stops running is
// worse than no guard at all.
const readTokens = (): string => {
	const relative = join('packages', 'hive-tokens', 'tokens.css');
	let dir = here;
	while (true) {
		const candidate = join(dir, relative);
		if (existsSync(candidate)) {
			return readFileSync(candidate, 'utf8');
		}
		const parent = dirname(dir);
		if (parent === dir) {
			break;
		}
		dir = parent;
	}
	const mirrored = read(join('..', '..', 'hive-tokens.css'));
	if (mirrored) {
		return mirrored;
	}
	throw new Error('the hive-tokens stylesheet is unreachable from this test lane');
};

const tokens = readTokens();
const tailwind = read(join('..', '..', 'tailwind.css'));
const hive = read('hive.css');

const occurrences = (haystack: string, needle: string): number =>
	haystack.split(needle).length - 1;

describe('the state indicator coral has a value in every theme (#1521)', () => {
	// The defect this replaces: the focus ring token was one literal on bare
	// :root with no value in either dark block, so it was correct at 5.34:1 on
	// charcoal and silently 2.67:1 on cream, against a 3:1 requirement.
	it('is declared three times, once per theme block', () => {
		expect(occurrences(tokens, '--hv-accent-strong:')).toBe(3);
	});

	it('has the measured light value, 4.50:1 on the canvas', () => {
		// On bare :root, so an explicit light choice is never left to a media
		// query to supply.
		expect(tokens).toContain('--hv-accent-strong: oklch(0.55 0.16 42);');
	});

	it('returns to plain coral in both dark blocks, 5.34:1 on the dark page', () => {
		expect(occurrences(tokens, '--hv-accent-strong: oklch(0.678 0.164 43);')).toBe(2);
	});

	it('is what the focus ring derives from, rather than its own literal', () => {
		// Derivation is what makes a missing dark value impossible: there is one
		// token to override per theme, not two that can drift apart.
		expect(tokens).toContain('--hv-focus-ring: var(--hv-accent-strong);');
		expect(tokens).not.toContain('--hv-focus-ring: oklch(');
	});
});

describe('the token families reach a utility class', () => {
	// Before this, only the colour family was bridged, so five of the six
	// families in the token file were reachable from hive.css and from nothing
	// else, and roughly 250 components ran Tailwind's own defaults instead.
	it.each([
		['type', '--text-2xl:'],
		['radius', '--radius-2xl:'],
		['elevation', '--shadow-lg:'],
		['easing', '--ease-out:'],
		['families', '--font-mono:']
	])('the %s family is mapped in @theme', (_family, declaration) => {
		expect(tailwind).toContain(declaration);
	});

	it('bare transition utilities resolve to the token scale, not Tailwind 150ms', () => {
		// These two keys are what all 250 bare transition utilities in the chat
		// surface resolve through.
		expect(tailwind).toContain('--default-transition-duration: var(--hv-duration-fast);');
		expect(tailwind).toContain('--default-transition-timing-function: var(--hv-ease-out);');
	});

	it('the 16px radius the token file forbids by name is not reachable', () => {
		// "Nothing is drawn between 12px and 24px, which is what stops the
		// interface sliding into a field of uniformly soft rectangles." There were
		// 92 uses of rounded-2xl, which was that 16px step.
		expect(tailwind).toContain('--radius-2xl: var(--hv-radius-md);');
		expect(tailwind).toContain('--radius-4xl: var(--hv-radius-lg);');
	});

	it('elevation is copied into @theme rather than referenced', () => {
		// Deliberate, and the one exception in that block. The first three shadow
		// tokens resolve to `none` in dark, and `none` is only legal alone in a
		// box-shadow list, so a reference would invalidate the whole declaration
		// and take every ring utility on the same element down with it.
		expect(tailwind).not.toContain('--shadow-lg: var(--hv-shadow');
		expect(tailwind).not.toContain('--shadow-sm: var(--hv-shadow');
	});
});

describe('contrast failures in the base layer are repointed', () => {
	it('placeholders are the muted ink, not gray-400 at 1.86:1', () => {
		// The composer's placeholder is the first sentence a new user reads on
		// this product, and it failed WCAG 1.4.3 by more than a factor of two.
		expect(tailwind).toContain('color: var(--hv-ink-muted);');
		expect(tailwind).not.toContain('color: theme(--color-gray-400);');
	});

	it('checkboxes carry no blue at all', () => {
		// Blue is absent from this palette by design; coral "is the only hue that
		// is ever a fill".
		const checkbox = tailwind.slice(tailwind.indexOf("input[type='checkbox']"));
		expect(checkbox).not.toMatch(/blue-[0-9]/);
		expect(checkbox).toContain('background-color: var(--hv-accent);');
	});

	it('the checkmark is charcoal at 4.60:1, not white at 2.82:1', () => {
		// A data URI cannot read a custom property, so the token is written out.
		expect(tailwind).toContain('stroke="%232b2b28"');
		expect(tailwind).not.toContain('stroke="white"');
	});
});

describe('focus is drawn for every focusable element (WCAG 2.4.7)', () => {
	const blanket = ':focus-visible {\n\toutline: 2px solid var(--hv-focus-ring);';

	it('one blanket rule exists, against 576 outline-none sites in this tree', () => {
		expect(hive).toContain(blanket);
	});

	it('it stays at specificity (0,2,0), so component rules can still tie', () => {
		// The broader attribute-plus-negation form is (0,3,0) and would outrank
		// the component focus rules rather than tie with them, silently stacking
		// a second ring on the chip and the not-found action. Nothing in this
		// tree uses a positive tabindex, so the narrow form loses no coverage.
		//
		// Asserted against the selector list itself rather than the whole file,
		// because the comment above the rule names the rejected form in order to
		// explain it, and a file-wide search cannot tell an explanation from a
		// selector.
		const declaration = hive.indexOf(blanket);
		const start = hive.lastIndexOf(':is(', declaration);
		expect(start).toBeGreaterThan(-1);
		const selector = hive.slice(start, declaration);
		expect(selector).toContain("[tabindex='0']");
		expect(selector).not.toContain(':not(');
	});

	it('it sits above every component focus rule, because the tie breaks on order', () => {
		const blanketAt = hive.indexOf(blanket);
		const firstComponent = hive.indexOf('.hv-chip:focus-visible');
		expect(blanketAt).toBeGreaterThan(-1);
		expect(firstComponent).toBeGreaterThan(-1);
		expect(blanketAt).toBeLessThan(firstComponent);
	});

	it('the nav row suppresses the outline, so it does not draw three rings', () => {
		const rule = hive.slice(hive.indexOf('.hv-nav-row:focus-visible'), hive.indexOf('.hv-nav-icon'));
		expect(rule).toContain('outline: none;');
	});
});

describe('selected and current states are visible in the light theme', () => {
	const block = (selector: string): string => {
		const start = hive.indexOf(selector);
		expect(start).toBeGreaterThan(-1);
		return hive.slice(start, hive.indexOf('}', start));
	};

	it('the current destination bar is the strong coral, not coral at 2.67:1', () => {
		// A neutral fill cannot reach 3:1 anywhere in this light ramp, so the bar
		// is the indicator that carries WCAG 1.4.11 on this row.
		expect(block('.hv-nav-row-active::before')).toContain(
			'background-color: var(--hv-accent-strong);'
		);
	});

	it('the segmented control has a track for the thumb to sit in', () => {
		// It declared padding and a radius and no background at all.
		expect(block('.hv-mode {')).toContain('background-color: var(--hv-surface-sunken);');
	});

	it('the selected segment carries the ring that clears 3:1, not a 1.15:1 fill', () => {
		expect(block('.hv-mode-segment-on {')).toContain(
			'box-shadow: inset 0 0 0 1px var(--hv-accent-strong);'
		);
	});
});
