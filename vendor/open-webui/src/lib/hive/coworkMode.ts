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

import type { TaskEvent, TaskPack } from './agentTasks';

export type ComposerMode = 'chat' | 'cowork';

export const COMPOSER_MODES: readonly ComposerMode[] = ['chat', 'cowork'];

export const isComposerMode = (value: unknown): value is ComposerMode =>
	value === 'chat' || value === 'cowork';

/*
 * The pack the composer will send (#1500).
 *
 * This used to be `packForMode()`, a function that took a mode and returned the
 * knowledge-work pack whatever it was given. The comment above it argued that
 * deriving the pack was safer than offering it, because the coding pack
 * "belongs to a Code panel this shell does not have". That reasoning did not
 * survive contact with the shipped product: the derivation made `coding-pack`
 * unreachable from the chat surface entirely, and the only place left that
 * could reach it was the `/agents` destination D-045 retires.
 *
 * HOW DIFFERENT THE TWO PACKS ACTUALLY ARE, so nobody re-derives it
 * ----------------------------------------------------------------
 * Less than the names suggest, and both pack manifests say so themselves.
 * `apps/agent-engine/packs/coding-pack/AGENTS.md` and its knowledge-work twin
 * both record that the two run at an identical sandbox trust tier, that both
 * may run arbitrary shell, build and test commands, and that "the difference
 * between packs is task framing and default tooling emphasis only". `Pack` in
 * apps/agent-engine/internal/sandbox/launcher.go selects one bind mount
 * (`ConfigDir` becomes `/opt/hive/pack`) and nothing else.
 *
 * The real difference is content: the knowledge-work pack ships three skills
 * (doc-layout, deck-generation, code-canvas) that publish artifacts, and the
 * coding pack ships none and frames the task around reading, editing and
 * testing a codebase in /workspace. So a coding-flavoured prompt succeeding
 * under the knowledge-work pack is expected rather than surprising, and is not
 * evidence that the choice is cosmetic.
 *
 * The labels are the words the /agents route already used. A customer never
 * reads `knowledge-work-pack`: the identifier stays on the wire, where the API
 * and the `public.agent_tasks` CHECK constraint want it.
 */
export interface ComposerPackOption {
	value: TaskPack;
	label: string;
}

export const COMPOSER_PACKS: readonly ComposerPackOption[] = [
	{ value: 'knowledge-work-pack', label: 'Knowledge work' },
	{ value: 'coding-pack', label: 'Coding' }
];

/*
 * Knowledge work, because that is what this composer sent before the control
 * existed. A different default would silently change what an existing user's
 * next submission does, which is a behaviour change dressed up as a new
 * control.
 */
export const DEFAULT_COMPOSER_PACK: TaskPack = 'knowledge-work-pack';

/**
 * Arrow-key movement inside a radiogroup.
 *
 * A `radiogroup` with two `radio` children announces as "Chat, selected, 1 of
 * 2" and moves with the arrow keys; two plain buttons announce as two
 * unrelated controls and move with Tab. The keyboard contract is the reason
 * for the role, so it is implemented rather than assumed, and it wraps at both
 * ends the way a native radio group does.
 *
 * Generic over the option list because the composer now has two of these
 * groups, the mode and the pack, with the same contract. One copy, so a fix to
 * the wrapping behaviour cannot land on one group and miss the other.
 */
const nextInGroup = <T>(options: readonly T[], current: T, key: string): T | null => {
	const index = options.indexOf(current);
	if (index === -1) {
		return null;
	}
	if (key === 'ArrowRight' || key === 'ArrowDown') {
		return options[(index + 1) % options.length];
	}
	if (key === 'ArrowLeft' || key === 'ArrowUp') {
		return options[(index - 1 + options.length) % options.length];
	}
	return null;
};

export const nextMode = (current: ComposerMode, key: string): ComposerMode | null =>
	nextInGroup(COMPOSER_MODES, current, key);

const PACK_VALUES: readonly TaskPack[] = COMPOSER_PACKS.map((option) => option.value);

export const nextPack = (current: TaskPack, key: string): TaskPack | null =>
	nextInGroup(PACK_VALUES, current, key);

