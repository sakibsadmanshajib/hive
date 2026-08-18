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
	it('names the two destinations the shell ships', () => {
		// 'chats' was dropped: its href ('/') duplicated New Chat and no list
		// view was ever built behind it (PR #938 added it for row parity only).
		expect(HIVE_NAV.map((item) => item.id)).toEqual(['agents', 'knowledge']);
	});

	it('points the agent destination at a route inside this application', () => {
		// The whole point of the change: the agent is a destination in the shell,
		// not a link out of it. An absolute URL here would put the user back on a
		// separate page with no sidebar.
		expect(byId('agents').href).toBe('/agents');
		expect(byId('agents').href.startsWith('http')).toBe(false);
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
	const agents = byId('agents');
	const knowledge = byId('knowledge');

	it('marks Agents current on the agent destination and below it', () => {
		expect(isNavItemActive(agents, '/agents')).toBe(true);
		expect(isNavItemActive(agents, '/agents/')).toBe(true);
		expect(isNavItemActive(agents, '/agents/some-task')).toBe(true);
	});

	it('does not match a route that merely starts with the same characters', () => {
		expect(isNavItemActive(agents, '/agents-archive')).toBe(false);
		expect(isNavItemActive(knowledge, '/workspace/knowledgebase')).toBe(false);
	});

	it('treats an empty or missing pathname as no match, since no row links to it', () => {
		expect(isNavItemActive(agents, '')).toBe(false);
		expect(isNavItemActive(knowledge, '')).toBe(false);
	});

	it('marks exactly one row current on each row\'s own destination', () => {
		for (const route of ['/agents', '/workspace/knowledge']) {
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
