/*
 * The message an author sees when a skill name is already taken.
 *
 * `skill.id` is unique across the whole instance and the editor derives it by
 * slugifying the name, so two accounts that both call a skill "Research"
 * collide. Upstream answers that with "Uh-oh! This id is already registered.
 * Please choose another id string.", which is wrong on this surface in two
 * ways. The author typed a NAME and never saw an id, so the sentence names a
 * field they did not fill in. And the skill holding that id is almost never
 * one they can see: reads are denied cross-account by ownership plus access
 * grants, so they search their own library, find nothing called Research, and
 * conclude the product is broken.
 *
 * Issue #1397 is the real fix, scoping uniqueness to the tenant, and PR #1437
 * carries it. Until that lands, and for every account with no tenant group
 * after it lands, this is the message that has to make sense on its own.
 *
 * What it must NOT do is name the conflicting skill or its owner. The
 * constraint already leaks the bare fact that some id is taken, which no
 * wording can hide while the check exists, but nothing beyond that fact is
 * disclosed here.
 */

import { describe, expect, it } from 'vitest';

import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';

import { skillSaveErrorMessage } from './skill-save-error';

const UPSTREAM_ID_TAKEN = 'Uh-oh! This id is already registered. Please choose another id string.';

describe('skillSaveErrorMessage', () => {
	it('replaces the upstream id-collision sentence with one about the name', () => {
		const message = skillSaveErrorMessage(new Error(UPSTREAM_ID_TAKEN), 'Research');

		expect(message).toContain('Research');
		expect(message).toMatch(/unique/i);
		expect(message).not.toMatch(/id string/i);
		expect(message).not.toMatch(/uh-oh/i);
	});

	it('says the holder may be invisible without identifying it', () => {
		const message = skillSaveErrorMessage(UPSTREAM_ID_TAKEN, 'Research');

		// The author has to be told why searching their own library finds
		// nothing, or the message sends them hunting for a skill that is not
		// theirs to see.
		expect(message).toMatch(/cannot see|can't see|another account/i);
		// And nothing beyond the bare existence the constraint already leaks.
		expect(message).not.toMatch(/@/);
		expect(message).not.toMatch(/owned by|belongs to|created by/i);
	});

	it('still offers the id as the escape hatch, since the editor exposes it', () => {
		expect(skillSaveErrorMessage(UPSTREAM_ID_TAKEN, 'Research')).toMatch(/\bid\b/i);
	});

	it('degrades without a name rather than printing an empty quote', () => {
		const message = skillSaveErrorMessage(UPSTREAM_ID_TAKEN, '   ');

		expect(message).toMatch(/unique/i);
		expect(message).not.toContain('""');
		expect(message).not.toContain('“”');
	});

	it('passes every other failure through untouched', () => {
		// Swallowing the real text is how a permission failure, a network
		// failure and a validation failure all come to read as the same
		// shrug. Only the one sentence this function exists to rewrite is
		// rewritten.
		for (const raw of [
			'401: Unauthorized',
			'Error creating skill',
			'Failed to fetch',
			'Uh-oh! This model id is already registered. Please choose another model id string.'
		]) {
			expect(skillSaveErrorMessage(new Error(raw), 'Research')).toBe(raw);
			expect(skillSaveErrorMessage(raw, 'Research')).toBe(raw);
		}
	});

	it('reports something rather than nothing for a thrown non-error', () => {
		expect(skillSaveErrorMessage(undefined, 'Research')).not.toBe('');
		expect(skillSaveErrorMessage(null, 'Research')).not.toBe('');
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

	it('imports the helper and hands it the typed name', () => {
		expect(createRoute).toContain("from '$lib/hive/skill-save-error'");
		expect(createRoute).toMatch(/skillSaveErrorMessage\(\s*error\s*,/);
	});

	it('no longer toasts the raw error text', () => {
		expect(createRoute).not.toMatch(/toast\.error\(`\$\{error\}`\)/);
	});
});
