/*
 * The picker used to render `items` in whatever order the caller passed
 * them, which in practice was `$models` as edge-api's `/v1/models` returns
 * it: alias_id ascending, a database default rather than anything a user
 * would recognise. Pulled out as a pure function so ordering is unit
 * testable without mounting the Svelte dropdown (issue #1601).
 */

export interface SortableModelItem {
	value: string;
	label: string;
}

/**
 * Pinned models (a user already has a way to mark these, via the picker's
 * own item menu) sort first, then everything is alphabetical by label. Both
 * halves are things the user actually chose or can read at a glance, unlike
 * the alias_id default they replace.
 */
export function sortModelItems<T extends SortableModelItem>(
	items: readonly T[],
	pinnedIds: readonly string[] = []
): T[] {
	const pinned = new Set(pinnedIds);
	return [...items].sort((a, b) => {
		const aPinned = pinned.has(a.value);
		const bPinned = pinned.has(b.value);
		if (aPinned !== bPinned) {
			return aPinned ? -1 : 1;
		}
		return a.label.localeCompare(b.label);
	});
}
