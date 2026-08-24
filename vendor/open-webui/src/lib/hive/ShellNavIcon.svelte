<script lang="ts">
	import type { HiveNavIcon } from './nav';

	export let name: HiveNavIcon;
	export let className = 'hv-nav-icon';

	/*
	 * Lucide, ISC licensed, drawn on the 24 unit grid at stroke width 1.5.
	 *
	 * Inlined rather than pulled in as a dependency: three glyphs do not
	 * justify an icon package in a frontend we now build ourselves, and 1.5 is
	 * the stroke the upstream header icons already use, so a Hive icon and an
	 * upstream icon sit in the same row without one looking heavier.
	 */
</script>

<svg
	xmlns="http://www.w3.org/2000/svg"
	viewBox="0 0 24 24"
	fill="none"
	stroke="currentColor"
	stroke-width="1.5"
	stroke-linecap="round"
	stroke-linejoin="round"
	class={className}
	aria-hidden="true"
	focusable="false"
>
	{#if name === 'agents'}
		<path d="M12 8V4H8" />
		<rect width="16" height="12" x="4" y="8" rx="2" />
		<path d="M2 14h2" />
		<path d="M20 14h2" />
		<path d="M15 13v2" />
		<path d="M9 13v2" />
	{:else if name === 'knowledge'}
		<path d="M12 7v14" />
		<path
			d="M3 18a1 1 0 0 1-1-1V4a1 1 0 0 1 1-1h5a4 4 0 0 1 4 4 4 4 0 0 1 4-4h5a1 1 0 0 1 1 1v13a1 1 0 0 1-1 1h-6a3 3 0 0 0-3 3 3 3 0 0 0-3-3z"
		/>
	{:else if name === 'scheduled'}
		<!-- Lucide "clock", same 24 grid and stroke as the other two glyphs. -->
		<circle cx="12" cy="12" r="10" />
		<polyline points="12 6 12 12 16 14" />
	{/if}
</svg>
