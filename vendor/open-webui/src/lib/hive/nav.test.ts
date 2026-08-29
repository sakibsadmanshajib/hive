import { describe, expect, it } from 'vitest';

import { HIVE_NAV, isNavItemActive, type HiveNavItem } from './nav';

const byId = (id: string): HiveNavItem => {
	const item = HIVE_NAV.find((entry) => entry.id === id);
	if (!item) {
		throw new Error(`no nav item with id ${id}`);
	}
	return item;
};

describe('HIVE_NAV', () => {
	it('names the destinations the shell ships, Projects first per D-045', () => {
		// 'chats' was dropped: its href ('/') duplicated New Chat and no list
		// view was ever built behind it (PR #938 added it for row parity only).
		expect(HIVE_NAV.map((item) => item.id)).toEqual([
			'projects',
			'artifacts',
			'knowledge',
			'skills',
			'scheduled'
		]);
	});

	it('points the projects destination at its own route inside this application', () => {
		// D-045 ruling 2: Knowledge is replaced by Projects; context brought in
		// lives here. Same in-shell rule as Agents: no link out of the shell.
		expect(byId('projects').href).toBe('/projects');
		expect(byId('projects').href.startsWith('http')).toBe(false);
	});

	it('points Artifacts at the in-shell index shipped by #1141', () => {
		// D-045 names Projects and Artifacts as the pair replacing Knowledge and
		// Workspace. The index existed for a week with nothing linking to it.
		expect(byId('artifacts').href).toBe('/artifacts');
		expect(byId('artifacts').href.startsWith('http')).toBe(false);
	});

	it('ships no Agents row, because Cowork is a composer mode (#944, D-045)', () => {
		// The row is the thing D-045 forbids, not the route: '/agents' still
		// answers so runs submitted before the composer mode existed are
		// reachable by URL. A row here would restore the destination the owner
		// rejected, where a run is started on a page the conversation cannot see.
		expect(HIVE_NAV.some((item) => item.id === 'agents')).toBe(false);
		expect(HIVE_NAV.some((item) => item.href.startsWith('/agents'))).toBe(false);
	});

	it('points Scheduled at the schedules surface inside this application', () => {
		// The schedules route is a top-level destination (D-045 sidebar grammar:
		// New, Projects, Artifacts, Scheduled), not a child of /agents, so it
		// never lights up the Agents row's prefix.
		expect(byId('scheduled').href).toBe('/schedules');
	});

	it('points Knowledge at its own top-level route, outside the workspace permission guard (#1109)', () => {
		// /workspace/knowledge sits behind the workspace layout's permission
		// guard and bounced a non-admin straight home, which is what made this
		// row read as dead on the demo box. The top-level route renders the
		// read-only index instead.
		expect(byId('knowledge').href).toBe('/knowledge');
	});

	it('points Skills at its own top-level route, not at the removed Workspace tab', () => {
		// Issue #783 removed Workspace > Skills and Caddyfile.owui still 404s
		// /workspace/skills, so a row pointing there would be dead on arrival.
		// The surface it does point at is the user-created skills library.
		expect(byId('skills').href).toBe('/skills');
	});

	it('links no row into the Workspace container at all', () => {
		// Every one of Models, Prompts, Skills and Tools is 404'd at the proxy,
		// and the target navigation deletes the container itself, so a
		// /workspace href in this list is a row that cannot work.
		for (const item of HIVE_NAV) {
			expect(item.href.startsWith('/workspace')).toBe(false);
		}
	});

	it('gives every row a unique id, a label and an in-app href', () => {
		const ids = HIVE_NAV.map((item) => item.id);
		expect(new Set(ids).size).toBe(ids.length);

		for (const item of HIVE_NAV) {
			expect(item.label.trim()).not.toBe('');
			expect(item.href.startsWith('/')).toBe(true);
			expect(item.activePaths.length).toBeGreaterThan(0);
		}
	});
});

describe('isNavItemActive', () => {
	const projects = byId('projects');
	const knowledge = byId('knowledge');

	it('marks a row current on its own destination and below it', () => {
		expect(isNavItemActive(projects, '/projects')).toBe(true);
		expect(isNavItemActive(projects, '/projects/')).toBe(true);
		expect(isNavItemActive(projects, '/projects/some-project')).toBe(true);
	});

	it('does not match a route that merely starts with the same characters', () => {
		expect(isNavItemActive(projects, '/projects-archive')).toBe(false);
		expect(isNavItemActive(knowledge, '/knowledgebase')).toBe(false);
	});

	it('treats an empty or missing pathname as no match, since no row links to it', () => {
		expect(isNavItemActive(projects, '')).toBe(false);
		expect(isNavItemActive(knowledge, '')).toBe(false);
	});

	it('marks exactly one row current on each row\'s own destination', () => {
		for (const route of [
			'/projects',
			'/projects/abc',
			'/artifacts',
			'/knowledge',
			'/schedules'
		]) {
			const active = HIVE_NAV.filter((item) => isNavItemActive(item, route));
			expect(active.length).toBe(1);
		}
	});

	it('marks no row current on the chat root, since Chats is no longer a nav row', () => {
		for (const route of ['/', '/c/abc-123']) {
			const active = HIVE_NAV.filter((item) => isNavItemActive(item, route));
			expect(active.length).toBe(0);
		}
	});
});
