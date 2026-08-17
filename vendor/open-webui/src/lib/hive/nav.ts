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

export type HiveNavIcon = 'chats' | 'agents' | 'knowledge';

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
		id: 'chats',
		label: 'Chats',
		href: '/',
		icon: 'chats',
		activePaths: ['/', '/c']
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
	}
];

/**
 * Whether a nav row is the current destination.
 *
 * Exact match on '/' rather than prefix match, otherwise the Chats row would
 * be active on every route in the application.
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
