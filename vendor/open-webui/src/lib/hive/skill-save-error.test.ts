/*
 * The message an author sees when saving a skill fails.
 *
 * Upstream answers an id collision with "Uh-oh! This id is already registered.
 * Please choose another id string.", which reads as a schema complaint and
 * explains nothing about why the author cannot find the offending skill: reads
 * are denied cross-account by ownership plus access grants, so they search
 * their own library, find nothing, and conclude the product is broken.
 *
 * Two properties are load bearing and are pinned below rather than trusted.
 *
 * It must name the ID, never the name. `SkillEditor.svelte` derives the id
 * from the name only while creating a fresh skill (`if (!edit && !clone &&
 * name)`), so a clone, a markdown import and a hand-edited id all break that
 * link, and `slugify` collapses punctuation so "Research!" and "Research" are
 * one id. On all four an author told to change the NAME can edit forever
 * without moving the thing that collided.
 *
 * It must assert no scope. "Unique across this whole instance" is true today
 * and false for tenant-grouped accounts the moment PR #1437 lands, which this
 * branch is blocked on.
 *
 * It must not name the conflicting skill or its owner. The constraint already
 * leaks that an id is taken, which no wording hides while the check exists,
 * but nothing beyond that fact is disclosed.
 */

import { describe, expect, it } from 'vitest';

import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';

import { skillSaveErrorMessage } from './skill-save-error';

// The exact bytes of ERROR_MESSAGES.ID_TAKEN in
// backend/open_webui/constants.py, which is what the client rethrows.
const UPSTREAM_ID_TAKEN = 'Uh-oh! This id is already registered. Please choose another id string.';

describe('skillSaveErrorMessage', () => {
	it('names the id that collided, not the name the author typed', () => {
		const message = skillSaveErrorMessage(UPSTREAM_ID_TAKEN, 'research');

		expect(message).toContain('research');
		expect(message).toMatch(/\bid\b/i);
		expect(message).not.toMatch(/id string/i);
		expect(message).not.toMatch(/uh-oh/i);
		// Telling the author to rename is the advice that loops forever on a
		// clone, an import, a hand-edited id, and a punctuation-only change.
		expect(message).not.toMatch(/different name|another name|choose a name/i);
	});

	it('claims no uniqueness scope, because #1437 is about to change it', () => {
		const message = skillSaveErrorMessage(UPSTREAM_ID_TAKEN, 'research');

		expect(message).not.toMatch(/instance/i);
		expect(message).not.toMatch(/\bglobal(ly)?\b/i);
		expect(message).not.toMatch(/\beverywhere\b/i);
	});

	it('says the holder may be invisible without identifying it', () => {
		const message = skillSaveErrorMessage(UPSTREAM_ID_TAKEN, 'research');

		// Without this the author hunts through their own library for a skill
		// that was never theirs to see.
		expect(message).toMatch(/cannot see/i);
		// And nothing beyond the bare existence the constraint already leaks.
		expect(message).not.toMatch(/@/);
		expect(message).not.toMatch(/owned by|belongs to|created by/i);
	});

	it('degrades without an id rather than printing an empty quote', () => {
		const message = skillSaveErrorMessage(UPSTREAM_ID_TAKEN, '   ');

		expect(message).toMatch(/already in use/i);
		expect(message).not.toContain('""');
		expect(message).not.toContain('“”');
	});

	/*
	 * The pass-through cases use bare strings because that is the only shape
	 * this can actually receive. `createNewSkill` does `error = err.detail` and
	 * then `throw error`, so what arrives is the `detail` value out of the JSON
	 * body, never an Error and never the response. Feeding it Error objects and
	 * fetch's "Failed to fetch" would be testing an input the client cannot
	 * produce, and passing on an impossible input proves nothing.
	 */
	it('passes every other backend detail through untouched', () => {
		for (const detail of [
			'401: Unauthorized',
			'Error creating skill',
			'Uh-oh! This model id is already registered. Please choose another model id string.'
		]) {
			expect(skillSaveErrorMessage(detail, 'research')).toBe(detail);
		}
	});

	it('handles an Error defensively, for a caller that is not this client', () => {
		// Not the shape createNewSkill throws. Covered because the parameter is
		// `unknown` and a future caller may hand it one, not because this path
		// is reachable from the create route today.
		expect(skillSaveErrorMessage(new Error(UPSTREAM_ID_TAKEN), 'research')).toContain(
			'research'
		);
		expect(skillSaveErrorMessage(new Error('boom'), 'research')).toBe('boom');
	});

	it('reports something rather than nothing when the client resolves with no detail', () => {
		expect(skillSaveErrorMessage(undefined, 'research')).not.toBe('');
		expect(skillSaveErrorMessage(null, 'research')).not.toBe('');
	});

	/*
	 * The success toast beside these is `$i18n.t('Skill created successfully')`,
	 * and the fork ships a bn-BD catalogue because Bangladesh is the first
	 * market. A translated success next to an untranslated failure out of the
	 * same handler is an oversight, so the sentences take the translator.
	 */
	it('routes its own sentences through the supplied translator', () => {
		const seen: string[] = [];
		const t = (key: string, vars?: Record<string, string>) => {
			seen.push(key);
			return vars ? `TRANSLATED:${vars.id}` : `TRANSLATED:${key}`;
		};

		expect(skillSaveErrorMessage(UPSTREAM_ID_TAKEN, 'research', t)).toBe('TRANSLATED:research');
		expect(seen[0]).toContain('{{id}}');

		// The generic failure is a translator key too, not a bare literal.
		expect(skillSaveErrorMessage(undefined, 'research', t)).toMatch(/^TRANSLATED:/);
	});

	it('interpolates without a translator so a caller with no i18n still reads English', () => {
		expect(skillSaveErrorMessage(UPSTREAM_ID_TAKEN, 'research')).not.toContain('{{id}}');
	});
});

