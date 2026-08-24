/*
 * The Hive shell's navigation, as data.
 *
 * Nav is described rather than hardcoded so it survives a change of chat
 * engine: this array is plain data with no framework in it, and the component
 * that renders it (ShellNavRow.svelte) is the only thing that would have to be
 * rewritten if the shell ever moves off Svelte.
 *
 * Hive authored. Everything under src/lib/hive/ is ours, so a rebase against a
 * future upstream tag reads as a file list rather than an archaeology exercise.
 */

export type HiveNavIcon = 'projects' | 'agents' | 'knowledge' | 'scheduled';

export interface HiveNavItem {
	/** Stable id, used for the DOM id and as the test hook. */
	id: string;
	/** English source string; passed through the i18n store at render time. */
	label: string;
	href: string;
	icon: HiveNavIcon;
	/**
	 * Route prefixes that make this row the current one. A prefix matches the
	 * path itself and anything below it, so '/c' covers '/c/<chat id>' without
	 * also matching '/chats-of-someone-else'.
	 */
	activePaths: string[];
}

export const HIVE_NAV: readonly HiveNavItem[] = [
	{
		// D-045 ruling 2: Projects is a first class destination, the home of
		// context brought in (RAG documents live here). It sits at the top of
		// the row list until the full D-045 sidebar grammar lands.
		id: 'projects',
		label: 'Projects',
		href: '/projects',
		icon: 'projects',
		activePaths: ['/projects']
	},
	{
		id: 'agents',
		label: 'Agents',
		href: '/agents',
		icon: 'agents',
		activePaths: ['/agents']
	},
	{
		id: 'knowledge',
		label: 'Knowledge',
		href: '/workspace/knowledge',
		icon: 'knowledge',
		activePaths: ['/workspace/knowledge']
	},
	{
		id: 'scheduled',
		label: 'Scheduled',
		href: '/schedules',
		icon: 'scheduled',
		activePaths: ['/schedules']
	}
];

/**
 * Whether a nav row is the current destination.
 *
 * Exact match on '/' rather than prefix match: none of the current rows link
 * to the chat root, so a prefix match on '/' would light up every route.
 */
export const isNavItemActive = (item: HiveNavItem, pathname: string): boolean => {
	const path = normalizePath(pathname);

	return item.activePaths.some((prefix) => {
		if (prefix === '/') {
			return path === '/';
		}
		return path === prefix || path.startsWith(`${prefix}/`);
	});
};

const normalizePath = (pathname: string): string => {
	if (!pathname) {
		return '/';
	}
	// Trailing slashes arrive from typed URLs and from SvelteKit's own
	// trailingSlash handling; '/agents/' and '/agents' are one destination.
	const trimmed = pathname.length > 1 ? pathname.replace(/\/+$/, '') : pathname;
	return trimmed === '' ? '/' : trimmed;
};