/*
 * The projection of a run onto a transcript turn.
 *
 * This is the load-bearing half of #944: the claim underneath the whole change
 * is that a run and a conversation are the same kind of object, and this
 * function is where that claim is cashed. The assistant turn holds a run's
 * state while it is queued or running and its own result once it settles, so
 * one transcript component renders both kinds of turn with no branch in it.
 *
 * The turn's content is the run's own words: its state while it is queued or
 * running, its summary once it settles. The intermediate tool lines D-045
 * describes ("Used Claude in Chrome (2 actions)") are NOT content: they are
 * built by runSteps() below from the per-step event feed and ride on the same
 * `statusHistory` field the chat path already uses for "Searching the web",
 * which is why they render as muted lines with no new component and no branch
 * in the transcript.
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

export interface PendingCoworkTurn {
	id: string;
	hive_agent_task_id: string;
}

/**
 * Every assistant turn in a conversation that still carries a run id.
 *
 * A conversation can hold more than one run: the user can submit again in
 * Cowork mode once the first settles. loadChat also stamps every mid-flight
 * assistant turn `done = true` on reload, so a turn's local `done` flag
 * cannot be trusted to say whether its run actually finished; the caller has
 * to re-read every one of these from the server rather than picking the
 * first and assuming the rest are either absent or already done.
 */
export const selectPendingCoworkTurns = (
	messages: Record<string, unknown> | null | undefined
): PendingCoworkTurn[] =>
	Object.values(messages ?? {})
		.filter(
			(message): message is { id: string; role: string; hive_agent_task_id: string } =>
				typeof message === 'object' &&
				message !== null &&
				(message as { role?: unknown }).role === 'assistant' &&
				typeof (message as { hive_agent_task_id?: unknown }).hive_agent_task_id === 'string' &&
				(message as { hive_agent_task_id: string }).hive_agent_task_id !== '' &&
				typeof (message as { id?: unknown }).id === 'string'
		)
		.map((message) => ({ id: message.id, hive_agent_task_id: message.hive_agent_task_id }));

/* ---------------------------------------------------------------------- *
 * Per-step progress (#1193 follow-up)
 *
 * The run turn used to show nothing at all between submit and completion,
 * which reads as a hang, and the reason given for it was that the wire had no
 * per-step feed. It has had one since #1073: agent-engine emits real
 * tool_call / tool_result / message / error / status / file events, the
 * control-plane stores and serves them behind a cursor, edge-api exposes them
 * and the chat proxy already routes them. The only missing piece was here.
 *
 * WHAT IS AND IS NOT INVENTED HERE
 * --------------------------------
 * Every line below comes from an event the backend actually sent. There is no
 * optimistic step, no "thinking" placeholder and no synthesised progress: a
 * fabricated step is worse than a spinner, because a person will believe it.
 * The one thing this file adds that no single event carries is the PAIRING of
 * a tool_result back onto its tool_call, which is not invention: both events
 * carry the same tool_call_id precisely so they can be joined.
 * ---------------------------------------------------------------------- */

/**
 * One muted line in the transcript, in the shape `statusHistory` already
 * takes (see ResponseMessage.svelte's MessageType and StatusItem.svelte).
 *
 * `action` is deliberately a name no StatusItem branch matches, so the line
 * falls to that component's default branch and renders as plain muted text.
 * `done: false` is what makes the newest line shimmer while the step is still
 * running, which is the same treatment a web search gets in an ordinary chat
 * turn and the one piece of this surface the parity review found already
 * matched the reference product.
 */
export interface RunStep {
	action: 'hive_agent_step';
	description: string;
	done: boolean;
	/** Cursor position of the event that produced this line. */
	seq: number;
	/** Present on a tool step, so its result can find it. */
	tool_call_id?: string;
}

/*
 * maxPreviewRunes in apps/control-plane/internal/agenttask/events.go. Every
 * preview on the wire was cut to this many RUNES, with no marker left behind,
 * so a preview sitting exactly on the boundary is the only evidence that
 * anything was removed. Treating it as truncated over-reports by one preview
 * in a very large number; the alternative under-reports, and showing a
 * shortened tool result as though it were complete is the failure this whole
 * change is against.
 */
const PREVIEW_RUNE_CAP = 2000;

const asString = (payload: Record<string, unknown>, key: string): string => {
	const raw = payload[key];
	return typeof raw === 'string' ? raw : '';
};

/**
 * A preview, and whether the wire had already cut it.
 *
 * Array.from, not .length: the cap counts runes, and a surrogate pair is one
 * rune and two UTF-16 units.
 */
const preview = (payload: Record<string, unknown>): { text: string; shortened: boolean } => {
	const raw = asString(payload, 'preview').trim();
	if (raw === '') {
		return { text: '', shortened: false };
	}
	return { text: raw, shortened: Array.from(raw).length >= PREVIEW_RUNE_CAP };
};

/*
 * The truncation marker goes in FRONT of the text it is about, never after it.
 *
 * A step renders as a single clamped line, so a marker appended to a 2000-rune
 * preview is the first thing the clamp throws away and the line reads as a
 * complete tool result. That is the exact failure this marker exists to
 * prevent, and it was visible in the first capture of this surface.
 */
const SHORTENED = '(shortened)';

