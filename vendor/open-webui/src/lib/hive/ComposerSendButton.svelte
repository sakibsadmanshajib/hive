<script lang="ts">
	import Spinner from '$lib/components/common/Spinner.svelte';

	/*
	 * The composer's send button, extracted alongside ComposerShell.svelte for
	 * the same reason and lifted from the same place (MessageInput.svelte's
	 * `#send-message-button`). Classes, geometry and glyph are unchanged.
	 *
	 * It is the one control both composers genuinely have, so it is the one
	 * control worth sharing. Everything else in either toolbar row belongs to
	 * exactly one surface.
	 */

	/** Upstream's own DOM hook on chat; the agent composer passes its own. */
	export let id: string | undefined = undefined;
	export let disabled = false;
	/** Renders the spinner in place of the arrow. Chat's upload-pending state. */
	export let pending = false;
</script>

<button
	{id}
	class="{!disabled || pending
		? 'bg-black text-white hover:bg-gray-900 dark:bg-white dark:text-black dark:hover:bg-gray-100 '
		: 'text-white bg-gray-200 dark:text-gray-900 dark:bg-gray-700 disabled'} transition rounded-full p-1.5 self-center"
	type="submit"
	{disabled}
>
	{#if pending}
		<Spinner className="size-5" />
	{:else}
		<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 16 16" fill="currentColor" class="size-5">
			<path
				fill-rule="evenodd"
				d="M8 14a.75.75 0 0 1-.75-.75V4.56L4.03 7.78a.75.75 0 0 1-1.06-1.06l4.5-4.5a.75.75 0 0 1 1.06 0l4.5 4.5a.75.75 0 0 1-1.06 1.06L8.75 4.56v8.69A.75.75 0 0 1 8 14Z"
				clip-rule="evenodd"
			/>
		</svg>
	{/if}
</button>
