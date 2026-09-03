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
 * `/workspace/knowledge` does not merely lose a link. `Caddyfile.owui` 404s
 * the path, and the Knowledge tab in `workspace/+layout.svelte` that pointed at
 * it is deleted here too: its condition was the very permission this change
 * grants, so leaving it would have made the tab appear for every ordinary
 * account at the moment one gained the right to create a knowledge base. The
 * assertions below pin the tab's absence for that reason.
 */
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { describe, expect, it } from 'vitest';

const route = (rel: string): string =>
	readFileSync(fileURLToPath(new URL(`../../routes/${rel}`, import.meta.url)), 'utf8');

describe('the Knowledge destinations now lead to Projects (#1505)', () => {
	it('sends /knowledge to /projects', () => {
		const source = route('(app)/knowledge/+page.svelte');

		expect(source).toContain("goto('/projects'");
		// The dead end itself: an index that lists what was shared with you and
		// offers no way to author anything must not still be what this renders.
		expect(source).not.toContain('$lib/hive/KnowledgeIndex');
	});

	it('sends /workspace to /projects rather than to the stock Knowledge page', () => {
		const source = route('(app)/workspace/+page.svelte');

		expect(source).toContain("goto('/projects'");
		expect(source).not.toContain("goto('/workspace/knowledge')");
	});

	it('replaces history on both redirects, so Back is not a trap', () => {
		// A bookmark is the documented reason these routes still exist. Without
		// replaceState, arriving from one and pressing Back returns here and is
		// pushed forward again, which makes the button useless for exactly the
		// visitor the redirect is for.
		for (const rel of ['(app)/knowledge/+page.svelte', '(app)/workspace/+page.svelte']) {
			expect(route(rel), rel).toContain("goto('/projects', { replaceState: true })");
		}
	});

	it('deletes the Knowledge tab the granted permission would have revealed', () => {
		// The tab's condition was `role === 'admin' || workspace.knowledge`,
		// which is the permission this change grants to every ordinary account.
		// Caddy 404s its target, but the tab is an ordinary anchor: a click is
		// client-side navigation the proxy never sees.
		const layout = route('(app)/workspace/+layout.svelte');

		expect(layout).not.toContain('href="/workspace/knowledge"');
		expect(layout).not.toContain("permissions?.workspace?.knowledge}");
	});
});