const withPreview = (label: string, payload: Record<string, unknown>): string => {
	const { text, shortened } = preview(payload);
	const head = shortened ? `${label} ${SHORTENED}` : label;
	return text === '' ? head : `${head}: ${text}`;
};

const toolLabel = (payload: Record<string, unknown>, verb: string): string => {
	const name = asString(payload, 'tool_name').trim();
	return name === '' ? `${verb} a tool` : `${verb} ${name}`;
};

/**
 * The sentence one event becomes, or null when it says nothing a person can
 * read, or nothing a person should have to read.
 *
 * Four cases return null. None of them silently drop information the user
 * needed: a `status` row carrying the task's own status duplicates the
 * turn's state, which is rendered from the task itself and would otherwise
 * appear twice; a `message` with an empty preview has no text to show; and a
 * `status` row that is neither a sandbox event nor a task-status echo, or an
 * event kind this build has never heard of, has no readable content at all.
 * That last pair used to render the literal apology "An update this version
 * of Hive cannot read.", which reached real users as a junk line in an
 * otherwise normal step list and told them nothing actionable. Filtered
 * instead, same as the other two null cases: an unreadable step is not
 * information.
 */
export const describeEvent = (event: TaskEvent): string | null => {
	const payload = event.payload;

	// capEventPayload replaces an oversized payload wholesale with this marker,
	// so it is checked before the kind: there is no preview left to read.
	if (payload['truncated'] === true) {
		const size = payload['size'];
		return typeof size === 'number'
			? `An update too large to show here (${size} bytes).`
			: 'An update too large to show here.';
	}

	switch (event.kind) {
		case 'tool_call':
			return withPreview(toolLabel(payload, 'Using'), payload);
		case 'tool_result':
			return withPreview(toolLabel(payload, 'Used'), payload);
		case 'message': {
			const { text, shortened } = preview(payload);
			if (text === '') {
				return null;
			}
			return shortened ? `${SHORTENED} ${text}` : text;
		}
		case 'error':
			return withPreview('Error', payload);
		case 'file': {
			const name = asString(payload, 'name').trim();
			return name === '' ? 'Wrote a file in the workspace.' : `Workspace file: ${name}`;
		}
		case 'status': {
			const sandboxKind = asString(payload, 'sandbox_kind').trim();
			if (sandboxKind !== '') {
				return `Sandbox event: ${sandboxKind}`;
			}
			if (asString(payload, 'status') !== '') {
				return null;
			}
			// Neither a sandbox event nor a task-status echo: nothing readable
			// left in this row. Filtered, not apologized for.
			return null;
		}
		default:
			// A kind this build has never heard of. Filtered rather than shown
			// as an apology: a step the user cannot act on does not belong in
			// the step list at all.
			return null;
	}
};

/**
 * Folds a page of new events onto the lines a turn already shows.
 *
 * Incremental rather than a recompute, because the read is incremental: the
 * follower asks for events after the highest seq it has, so the pages it gets
 * never contain a step that is already on screen and there is nothing to
 * rebuild. New array, new objects: the caller assigns the result onto the
 * message, and mutating the stored one in place would leave Svelte with no
 * change to notice.
 *
 * A tool_result closes the newest still-open step with its tool_call_id. When
 * there is none, which happens on a conversation reopened mid-run whose
 * earlier events are already behind the cursor, it becomes its own line
 * instead of being dropped.
 */
export const foldRunSteps = (previous: readonly RunStep[], events: readonly TaskEvent[]): RunStep[] => {
	const steps = previous.map((step) => ({ ...step }));

	for (const event of events) {
		if (event.kind === 'tool_result') {
			const callId = asString(event.payload, 'tool_call_id').trim();
			if (callId !== '') {
				const open = steps.map((s, i) => ({ s, i })).filter(({ s }) => s.tool_call_id === callId && !s.done);
				if (open.length > 0) {
					const { s, i } = open[open.length - 1];
					steps[i] = {
						...s,
						done: true,
						description: describeEvent(event) ?? s.description,
						seq: event.seq
					};
					continue;
				}
			}
		}

		const description = describeEvent(event);
		if (description === null) {
			continue;
		}
		const step: RunStep = {
			action: 'hive_agent_step',
			description,
			// A tool call is the only step that stays open, because it is the
			// only one with a second event coming that closes it.
			done: event.kind !== 'tool_call',
			seq: event.seq
		};
		if (event.kind === 'tool_call') {
			const callId = asString(event.payload, 'tool_call_id').trim();
			if (callId !== '') {
				step.tool_call_id = callId;
			}
		}
		steps.push(step);
	}

	return steps;
};

