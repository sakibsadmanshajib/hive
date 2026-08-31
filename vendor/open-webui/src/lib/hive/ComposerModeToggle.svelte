<script lang="ts">
	/*
	 * The `Chat | Cowork` segmented control, inside the composer (#944, D-045).
	 *
	 * A `radiogroup` with two `radio` children rather than two buttons. Two
	 * buttons announce as two unrelated controls and are reached with Tab; a
	 * radiogroup announces as "Chat, selected, 1 of 2" and moves with the arrow
	 * keys, which is what a two-way mode actually is. The roving tabindex is
	 * part of that contract: only the selected segment is in the tab order, so
	 * Tab moves past the whole control rather than through it.
	 *
	 * Switching modes never touches the draft. The composer's text lives in
	 * MessageInput, this control writes one store, and nothing in the change
	 * path clears either, which is the property the issue calls out by name.
	 */
	import { getContext } from 'svelte';

	import { composerMode } from '$lib/stores';
	import { COMPOSER_MODES, nextMode, type ComposerMode } from './coworkMode';

	const i18n: any = getContext('i18n');

	const LABELS: Record<ComposerMode, string> = {
		chat: 'Chat',
		cowork: 'Work'
	};

	const select = (mode: ComposerMode) => {
		composerMode.set(mode);
	};

	const onKeydown = (event: KeyboardEvent) => {
		const moved = nextMode($composerMode, event.key);
		if (!moved) {
			return;
		}
		event.preventDefault();
		select(moved);
		// Focus follows selection, as it does in a native radio group, so the
		// arrow key that changed the mode leaves the keyboard on the segment it
		// changed to rather than on the one it left.
		(event.currentTarget as HTMLElement)
			?.querySelector<HTMLElement>(`[data-hive-mode="${moved}"]`)
			?.focus();
	};
</script>

<div
	class="hv-mode"
	role="radiogroup"
	aria-label={$i18n.t('Composer mode')}
	data-hive-composer-mode={$composerMode}
	on:keydown={onKeydown}
>
	{#each COMPOSER_MODES as mode (mode)}
		<button
			type="button"
			role="radio"
			data-hive-mode={mode}
			class="hv-mode-segment"
			class:hv-mode-segment-on={$composerMode === mode}
			aria-checked={$composerMode === mode}
			tabindex={$composerMode === mode ? 0 : -1}
			on:click={() => select(mode)}
		>
			{$i18n.t(LABELS[mode])}
		</button>
	{/each}
</div>
