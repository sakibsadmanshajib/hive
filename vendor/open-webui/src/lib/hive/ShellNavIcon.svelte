<script lang="ts">
	import type { HiveNavIcon } from './nav';

	export let name: HiveNavIcon;
	export let className = 'hv-nav-icon';

	/*
	 * Lucide, ISC licensed, drawn on the 24 unit grid at stroke width 1.5.
	 *
	 * Inlined rather than pulled in as a dependency: a handful of glyphs do not
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
	{#if name === 'projects'}
		<path d="M20 20a2 2 0 0 0 2-2V8a2 2 0 0 0-2-2h-7.9a2 2 0 0 1-1.69-.9L9.6 3.9A2 2 0 0 0 7.93 3H4a2 2 0 0 0-2 2v13a2 2 0 0 0 2 2Z" />
	{:else if name === 'artifacts'}
		<!-- Lucide "waypoints": connected nodes, the reference's own Artifacts glyph. -->
		<circle cx="12" cy="4.5" r="2.5" />
		<path d="m10.2 6.3-3.9 3.9" />
		<circle cx="4.5" cy="12" r="2.5" />
		<path d="M7 12h10" />
		<circle cx="19.5" cy="12" r="2.5" />
		<path d="m13.8 17.7 3.9-3.9" />
		<circle cx="12" cy="19.5" r="2.5" />
	{:else if name === 'knowledge'}
		<path d="M12 7v14" />
		<path
			d="M3 18a1 1 0 0 1-1-1V4a1 1 0 0 1 1-1h5a4 4 0 0 1 4 4 4 4 0 0 1 4-4h5a1 1 0 0 1 1 1v13a1 1 0 0 1-1 1h-6a3 3 0 0 0-3 3 3 3 0 0 0-3-3z"
		/>
	{:else if name === 'skills'}
		<!-- Lucide "sparkles", the reference product's own glyph for the
		     customisation family. Same 24 grid and 1.5 stroke as its neighbours. -->
		<path
			d="M9.9 4.2 11 2l1.1 2.2 2.4.4-1.7 1.7.4 2.4L11 7.6 8.8 8.7l.4-2.4L7.5 4.6z"
		/>
		<path d="m18 13 .7 1.5 1.6.2-1.2 1.1.3 1.6-1.4-.8-1.4.8.3-1.6-1.2-1.1 1.6-.2z" />
		<path d="M5.5 11.5 12 18l-3.5 3.5L2 15z" />
		<path d="m9 15 3-3" />
	{:else if name === 'scheduled'}
		<!-- Lucide "clock", same 24 grid and stroke as the other two glyphs. -->
		<circle cx="12" cy="12" r="10" />
		<polyline points="12 6 12 12 16 14" />
	{/if}
</svg>
