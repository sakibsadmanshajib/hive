/*
 * The user-created Skills surface.
 *
 * Source-pinning rather than rendering, for the same reason the settings
 * declutter guard is: what has to hold is a property of the shipped tree (a
 * route exists, and every link into it points at the Hive path rather than the
 * removed Workspace one), and reading the sources catches a half-done retarget
 * that a render of one component would not.
 *
 * The negative half matters as much as the positive half. Issue #783 removed
 * Workspace > Skills and Caddyfile.owui still 404s `/workspace/skills`, so a
 * link left pointing there is a dead button, not a fallback.
 */

import { describe, expect, it } from 'vitest';

import { existsSync, readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';

const route = (rel: string): string =>
	readFileSync(fileURLToPath(new URL(`../../routes/(app)/skills/${rel}`, import.meta.url)), 'utf8');

const component = (rel: string): string =>
	readFileSync(fileURLToPath(new URL(`../components/workspace/${rel}`, import.meta.url)), 'utf8');

const routeExists = (rel: string): boolean =>
	existsSync(fileURLToPath(new URL(`../../routes/(app)/skills/${rel}`, import.meta.url)));

describe('the /skills surface', () => {
	it('ships the three routes the editor needs', () => {
		// A create page with no index to return to, or an index with no editor,
		// is the shape that reads as shipped and cannot complete a single task.
		expect(routeExists('+page.svelte')).toBe(true);
		expect(routeExists('create/+page.svelte')).toBe(true);
		expect(routeExists('edit/+page.svelte')).toBe(true);
	});

	it('gives every page the workspace scroll container these components were written for', () => {
		// Not cosmetic. The first proof capture rendered the list underneath the
		// sidebar and pinned New Skill to the window corner, because these are
		// upstream Workspace components and a bare flex region gives them
		// neither the sidebar-width constraint nor the horizontal padding.
		const layout = route('+layout.svelte');
		expect(layout).toContain('md:max-w-[calc(100%-var(--sidebar-width))]');
		expect(layout).toContain('px-3 md:px-[18px]');
		expect(layout).toContain('overflow-y-auto');
	});

	it('renders the skills index from the component that already exists', () => {
		// Reused verbatim rather than reimplemented: the list, the editor, the
		// import path and the access dialog are all upstream code that works.
		expect(route('+page.svelte')).toContain('workspace/Skills.svelte');
	});

	it('bounces a session that may not write skills, instead of 401ing on save', () => {
		// The same guard routes/(app)/workspace/+layout.svelte applies, carried
		// here because this route deliberately sits outside that layout. Without
		// it, a deployment that turned the permission off renders a New Skill
		// button whose only outcome is a 401 from the create endpoint.
		// In the layout, so it covers the editor routes too. A guard on the
		// index alone would leave /skills/create reachable by typing the URL.
		const source = route('+layout.svelte');
		expect(source).toContain('permissions?.workspace?.skills');
		expect(source).toContain("goto('/')");
	});

	it('routes every link through /skills and never through the removed /workspace/skills', () => {
		// Caddyfile.owui's @removedSurfaces still 404s /workspace/skills: #783
		// stands, and only its "no user-created skills" half is reversed. A
		// surviving link there is a button that cannot work.
		// Matched at the navigation site (a goto or an href) rather than anywhere
		// in the file, so the comment recording the divergence for the next
		// subtree pull is allowed to name the path it diverged from. A link
		// coming back still fails, which is the property under test.
		const linkToWorkspaceSkills = /(?:goto\(|href=)[^\n]*\/workspace\/skills/;

		for (const rel of ['Skills.svelte', 'Skills/SkillEditor.svelte']) {
			const source = component(rel);
			expect(source).not.toMatch(linkToWorkspaceSkills);
			expect(source).toContain('/skills');
		}
	});

	it('sends the editor back to the index it was opened from', () => {
		// Both editor routes navigate on success and on a missing id. Pointing
		// either at the old path would strand a saved skill on a 404.
		for (const rel of ['create/+page.svelte', 'edit/+page.svelte']) {
			const source = route(rel);
			expect(source).not.toContain('/workspace/skills');
			expect(source).toContain("'/skills'");
		}
	});
});
