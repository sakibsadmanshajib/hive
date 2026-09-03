/*
 * The two routes that used to end at a Knowledge page, and now end at Projects
 * (issue #1505, and the direction settled on issue #1595).
 *
 * Read as source rather than rendered, exactly as skills-surface.test.ts reads
 * its routes: a redirect that fires in onMount has nothing to assert about in a
 * server render, and the claim worth pinning is which destination the file
 * names, not how Svelte schedules it.
 *
 * Why both files. `/knowledge` was the Hive-authored index that #1109 added to
 * dodge the Workspace layout's permission guard; it lists what is shared with
 * you and can create nothing, which is the whole of #1505. `/workspace` is the
 * upstream index, and it sent every session to `/workspace/knowledge`, the
 * stock management page. That link was dead for an ordinary customer while the
 * knowledge permission was false, and granting the permission in this same
 * change would have quietly brought it back to life as a second Knowledge
 * destination beside Projects. D-045 eliminates Knowledge rather than keeping
 * two of it, so both indexes point at Projects.
 *
 * Neither underlying page is deleted. `/workspace/knowledge` keeps its route
 * and loses its only inbound link, the same posture `/agents` has had since
 * #944: removing a link is not deleting a page.
 */
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { describe, expect, it } from 'vitest';

const route = (rel: string): string =>
	readFileSync(fileURLToPath(new URL(`../../routes/${rel}`, import.meta.url)), 'utf8');

describe('the Knowledge destinations now lead to Projects (#1505)', () => {
	it('sends /knowledge to /projects', () => {
		const source = route('(app)/knowledge/+page.svelte');

		expect(source).toContain("goto('/projects')");
		// The dead end itself: an index that lists what was shared with you and
		// offers no way to author anything must not still be what this renders.
		expect(source).not.toContain('$lib/hive/KnowledgeIndex');
	});

	it('sends /workspace to /projects rather than to the stock Knowledge page', () => {
		const source = route('(app)/workspace/+page.svelte');

		expect(source).toContain("goto('/projects')");
		expect(source).not.toContain("goto('/workspace/knowledge')");
	});
});
