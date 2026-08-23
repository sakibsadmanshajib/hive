<script lang="ts">
	/*
	 * The composer's visual container, extracted so two surfaces share one.
	 *
	 * This markup was `#message-input-container` inside MessageInput.svelte and
	 * nothing else. It is lifted here verbatim, unchanged, and MessageInput now
	 * renders this component in its place, so chat's composer and the agent
	 * composer cannot drift: there is one rounded surface, one border, one
	 * focus-within treatment and one inner padding, in one file.
	 *
	 * Presentational only, deliberately. No store is read here, nothing is
	 * submitted here, and every piece of chat state that used to compute this
	 * container's classes arrives as a prop. That is what keeps the extraction
	 * mechanical: MessageInput's behaviour, history, model selection and
	 * submission are untouched by it.
	 *
	 * What is NOT shared, and why it is not a gap: the row of controls inside
	 * the container is per surface by design. Chat's carries attach, tools,
	 * skills, web search, voice and the model picker; the agent composer's
	 * carries the pack toggle. Sharing the row would mean sharing chat state
	 * with a surface that has none of it, which is the coupling this extraction
	 * exists to avoid. The send button IS shared, in ComposerSendButton.svelte,
	 * because it is the one control both surfaces really do have.
	 */

	/** Passed through so upstream's own DOM hook survives the extraction. */
	export let id: string | undefined = undefined;
	/** Text direction, chat's own `$settings?.chatDirection` on that surface. */
	export let dir: string = 'auto';
	/** Chat's temporary-chat treatment: a dashed border instead of a solid one. */
	export let dashed = false;
</script>

<div
	{id}
	class="flex-1 flex flex-col relative w-full shadow-lg rounded-3xl border {dashed
		? 'border-dashed border-gray-100 dark:border-gray-800 hover:border-gray-200 focus-within:border-gray-200 hover:dark:border-gray-700 focus-within:dark:border-gray-700'
		: ' border-gray-100/30 dark:border-gray-850/30 hover:border-gray-200 focus-within:border-gray-100 hover:dark:border-gray-800 focus-within:dark:border-gray-800'}  transition px-1 bg-white/5 dark:bg-gray-500/5 backdrop-blur-sm dark:text-gray-100"
	{dir}
>
	<slot />
</div>
