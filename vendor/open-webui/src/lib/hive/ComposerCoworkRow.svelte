<script lang="ts">
	/*
	 * The second row Cowork grows inside the composer (#944, D-045).
	 *
	 * The reference's row carries a project-or-folder picker, an autonomy
	 * control reading `Auto`, and a note. Two of those three are deliberately
	 * absent here, and #944 is the thing that says so:
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
	 * What is left is the row's honest content: the pack this composer will
	 * send, stated rather than chosen, and one line saying where the work will
	 * appear. Both are true today. When either control above becomes real it
	 * lands in this row and this comment loses a bullet.
	 */
	import { getContext } from 'svelte';

	const i18n: any = getContext('i18n');
</script>

<div class="hv-cowork-row" data-hive-cowork-row>
	<span class="hv-cowork-scope">
		<svg
			xmlns="http://www.w3.org/2000/svg"
			viewBox="0 0 24 24"
			fill="none"
			stroke="currentColor"
			stroke-width="1.5"
			stroke-linecap="round"
			stroke-linejoin="round"
			class="hv-cowork-icon"
			aria-hidden="true"
			focusable="false"
		>
			<!-- Lucide "briefcase-business", the knowledge-work pack's own glyph. -->
			<path d="M16 20V4a2 2 0 0 0-2-2h-4a2 2 0 0 0-2 2v16" />
			<path d="M12 12h.01" />
			<rect width="20" height="14" x="2" y="6" rx="2" />
		</svg>
		{$i18n.t('Knowledge work')}
	</span>
	<span class="hv-cowork-note">
		{$i18n.t('Runs in a sandbox. Progress appears in this conversation.')}
	</span>
</div>
