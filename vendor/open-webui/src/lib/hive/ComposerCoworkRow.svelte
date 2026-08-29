<script lang="ts">
	/*
	 * The second row Cowork grows inside the composer (#944, #1500, D-045).
	 *
	 * The reference's row carries a project-or-folder picker, an autonomy
	 * control reading `Auto`, and a note. The first two are still absent, and
	 * #944 is the thing that says so:
	 *
	 *   * The autonomy control ships with the `waiting_for_confirmation`
	 *     collapse in engine.go or not at all. With Auto selected there are no
	 *     approval rows, so a control shipped before that line is fixed is a
	 *     control with no observable effect.
	 *   * The project or folder picker ships only if a run can be bound to a
	 *     workspace or a collection. `POST /v1/agent/tasks` accepts `pack` and
	 *     `instructions` and nothing else, so there is no binding to set, and a
	 *     picker that sets nothing is worse than no picker.
	 *
	 * The pack was the third thing in that list until #1500. It was rendered
	 * here as a static `<span>` reading "Knowledge work", on the argument that
	 * the pack should be derived rather than chosen. That argument made
	 * `coding-pack` unreachable from the chat surface, leaving the `/agents`
	 * destination D-045 retires as the only way to reach it, so the row now
	 * offers the pack instead of announcing it. `pack` is the one field on
	 * `POST /v1/agent/tasks` a person can actually set, which is exactly the
	 * test the two absent controls above fail.
	 *
	 * WHY THIS ROW AND NOT THE PILL ROW ABOVE
	 * ---------------------------------------
	 * Two reasons, neither of them taste. This row is where the reference puts
	 * Cowork's secondary controls, which is what the bullets above are about:
	 * when either of those becomes real it lands here too. And the pill row has
	 * already lost this argument once, in #1349, where the Chat/Cowork toggle
	 * overlapped the model chip by fifty-four pixels at 375px and had to be
	 * taught to wrap; a second always-mounted segmented control up there adds
	 * width pressure to a row with a measured history of running out of it, to
	 * show a control that means nothing in Chat mode.
	 *
	 * The segments reuse `.hv-mode-segment`, the Chat/Cowork toggle's own
	 * class, rather than introducing a second segmented-control idiom two rows
	 * apart in the same composer.
	 */
	import { getContext } from 'svelte';

	import { composerPack } from '$lib/stores';
	import { COMPOSER_PACKS, nextPack } from './coworkMode';

	const i18n: any = getContext('i18n');

	const select = (pack: (typeof COMPOSER_PACKS)[number]['value']) => {
		composerPack.set(pack);
	};

	const onKeydown = (event: KeyboardEvent) => {
		const moved = nextPack($composerPack, event.key);
		if (!moved) {
			return;
		}
		event.preventDefault();
		select(moved);
		// Focus follows selection, as it does in a native radio group.
		(event.currentTarget as HTMLElement)
			?.querySelector<HTMLElement>(`[data-hive-pack="${moved}"]`)
			?.focus();
	};
</script>

<div class="hv-cowork-row" data-hive-cowork-row>
	<div
		class="hv-mode hv-cowork-packs"
		role="radiogroup"
		aria-label={$i18n.t('Kind of task')}
		data-hive-composer-pack={$composerPack}
		on:keydown={onKeydown}
	>
		{#each COMPOSER_PACKS as option (option.value)}
			<button
				type="button"
				role="radio"
				data-hive-pack={option.value}
				class="hv-mode-segment"
				class:hv-mode-segment-on={$composerPack === option.value}
				aria-checked={$composerPack === option.value}
				tabindex={$composerPack === option.value ? 0 : -1}
				on:click={() => select(option.value)}
			>
				{$i18n.t(option.label)}
			</button>
		{/each}
	</div>
	<span class="hv-cowork-note">
		{$i18n.t('Runs in a sandbox. Progress appears in this conversation.')}
	</span>
</div>