/*
 * A helper nothing calls is worse than no helper: it passes its own unit tests
 * forever while the author on the deployed box still reads the upstream
 * sentence. Source-pinning the one caller, the same way skills-surface.test.ts
 * pins the routes, is what makes a silent unwiring fail here rather than in
 * front of a customer.
 */
describe('the create route is wired to it', () => {
	const createRoute = readFileSync(
		fileURLToPath(new URL('../../routes/(app)/skills/create/+page.svelte', import.meta.url)),
		'utf8'
	);

	it('imports the helper and hands it the id plus the translator', () => {
		expect(createRoute).toContain("from '$lib/hive/skill-save-error'");
		expect(createRoute).toMatch(/skillSaveErrorMessage\(\s*error\s*,[^)]*\.id/);
		expect(createRoute).toMatch(/skillSaveErrorMessage\([^)]*\$i18n\.t/);
	});

	it('no longer toasts the raw error text', () => {
		expect(createRoute).not.toMatch(/toast\.error\(`\$\{error\}`\)/);
	});

	/*
	 * `createNewSkill` does not reject on every failure. Its own catch reads
	 * `err.detail` and only rethrows when that is truthy
	 * ($lib/apis/skills/index.ts), so a network failure, a proxy error page, or
	 * any response whose body is not JSON carrying a `detail` key resolves the
	 * promise with `null` instead. The route's `.catch` never fires for those,
	 * and the original `if (res)` had no else arm, so the author clicked Save
	 * and absolutely nothing happened: no toast, no navigation, no clue.
	 *
	 * Upstream's client is shared by every skills caller, so the guard goes in
	 * the route Hive owns rather than in vendor code this change never
	 * exercised elsewhere.
	 */
	it('still says something when the client resolves null instead of throwing', () => {
		expect(createRoute).toMatch(/}\s*else\s*if\s*\(\s*!\s*\w+\s*\)\s*{/);
		// At least two toast.error sites: the thrown-error arm and the
		// silent-null arm. Not an exact count, which would red this test for a
		// third legitimate error toast it has nothing to say about.
		expect(
			(createRoute.match(/toast\.error\(/g) ?? []).length
		).toBeGreaterThanOrEqual(2);
	});
});
