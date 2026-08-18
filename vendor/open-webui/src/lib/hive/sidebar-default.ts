/*
 * Whether the sidebar should default to expanded when this browser has never
 * recorded an explicit choice.
 *
 * Sidebar.svelte reads `localStorage.sidebar` on every mount and writes it
 * right back (`localStorage.sidebar = value`) as a side effect of the same
 * mount, so "never set" (undefined/null/empty) and "the app's own default"
 * would otherwise become indistinguishable after the very first page load.
 * That is not a problem in practice: this function is only ever consulted
 * for the READ, and it treats anything other than the literal string
 * 'false' as expanded, so:
 *
 *   - a first-time visitor (no stored value yet) gets the new default,
 *     expanded, and that choice is what gets written back;
 *   - a visitor who has explicitly collapsed it before reads back 'false'
 *     and stays collapsed on every later visit;
 *   - a visitor who explicitly expanded it (or simply accepted the new
 *     default) reads back 'true' and stays expanded.
 *
 * Hive authored. Everything under src/lib/hive/ is ours (see nav.ts).
 */
export const sidebarDefaultExpanded = (stored: string | null | undefined): boolean => stored !== 'false';

/*
 * Whether the current sidebar open/closed state should be persisted to
 * localStorage.
 *
 * Sidebar.svelte forces the sidebar closed whenever the viewport becomes
 * mobile (a transient drawer, not a setting), and that forced change flows
 * through the same subscriber that persists a real, user-initiated toggle.
 * Persisting unconditionally would let one mobile visit silently overwrite
 * an explicit desktop choice (or the expanded default above) with 'false',
 * so the desktop preference is only ever written from a desktop context.
 */
export const shouldPersistSidebarChoice = (mobile: boolean): boolean => !mobile;
