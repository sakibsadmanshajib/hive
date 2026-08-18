import { describe, expect, it } from 'vitest';

import { sidebarDefaultExpanded } from './sidebar-default';

describe('sidebarDefaultExpanded', () => {
	it('defaults a first-time visitor (no stored value yet) to expanded', () => {
		// localStorage.sidebar reads as undefined via property access before
		// Sidebar.svelte's onMount has ever run for this browser.
		expect(sidebarDefaultExpanded(undefined)).toBe(true);
	});

	it('treats a missing key read via getItem (null) the same as undefined', () => {
		expect(sidebarDefaultExpanded(null)).toBe(true);
	});

	it('treats an empty string the same as unset', () => {
		expect(sidebarDefaultExpanded('')).toBe(true);
	});

	it('keeps a user who explicitly collapsed it collapsed on their next visit', () => {
		expect(sidebarDefaultExpanded('false')).toBe(false);
	});

	it('keeps a user who explicitly expanded it (or accepted the new default) expanded', () => {
		expect(sidebarDefaultExpanded('true')).toBe(true);
	});
});
