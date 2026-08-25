<script lang="ts">
	/*
	 * Four quick-start chips under the composer on the chat home.
	 *
	 * Recognition, not personalisation. The reference's home screen is
	 * identifiable partly because of this row, and a ranked or remembered set
	 * would need a store, a write path, and an answer for what a brand new
	 * account sees, none of which buys anything the static four do not.
	 *
	 * Each chip seeds the composer and returns focus to it rather than
	 * submitting. A chip that sent a message on one click would spend the
	 * user's credits on a label they were reading, and there would be no way
	 * to edit the prompt it stood for.
	 */
	import { createEventDispatcher, getContext } from 'svelte';

	import Code from '$lib/components/icons/Code.svelte';
	import Pencil from '$lib/components/icons/Pencil.svelte';
	import LightBulb from '$lib/components/icons/LightBulb.svelte';
	import ChartBar from '$lib/components/icons/ChartBar.svelte';

	const i18n: any = getContext('i18n');
	const dispatch = createEventDispatcher();

	// The label is what the chip says; the seed is what lands in the composer.
	// They are deliberately different: a one-word label reads as a category, and
	// a composer that fills with the single word "Code" tells the model nothing.
	const chips = [
		{ key: 'Code', seed: 'Help me write code for ', icon: Code },
		{ key: 'Write', seed: 'Help me write ', icon: Pencil },
		{ key: 'Explain', seed: 'Explain this to me: ', icon: LightBulb },
		{ key: 'Analyze', seed: 'Help me analyze ', icon: ChartBar }
	];
</script>

<div class="hv-chips" data-hive-quickstart>
	{#each chips as chip (chip.key)}
		<button
			type="button"
			class="hv-chip"
			on:click={() => {
				dispatch('pick', chip.seed);
			}}
		>
			<svelte:component this={chip.icon} className="hv-chip-icon" />
			<span>{$i18n.t(chip.key)}</span>
		</button>
	{/each}
</div>
