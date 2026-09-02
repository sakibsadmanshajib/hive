<script lang="ts">
	/*
	 * The second row Cowork grows inside the composer (#944, #1500, #1623, D-045).
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
	 *     workspace or a collection.
	 *
	 * The pack was a third control here, added by #1500 and removed by #1623.
	 * The two changes are not a reversal of each other. #1500 argued, correctly,
	 * that deriving the pack from a constant made `coding-pack` unreachable from
	 * the chat surface at all. #1623 keeps both packs reachable and moves the
	 * decision to the one party that can make it from evidence: the server, from
	 * the instructions the person just wrote
	 * (apps/control-plane/internal/agenttask/infer.go). Choosing between the
	 * words "Knowledge work" and "Coding" before typing anything asked a customer
	 * to classify their own request against two labels that name a system prompt.
	 *
	 * WHAT THIS ROW SHOWS NOW, AND WHEN
	 * ---------------------------------
	 * On a fresh conversation, the note and nothing else. That is the acceptance
	 * criterion of #1623: one toggle in the composer, `Chat | Cowork`, and no
	 * second choice to make.
	 *
	 * Once a run has been classified, one sentence naming what it was read as
	 * plus one button offering the other pack for the next submission. The
	 * correction appears only after there is something to correct, which is what
	 * keeps it from being the old toggle in different words, and it exists at all
	 * because an inference nobody can see or override is worse than the control
	 * it replaced: a person with a misread request would have no way to tell it
	 * from a bad answer.
	 *
	 * The durable record of the decision is not here. It is a line in the run's
	 * own progress chain (coworkMode.inferredPackStep), which persists with the
	 * conversation; this row is session scoped and only ever describes the last
	 * submission. That is why it says "your last task" rather than "this one":
	 * the store outlives a conversation switch, so a sentence that claimed to
	 * be about the conversation on screen would be wrong the moment somebody
	 * opened another one.
	 */
	import { getContext } from 'svelte';

	import { composerPack, coworkLastPack } from '$lib/stores';
	import { otherPack, packLabel } from './coworkMode';

	const i18n: any = getContext('i18n');
</script>

<div class="hv-cowork-row" data-hive-cowork-row>
	{#if $composerPack}
		<span class="hv-cowork-inference" data-hive-pack-pending={$composerPack}>
			{$i18n.t('Next task: {{kind}}.', { kind: packLabel($composerPack) })}
		</span>
		<button type="button" class="hv-cowork-override" on:click={() => composerPack.set(null)}>
			{$i18n.t('Let Hive choose')}
		</button>
	{:else if $coworkLastPack}
		<span class="hv-cowork-inference" data-hive-pack-inferred={$coworkLastPack}>
			{$i18n.t('Hive read your last task as {{kind}}.', { kind: packLabel($coworkLastPack) })}
		</span>
		<button
			type="button"
			class="hv-cowork-override"
			data-hive-pack-override={otherPack($coworkLastPack)}
			on:click={() => composerPack.set(otherPack($coworkLastPack))}
		>
			{$i18n.t('Run the next one as {{kind}}', { kind: packLabel(otherPack($coworkLastPack)) })}
		</button>
	{/if}
	<span class="hv-cowork-note">
		{$i18n.t('Runs in a sandbox. Progress appears in this conversation.')}
	</span>
</div>
