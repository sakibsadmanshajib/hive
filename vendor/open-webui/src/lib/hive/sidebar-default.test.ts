import { describe, expect, it } from 'vitest';

import { sidebarDefaultExpanded, shouldPersistSidebarChoice } from './sidebar-default';

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

describe('shouldPersistSidebarChoice', () => {
	it('persists a real toggle made on desktop', () => {
		expect(shouldPersistSidebarChoice(false)).toBe(true);
	});

	it('does not persist the forced collapse mobile applies on its own', () => {
		// A mobile visit's forced showSidebar=false must never overwrite an
		// explicit desktop choice (or the expanded default) with 'false'.
		expect(shouldPersistSidebarChoice(true)).toBe(false);
	});
});
