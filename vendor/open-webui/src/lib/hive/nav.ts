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

export type HiveNavIcon = 'projects' | 'artifacts' | 'knowledge' | 'scheduled';

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
		// D-045 ruling 2 names Artifacts alongside Projects as the pair that
		// replaces Knowledge and Workspace. The route has existed since #1141
		// and was reachable only by typing the URL: the index rendered, and no
		// row in the shell ever pointed at it.
		id: 'artifacts',
		label: 'Artifacts',
		href: '/artifacts',
		icon: 'artifacts',
		activePaths: ['/artifacts']
	},
	/*
	 * There is no Agents row, and that is the point of #944 rather than an
	 * omission. D-045: Cowork is a mode of the chat composer, a run IS a
	 * conversation, and it appears in the conversation list beside chats. A
	 * destination called Agents is the design the owner rejected: you write a
	 * prompt on another page, press start, and can then neither see what the
	 * agent is doing nor steer it, because the surface that would carry both is
	 * somewhere else.
	 *
	 * The '/agents' route itself survives, unlinked, so runs submitted before
	 * this change are still reachable by URL. It gets no row.
	 */
	{
		/*
		 * D-045 ruling 2 eliminates Knowledge in favour of Projects and
		 * Artifacts, and this row is therefore on borrowed time. It is not
		 * deleted here because Projects does not hold RAG collections yet, so
		 * removing the row today would take away the only way to reach them and
		 * put nothing in its place, which is the failure mode #944 warns about
		 * for the Agents row. It goes when Projects can hold what it holds.
		 */
		id: 'knowledge',
		label: 'Knowledge',
		// '/knowledge', not '/workspace/knowledge': the workspace layout's
		// permission guard bounces a non-admin without the workspace.knowledge
		// permission straight home (#1109), which made this row highlight and do
		// nothing on the demo box. The /knowledge route sits outside that guard
		// and renders a read-only index of the caller's own bases; an admin or a
		// permitted account is forwarded to the full workspace surface.
		href: '/knowledge',
		icon: 'knowledge',
		activePaths: ['/knowledge']
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