/**
 * The cursor a turn resumes from: the highest seq any of its lines carries.
 *
 * Read off the stored lines rather than kept in a variable, so a conversation
 * reopened in a new tab asks for the events it has not seen instead of
 * re-reading the whole run from zero and re-rendering lines it already shows.
 * A step written before this field existed contributes 0, which re-reads and
 * is merely wasteful, never wrong.
 */
export const latestStepSeq = (steps: readonly RunStep[] | null | undefined): number => {
	let highest = 0;
	for (const step of steps ?? []) {
		if (typeof step?.seq === 'number' && Number.isFinite(step.seq) && step.seq > highest) {
			highest = step.seq;
		}
	}
	return highest;
};

/**
 * Drops the step that is only an echo of the turn's own content (#1509).
 *
 * A finished run used to say the same sentence twice, and the duplication was
 * not one payload appended twice: it was one payload arriving by two routes and
 * rendered by two components. The sandbox's closing assistant message reaches
 * this turn as a `message` event, which describeEvent turns into a step line,
 * AND as the task's `result_summary_ref`, which agent-engine fills from
 * FinalResponse and renderRun puts in the turn's content. Neither route is
 * wrong on its own: the event feed is the progress record and the final
 * response is the result. Rendering both is what is wrong, so the echo is
 * dropped here rather than either emission being suppressed upstream.
 *
 * Applied only once the run has settled, which is the only moment the duplicate
 * exists: while it is still going the turn's content is a status line, not the
 * summary, and nothing matches.
 *
 * Only the LAST match goes. An intermediate message that happened to repeat the
 * closing sentence is still a thing the agent said twice, and the step list is
 * where that is visible.
 *
 * The comparison is on the text because the wire carries nothing else to join
 * on: the event and the final response come from two different endpoints and
 * share no identifier. It is whitespace-insensitive, because the two routes
 * assemble the same message differently: normalizeEvent (agent-engine's
 * controlclient/events.go) joins an llm_message's content blocks with a single
 * space, while the final response keeps the message's own line breaks. Nothing
 * else is relaxed, so the rule's failure mode is that it does nothing and the
 * duplicate stays visible, never that it removes a line whose text is not
 * already on screen directly underneath it.
 *
 * One narrow prefix case on top of equality: a preview is cut at
 * maxPreviewRunes on the way out (events.go) and describeEvent marks such a
 * line, so a long closing message arrives here as a marked prefix of the
 * summary. The prefix comparison is applied ONLY to a line carrying that
 * marker, so an ordinary short step can never be dropped for accidentally
 * being the beginning of the summary.
 *
 * The equality branch is not scoped to a message step, and cannot be: RunStep
 * stores no event kind, every line carries the same `hive_agent_step` action.
 * So a tool_result, error or file step whose flattened preview is byte
 * identical to the flattened summary would also go. That is unlikely and it is
 * not harmful: the only line this drops is one whose text is already rendered
 * directly underneath it. (The prefix branch is incidentally narrower, because
 * describeEvent puts the marker at position 0 only for a message; every other
 * kind formats it as label, marker, colon, text.)
 *
 * One accepted side effect: latestStepSeq reads the lines the turn still holds,
 * so the dropped event sits above the stored cursor and a conversation reopened
 * later re-reads that one event, folds the echo back in, and drops it again in
 * the same pass. The persisted result is identical; the cost is one small
 * request per reopen, which is cheaper than carrying a second cursor field on
 * every turn to remember a line nobody should see.
 */
const flatten = (value: string): string => (value ?? '').replace(/\s+/g, ' ').trim();

const echoesSummary = (description: string, summary: string): boolean => {
	const text = flatten(description);
	if (text === '') {
		return false;
	}
	if (text === summary) {
		return true;
	}
	if (text.startsWith(`${SHORTENED} `)) {
		const head = text.slice(SHORTENED.length + 1).trim();
		return head !== '' && summary.startsWith(head);
	}
	return false;
};

export const dropSummaryEcho = (steps: readonly RunStep[], content: string): RunStep[] => {
	const summary = flatten(content);
	if (summary === '') {
		return [...steps];
	}
	for (let index = steps.length - 1; index >= 0; index--) {
		if (echoesSummary(steps[index].description, summary)) {
			return [...steps.slice(0, index), ...steps.slice(index + 1)];
		}
	}
	return [...steps];
};

/**
 * Closes any step still shimmering once the run itself has settled.
 *
 * A tool call whose result never arrived (a cancelled run, a sandbox that
 * died) would otherwise shimmer forever under a turn that says the task
 * finished, which claims work is still happening after the run is over. The
 * text is left exactly as it was: what is known is that the step stopped, not
 * that it succeeded.
 */
export const settleRunSteps = (steps: readonly RunStep[]): RunStep[] =>
	steps.map((step) => (step.done ? { ...step } : { ...step, done: true }));
