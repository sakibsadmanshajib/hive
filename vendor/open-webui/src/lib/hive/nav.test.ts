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
	it('names the three destinations the shell ships', () => {
		expect(HIVE_NAV.map((item) => item.id)).toEqual(['chats', 'agents', 'knowledge']);
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
	const chats = byId('chats');
	const agents = byId('agents');
	const knowledge = byId('knowledge');

	it('marks Chats current on the chat root and on an open conversation', () => {
		expect(isNavItemActive(chats, '/')).toBe(true);
		expect(isNavItemActive(chats, '/c/abc-123')).toBe(true);
	});

	it('does not leave Chats current on every other route', () => {
		expect(isNavItemActive(chats, '/agents')).toBe(false);
		expect(isNavItemActive(chats, '/workspace/knowledge')).toBe(false);
	});

	it('marks Agents current on the agent destination and below it', () => {
		expect(isNavItemActive(agents, '/agents')).toBe(true);
		expect(isNavItemActive(agents, '/agents/')).toBe(true);
		expect(isNavItemActive(agents, '/agents/some-task')).toBe(true);
	});

	it('does not match a route that merely starts with the same characters', () => {
		expect(isNavItemActive(agents, '/agents-archive')).toBe(false);
		expect(isNavItemActive(knowledge, '/workspace/knowledgebase')).toBe(false);
	});

	it('treats an empty or missing pathname as the chat root', () => {
		expect(isNavItemActive(chats, '')).toBe(true);
		expect(isNavItemActive(agents, '')).toBe(false);
	});

	it('marks exactly one row current for any route the shell links to', () => {
		for (const route of ['/', '/c/abc', '/agents', '/workspace/knowledge']) {
			const active = HIVE_NAV.filter((item) => isNavItemActive(item, route));
			expect(active.length).toBe(1);
		}
	});
});
