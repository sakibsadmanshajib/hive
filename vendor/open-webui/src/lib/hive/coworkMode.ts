/*
 * Cowork as a mode of the composer, not a place you go (#944, D-045).
 *
 * Hive authored. Everything under src/lib/hive/ is ours, so a rebase against a
 * future upstream tag reads as a file list rather than an archaeology exercise.
 *
 * WHY THIS FILE HOLDS NO SVELTE
 * -----------------------------
 * scripts/test-owui-hive-frontend.sh copies lib/hive into a scratch tree and
 * installs vitest and nothing else, so anything importing `svelte/store` here
 * would be untestable by the one runner that covers this front end. The
 * writable lives in `$lib/stores` with the application's other writables; the
 * decisions live here, where they can be tested.
 *
 * WHAT D-045 SETTLES, SO IT IS NOT RE-DERIVED BELOW
 * ------------------------------------------------
 * Cowork is a two segment control inside the composer reading `Chat | Cowork`,
 * immediately right of the plus. Switching it changes what the next message
 * does and never navigates. A run IS a conversation: it lands in the same
 * conversation list, opens in the same main pane, and renders in the same
 * transcript component as a chat.
 */

export type ComposerMode = 'chat' | 'cowork';

export const COMPOSER_MODES: readonly ComposerMode[] = ['chat', 'cowork'];

export const isComposerMode = (value: unknown): value is ComposerMode =>
	value === 'chat' || value === 'cowork';

/*
 * The pack is derived, never chosen (#944).
 *
 * The surface being replaced offered a segmented Coding / Knowledge control.
 * `agent_kind` still carries the distinction on the wire, but the coding pack
 * belongs to a Code panel this shell does not have, so offering it here would
 * be a control whose second segment produces a worse result on this surface.
 * The Home composer sends the knowledge work pack; when a Code panel exists it
 * passes its own mode through this function rather than growing a control.
 */
export const packForMode = (_mode: ComposerMode): 'knowledge-work-pack' => 'knowledge-work-pack';

/**
 * Arrow-key movement inside the radiogroup.
 *
 * A `radiogroup` with two `radio` children announces as "Chat, selected, 1 of
 * 2" and moves with the arrow keys; two plain buttons announce as two
 * unrelated controls and move with Tab. The keyboard contract is the reason
 * for the role, so it is implemented rather than assumed, and it wraps at both
 * ends the way a native radio group does.
 */
export const nextMode = (current: ComposerMode, key: string): ComposerMode | null => {
	const index = COMPOSER_MODES.indexOf(current);
	if (index === -1) {
		return null;
	}
	if (key === 'ArrowRight' || key === 'ArrowDown') {
		return COMPOSER_MODES[(index + 1) % COMPOSER_MODES.length];
	}
	if (key === 'ArrowLeft' || key === 'ArrowUp') {
		return COMPOSER_MODES[(index - 1 + COMPOSER_MODES.length) % COMPOSER_MODES.length];
	}
	return null;
};

/*
 * The projection of a run onto a transcript turn.
 *
 * This is the load-bearing half of #944: the claim underneath the whole change
 * is that a run and a conversation are the same kind of object, and this
 * function is where that claim is cashed. The assistant turn holds a run's
 * state while it is queued or running and its own result once it settles, so
 * one transcript component renders both kinds of turn with no branch in it.
 *
 * ponytail: the ceiling here is the wire, not the renderer. edge-api exposes
 * a task's status and its final `result_summary_ref` and nothing in between:
 * there is no per-step feed, so the intermediate tool lines D-045 describes
 * ("Used Claude in Chrome (2 actions)") and the Progress / Working folder /
 * Context panel cannot be populated by any frontend. When a per-task detail or
 * event endpoint lands, the extra lines go here and the panel reads the same
 * source; nothing else in the composer path has to move.
 */
export interface RunLike {
	status: string;
	result_summary_ref?: string;
	error_message?: string;
}

export const RUNNING_LABEL: Record<string, string> = {
	queued: 'Queued. Waiting for a sandbox.',
	running: 'Working on it.'
};

export const renderRun = (task: RunLike): string => {
	const summary = (task.result_summary_ref ?? '').trim();
	const error = (task.error_message ?? '').trim();

	if (task.status === 'succeeded') {
		// A run that succeeded and returned nothing is not an error, and it is
		// not blank either: a turn with no content renders as an empty bubble,
		// which reads as a failure the transcript declined to mention.
		return summary || 'The task finished and returned no summary.';
	}
	if (task.status === 'failed') {
		return error ? `The task failed. ${error}` : 'The task failed.';
	}
	if (task.status === 'cancelled') {
		return 'The task was cancelled.';
	}
	if (task.status === 'unknown') {
		// Mirrors agentTasks.ts: a status this build cannot name is reported as
		// unreadable rather than silently rendered as still running, which would
		// leave a spinner turning over a task that already settled.
		return 'This task is in a state this version of Hive cannot read. Reload to try again.';
	}
	return RUNNING_LABEL[task.status] ?? 'Working on it.';
};

/**
 * Whether the transcript turn should stop showing itself as in progress.
 *
 * `unknown` is settled HERE and is deliberately not settled in
 * agentTasks.TERMINAL_STATUSES, which drives polling. The two answer different
 * questions: polling keeps asking because the next answer may be readable,
 * while the turn must stop claiming to be generating, because a message left
 * with `done: false` disables the composer's send path for that conversation
 * forever.
 */
export const runTurnIsDone = (task: RunLike): boolean =>
	task.status === 'succeeded' ||
	task.status === 'failed' ||
	task.status === 'cancelled' ||
	task.status === 'unknown';
