<script lang="ts">
	import { getContext, onDestroy, onMount } from 'svelte';

	import { WEBUI_API_BASE_URL } from '$lib/constants';

	import ComposerShell from './ComposerShell.svelte';
	import ComposerSendButton from './ComposerSendButton.svelte';
	import {
		canStartTask,
		cancelTask,
		createTask,
		describeRefusal,
		describeTask,
		elapsed,
		IN_FLIGHT_STATUSES,
		isEngineUnavailable,
		listTasks,
		TERMINAL_STATUSES,
		type AgentTask,
		type Refusal,
		type TaskPack
	} from './agentTasks';

	const i18n: any = getContext('i18n');

	/*
	 * The agent surface, native.
	 *
	 * This replaced an <iframe> that booted apps/agent-console, a second whole
	 * application, inside the shell. Function is at parity with what that frame
	 * showed; layout deliberately is not. The composer here IS the chat
	 * composer's container and send button (ComposerShell.svelte,
	 * ComposerSendButton.svelte, both extracted from MessageInput.svelte), so
	 * the two surfaces cannot drift into looking like two products.
	 *
	 * Not built here, on purpose, and separately specified: tool call cards, the
	 * progress panel and the transcript. All three need an event relay that does
	 * not exist yet (spec-2026-08-17-agent-run-surface, step S6), and a card with
	 * nothing to render is worse than today's status row.
	 */

	const POLL_INTERVAL_MS = 3000;
	const MAX_POLL_INTERVAL_MS = 30_000;
	const MAX_POLL_FAILURES = 5;

	// edge-api reads the create body through io.LimitReader(r.Body, 64<<10). This
	// cap is far below that: it keeps the composer honest about what a task brief
	// should be, it does not guard the wire.
	const MAX_INSTRUCTIONS = 4000;

	const PACKS: ReadonlyArray<{ value: TaskPack; label: string; hint: string }> = [
		{
			value: 'knowledge-work-pack',
			label: 'Knowledge work',
			hint: 'Researches, reads documents, and writes up an answer.'
		},
		{
			value: 'coding-pack',
			label: 'Coding',
			hint: 'Reads and edits a codebase, runs commands, and reports what changed.'
		}
	];

	const PACK_LABELS: Record<TaskPack, string> = {
		'coding-pack': 'Coding',
		'knowledge-work-pack': 'Knowledge work'
	};

	let tasks: AgentTask[] = [];
	let instructions = '';
	// Unchanged from the surface this replaces. The toggle's order changed, its
	// default did not: this is a presentation change, not a capability change.
	let pack: TaskPack = 'coding-pack';
	let submitting = false;
	let invalid = false;
	let error: string | null = null;
	let announcement = '';
	let failures = 0;
	/*
	 * A refusal no amount of retrying can change, carrying the server's own
	 * sentence. 401 means this session cannot reach the agent service at all;
	 * 403 means this tenant does not hold the Cowork gate. Both are separated
	 * from the ordinary failure count because the ordinary copy promises a
	 * retry, and a promise the loop cannot keep is the exact failure this
	 * surface exists to stop making.
	 */
	let blocked: Refusal | null = null;
	let nowMs = Date.now();

	let promptEl: HTMLTextAreaElement | null = null;
	let pollTimer: ReturnType<typeof setTimeout> | null = null;
	let clockTimer: ReturnType<typeof setInterval> | null = null;
	let destroyed = false;
	/*
	 * Bumped by every local change to `tasks` that did not come from a list
	 * response: a create, a cancel. A refresh captures it before its request
	 * goes out and drops its own answer if it changed while the request was in
	 * flight, because `tasks = await listTasks(...)` replaces the array whole
	 * and a poll that started before a create knows nothing about the new row.
	 * Without this, a poll overlapping a create loses the row the user just
	 * submitted, or reverts a row they just cancelled, and if that stale list
	 * happens to hold no in-flight task then schedulePoll stops too and the
	 * screen never recovers on its own.
	 */
	let mutations = 0;

	$: selectedPack = PACKS.find((option) => option.value === pack) ?? PACKS[0];
	$: givenUp = failures >= MAX_POLL_FAILURES;
	/*
	 * The load failure reads off the failure count rather than being written into
	 * `error`, so the sentence cannot outlive the retry it promises: when the loop
	 * gives up, the copy stops claiming a retry is coming.
	 */
	$: loadFailure =
		failures === 0
			? null
			: givenUp
				? 'Could not load your tasks. Reload the page to try again.'
				: 'Could not load your tasks. Retrying automatically.';
	// A stale list outranks a one-off create or cancel error.
	// A refusal outranks both, because it is the only one of the three that is
	// still true after another attempt.
	$: alertMessage = blocked?.message ?? loadFailure ?? error;
	/*
	 * Current state, not history. `tasks` is newest first, so the newest task is
	 * the only one that says anything about how this deployment is configured
	 * right now. Asking whether ANY task was ever engine-blocked made the notice
	 * permanent, and a run that succeeded sat under a banner saying there was no
	 * sandbox to run it in.
	 */
	$: engineUnavailable = tasks.length > 0 && isEngineUnavailable(tasks[0]);
	$: nearLimit = instructions.length > MAX_INSTRUCTIONS * 0.8;
	$: canSubmit = canStartTask({ instructions, submitting, blocked });

	const sessionToken = (): string => localStorage.token ?? '';

	// The one SvelteKit-resolved value the API module deliberately does not
	// import for itself, so its unit tests can load at all. See the note at the
	// top of agentTasks.ts. Passing it from here keeps `npm run dev` against the
	// chat front end pointed at the backend's own port.
	const apiBase = `${WEBUI_API_BASE_URL}/hive/agent`;

	const schedulePoll = () => {
		if (pollTimer) {
			clearTimeout(pollTimer);
			pollTimer = null;
		}
		// A refusal is not retried. Polling through a 401 or a 403 would issue a
		// request every few seconds forever against an answer that cannot change.
		if (destroyed || blocked !== null) {
			return;
		}
		/*
		 * Decided here from the current values rather than read off the `$:`
		 * statements above. Svelte flushes reactive statements after the current
		 * synchronous block, so a schedulePoll() called immediately after
		 * assigning `tasks` or `failures` would decide on the previous values,
		 * and the first poll after a submit would stop the loop that the newly
		 * queued task is the whole reason to keep running.
		 */
		const stopped = failures >= MAX_POLL_FAILURES;
		const anyInFlight = tasks.some((task) => IN_FLIGHT_STATUSES.has(task.status));
		// Only a queued or running task can change without the user doing
		// anything, so a healthy list of finished tasks stops polling entirely.
		// A failure run keeps polling until it gives up, because the list on
		// screen may be stale rather than settled.
		if (!(failures > 0 ? !stopped : anyInFlight)) {
			return;
		}
		/*
		 * One self-rescheduling timeout rather than setInterval or a websocket:
		 * demo-scale task counts, and the sync contract ships no push channel yet.
		 * Each consecutive failure doubles the wait, and after MAX_POLL_FAILURES
		 * the loop stops and the screen says so rather than promising a retry that
		 * never comes.
		 */
		const delay = Math.min(POLL_INTERVAL_MS * 2 ** failures, MAX_POLL_INTERVAL_MS);
		pollTimer = setTimeout(() => {
			void refresh();
		}, delay);
	};

	const refresh = async () => {
		const startedAt = mutations;
		try {
			const fetched = await listTasks(sessionToken(), apiBase);
			// A create or a cancel landed while this request was open, so its
			// answer predates a change the user made and must not overwrite it.
			// Nothing else is rolled back: the request did reach the endpoint, so
			// the failure count and any refusal are genuinely cleared, and the
			// next poll fetches a list that includes the change.
			if (startedAt === mutations) {
				tasks = fetched;
			}
			error = null;
			blocked = null;
			failures = 0;
		} catch (e) {
			const refusal = describeRefusal(e);
			if (refusal) {
				blocked = refusal;
				failures = 0;
			} else {
				failures = failures + 1;
			}
		}
		nowMs = Date.now();
		schedulePoll();
	};

	const submit = async () => {
		// A refused surface must not take a submission it cannot deliver. The
		// control is disabled too; this is the guard for the keyboard path.
		if (submitting || blocked !== null) {
			return;
		}
		const trimmed = instructions.trim();
		if (!trimmed) {
			// Hand-rolled rather than the `required` attribute: the native bubble
			// cannot be styled and disappears on the next click, which is worse than
			// an inline message the user can re-read.
			invalid = true;
			error = null;
			promptEl?.focus();
			return;
		}

		invalid = false;
		submitting = true;
		error = null;
		announcement = 'Starting task.';

		try {
			const task = await createTask(sessionToken(), pack, trimmed, [], apiBase);
			// A create that round-trips is direct evidence the endpoint is back, so
			// the failure count is stale. Without this the poll stays given up and
			// the row the user just created sits under an alert telling them to
			// reload, which the create just disproved.
			failures = 0;
			// A create that round-trips also disproves a refusal, so the refusal
			// message goes with the failure count rather than outliving both.
			blocked = null;
			mutations = mutations + 1;
			// Deduplicated on id, which the mutation counter above cannot cover: it
			// catches a refresh that lands AFTER the create, while this catches one
			// that landed BEFORE the create response reached the browser but after
			// the server had already committed the row. In that window the counter
			// has not moved yet, so the refresh is applied, and prepending blindly
			// would put the same id in a keyed list twice.
			tasks = [task, ...tasks.filter((existing) => existing.id !== task.id)];
			instructions = '';
			resize();
			announcement = `Task submitted. Status: ${describeTask(task, Date.now()).label}.`;
			schedulePoll();
		} catch (e) {
			error =
				e instanceof Error && e.message
					? e.message
					: 'Could not start the task. Your text is still here, try again.';
			announcement = '';
		} finally {
			submitting = false;
		}
	};

	const cancel = async (id: string) => {
		// A polite region only speaks when its text changes, so cancelling a second
		// task would otherwise be silent. Clearing before the await puts a real
		// transition between the two identical sentences.
		announcement = '';
		try {
			const updated = await cancelTask(sessionToken(), id, apiBase);
			mutations = mutations + 1;
			tasks = tasks.map((task) => (task.id === updated.id ? updated : task));
			announcement = 'Task cancelled.';
			// Cancelling the last in-flight task should stop the loop now rather
			// than after one more request against a list that cannot change.
			schedulePoll();
		} catch {
			error = 'Could not cancel that task.';
		}
	};

	const onKeyDown = (event: KeyboardEvent) => {
		/*
		 * Enter sends, Shift+Enter is a newline. This is a deliberate change from
		 * the surface being replaced, which used Ctrl or Cmd plus Enter and left
		 * plain Enter to insert a newline "because a task brief is prose and often
		 * more than one line". That reasoning was sound for a form. It stops
		 * applying here: the owner's requirement is that this reads as the same
		 * control as the chat composer, and a control that looks identical and
		 * answers the same key differently is worse than one that looks different.
		 * Shift+Enter still writes the multi-line brief the old comment was
		 * protecting, and `isComposing` keeps an IME's own Enter out of it.
		 */
		if (event.key === 'Enter' && !event.shiftKey && !event.isComposing) {
			event.preventDefault();
			void submit();
		}
	};

	// Grows with its content up to the CSS max-height, then scrolls. Chat's
	// composer does the same, and a task brief is usually more than one line.
	const resize = () => {
		if (!promptEl) {
			return;
		}
		promptEl.style.height = '';
		promptEl.style.height = `${promptEl.scrollHeight}px`;
	};

	onMount(() => {
		void refresh();
		// Relative timestamps go stale in place otherwise, and a row reading "less
		// than a minute ago" an hour later is a row that lies quietly.
		clockTimer = setInterval(() => {
			nowMs = Date.now();
		}, 30_000);
	});

	onDestroy(() => {
		destroyed = true;
		if (pollTimer) clearTimeout(pollTimer);
		if (clockTimer) clearInterval(clockTimer);
	});
</script>

<div class="hv-agents">
	<!--
		One polite live region for the whole screen. Per-row aria-live was the
		previous approach and it re-announced every unchanged status on each poll.
	-->
	<p aria-live="polite" class="sr-only">{announcement}</p>

	{#if engineUnavailable}
		<div role="status" class="hv-agent-notice">
			<p class="hv-agent-notice-title">
				{$i18n.t('The agent runtime is not configured on this deployment')}
			</p>
			<p class="hv-agent-notice-body">
				{$i18n.t(
					'Tasks you submit are saved against your account, but there is no sandbox to run them in, so each one is marked blocked as soon as it is submitted. An administrator needs to configure the agent runtime for this deployment.'
				)}
			</p>
		</div>
	{/if}

	<form on:submit|preventDefault={submit}>
		<ComposerShell>
			<div class="px-2.5 pt-2.5">
				<label class="sr-only" for="hive-agent-instructions">
					{$i18n.t('Describe the task')}
				</label>
				<textarea
					id="hive-agent-instructions"
					bind:this={promptEl}
					bind:value={instructions}
					on:input={() => {
						if (invalid) invalid = false;
						resize();
					}}
					on:keydown={onKeyDown}
					rows="1"
					maxlength={MAX_INSTRUCTIONS}
					aria-required="true"
					aria-invalid={invalid}
					aria-describedby={invalid ? 'hive-agent-error hive-agent-pack-hint' : 'hive-agent-pack-hint'}
					disabled={blocked !== null}
					placeholder={blocked
						? $i18n.t('Tasks cannot be started right now')
						: $i18n.t('Describe the task in your own words')}
					class="hv-agent-input scrollbar-hidden"
				></textarea>
			</div>

			<div class="hv-agent-row">
				<!--
					aria-describedby points at the one line under the composer, so a
					screen reader hears what the selected mode does when it lands on
					this group. The surface this replaces wired the same relationship
					and losing it would have made the hint sighted-only.
				-->
				<fieldset class="hv-agent-packs" aria-describedby="hive-agent-pack-hint">
					<legend class="sr-only">{$i18n.t('Kind of task')}</legend>
					{#each PACKS as option (option.value)}
						<label class="hv-agent-pack">
							<input
								type="radio"
								name="hive-agent-pack"
								value={option.value}
								checked={pack === option.value}
								disabled={blocked !== null}
								on:change={() => (pack = option.value)}
								class="sr-only"
							/>
							<span class="hv-agent-pack-label">{$i18n.t(option.label)}</span>
						</label>
					{/each}
				</fieldset>

				<div class="hv-agent-send">
					{#if nearLimit}
						<span class="hv-agent-count">{instructions.length}/{MAX_INSTRUCTIONS}</span>
					{/if}
					<ComposerSendButton
						id="hive-agent-send"
						disabled={!canSubmit}
						pending={submitting}
						label={$i18n.t('Start task')}
					/>
				</div>
			</div>
		</ComposerShell>
	</form>

	<!--
		The one line of guidance that survives. It says what the selected mode does,
		it changes with the toggle, and it is deliberately the only prose near the
		composer: a composer needs no instructions.
	-->
	<p id="hive-agent-pack-hint" class="hv-agent-hint">{$i18n.t(selectedPack.hint)}</p>

	{#if invalid}
		<p id="hive-agent-error" role="alert" class="hv-agent-alert hv-agent-alert-inline">
			{$i18n.t('Describe the task first. The agent needs a goal to work from.')}
		</p>
	{/if}

	{#if alertMessage}
		<p role="alert" class="hv-agent-alert">{alertMessage}</p>
	{/if}

	<section aria-labelledby="hive-agent-list">
		<h2 id="hive-agent-list" class="sr-only">{$i18n.t('Your tasks')}</h2>
		{#if tasks.length === 0}
			<div class="hv-agent-empty">
				<p class="hv-agent-empty-title">{$i18n.t('Nothing submitted yet')}</p>
				<p class="hv-agent-empty-body">
					{$i18n.t(
						'Tasks you start appear here with their status, and stay here when you come back in another session.'
					)}
				</p>
			</div>
		{:else}
			<ul class="hv-agent-rows">
				{#each tasks as task (task.id)}
					{@const view = describeTask(task, nowMs)}
					<!-- Stable hook for the end-to-end spec, which asserts that every
					     row the API returned actually painted. A styling class would
					     do the job until someone renames it. -->
					<li class="hv-agent-task" data-hive-task-row={task.id}>
						<div class="hv-agent-task-head">
							<!--
								The brief the user wrote is the row's headline. Three rows on the
								deployed list have none, because they predate the goal field, so
								they say so rather than rendering an empty line that reads as a
								broken row.
							-->
							<p class="hv-agent-task-title">
								{#if task.instructions}
									{task.instructions}
								{:else}
									<span class="hv-agent-task-untitled">
										{$i18n.t('No description was recorded for this task.')}
									</span>
								{/if}
							</p>
							<span class="hv-agent-pill" data-tone={view.tone}>
								<span aria-hidden="true" class="hv-agent-dot" class:hv-agent-dot-live={view.live}
								></span>
								{$i18n.t(view.label)}
							</span>
						</div>

						<p class="hv-agent-task-detail">{view.detail}</p>

						{#if task.result_summary_ref}
							<p class="hv-agent-task-result">
								{$i18n.t('Result')}: {task.result_summary_ref}
							</p>
						{/if}

						<div class="hv-agent-task-meta">
							<span>{$i18n.t(PACK_LABELS[task.pack])} &middot; {elapsed(task.created_at, nowMs)}</span>
							{#if !TERMINAL_STATUSES.has(task.status)}
								<button type="button" class="hv-agent-cancel" on:click={() => cancel(task.id)}>
									{$i18n.t('Cancel')}
								</button>
							{/if}
						</div>
					</li>
				{/each}
			</ul>
		{/if}
	</section>
</div>

<style>
	/*
	 * Every value here is a Hive design token. Nothing is a hard-coded colour,
	 * radius or step, so this surface moves with the rest of the product rather
	 * than beside it. The composer's own container and send button are not styled
	 * here at all: they are the chat composer's, shared as components.
	 */
	.hv-agents {
		display: flex;
		flex-direction: column;
		gap: var(--hv-space-4);
		width: 100%;
		max-width: 48rem;
		margin: 0 auto;
		padding: var(--hv-space-6) var(--hv-space-4) var(--hv-space-10);
	}

	.hv-agent-input {
		width: 100%;
		resize: none;
		border: 0;
		background: transparent;
		outline: none;
		color: var(--hv-ink);
		font-size: var(--hv-text-base);
		line-height: var(--hv-leading-base);
		max-height: 24rem;
		overflow-y: auto;
	}

	.hv-agent-input::placeholder {
		color: var(--hv-ink-muted);
	}

	/* Refused, and it should look it. A control that reads as live and then
	   does nothing is worse than one that says it is unavailable. */
	.hv-agent-input:disabled {
		color: var(--hv-ink-disabled);
		cursor: not-allowed;
	}

	.hv-agent-pack input:disabled + .hv-agent-pack-label {
		color: var(--hv-ink-disabled);
		cursor: not-allowed;
	}

	.hv-agent-row {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: var(--hv-space-3);
		margin: var(--hv-space-2) var(--hv-space-1) var(--hv-space-3);
	}

	.hv-agent-packs {
		display: flex;
		gap: 2px;
		padding: 2px;
		border: 0;
		margin: 0;
		border-radius: var(--hv-radius-pill);
		background: var(--hv-surface-sunken);
	}

	.hv-agent-pack {
		cursor: pointer;
	}

	.hv-agent-pack-label {
		display: block;
		padding: 0.3rem 0.75rem;
		border-radius: var(--hv-radius-pill);
		font-size: var(--hv-text-xs);
		line-height: var(--hv-leading-xs);
		font-weight: var(--hv-weight-label);
		color: var(--hv-ink-secondary);
		transition: background-color var(--hv-duration-fast) var(--hv-ease-out);
	}

	.hv-agent-pack input:checked + .hv-agent-pack-label {
		background: var(--hv-surface);
		color: var(--hv-ink);
		box-shadow: var(--hv-shadow-1);
	}

	.hv-agent-pack input:focus-visible + .hv-agent-pack-label {
		outline: var(--hv-focus-ring);
		outline-offset: 2px;
	}

	.hv-agent-send {
		display: flex;
		align-items: center;
		gap: var(--hv-space-2);
	}

	.hv-agent-count {
		font-family: var(--hv-font-mono);
		font-size: var(--hv-text-2xs);
		color: var(--hv-ink-muted);
	}

	.hv-agent-hint {
		margin: 0;
		padding-inline: var(--hv-space-3);
		font-size: var(--hv-text-xs);
		line-height: var(--hv-leading-xs);
		color: var(--hv-ink-muted);
	}

	/*
	 * Ink on a tinted ground, never coloured text. The danger hue measures about
	 * 3.8:1 against the surface in light and 3.5:1 in dark, so as text at this
	 * size it fails WCAG AA in both. Hue moves to the border and the ground and
	 * the text stays readable, which is also what 1.4.1 asks for: the word
	 * carries the meaning and the colour reinforces it.
	 */
	.hv-agent-alert {
		margin: 0;
		border-left: 2px solid var(--hv-danger);
		border-radius: var(--hv-radius-sm);
		background: var(--hv-danger-soft);
		padding: var(--hv-space-3) var(--hv-space-4);
		font-size: var(--hv-text-sm);
		line-height: var(--hv-leading-sm);
		color: var(--hv-ink);
	}

	.hv-agent-alert-inline {
		font-size: var(--hv-text-xs);
		padding: var(--hv-space-2) var(--hv-space-3);
	}

	.hv-agent-notice {
		display: flex;
		flex-direction: column;
		gap: var(--hv-space-1);
		border: 1px solid var(--hv-border);
		border-left: 2px solid var(--hv-warning);
		border-radius: var(--hv-radius-md);
		background: var(--hv-warning-soft);
		padding: var(--hv-space-3) var(--hv-space-4);
	}

	.hv-agent-notice-title {
		margin: 0;
		font-size: var(--hv-text-sm);
		font-weight: var(--hv-weight-label);
		color: var(--hv-ink);
	}

	.hv-agent-notice-body {
		margin: 0;
		font-size: var(--hv-text-xs);
		line-height: var(--hv-leading-xs);
		color: var(--hv-ink-secondary);
	}

	.hv-agent-empty {
		display: flex;
		flex-direction: column;
		align-items: center;
		gap: var(--hv-space-1);
		border: 1px solid var(--hv-border);
		border-radius: var(--hv-radius-md);
		background: var(--hv-surface);
		padding: var(--hv-space-12) var(--hv-space-6);
		text-align: center;
	}

	.hv-agent-empty-title {
		margin: 0;
		font-size: var(--hv-text-sm);
		font-weight: var(--hv-weight-label);
		color: var(--hv-ink);
	}

	.hv-agent-empty-body {
		margin: 0;
		max-width: 22rem;
		font-size: var(--hv-text-xs);
		line-height: var(--hv-leading-xs);
		color: var(--hv-ink-secondary);
	}

	.hv-agent-rows {
		display: flex;
		flex-direction: column;
		margin: 0;
		padding: 0;
		list-style: none;
		border: 1px solid var(--hv-border);
		border-radius: var(--hv-radius-md);
		background: var(--hv-surface);
		overflow: hidden;
	}

	.hv-agent-task {
		display: flex;
		flex-direction: column;
		gap: var(--hv-space-2);
		padding: var(--hv-space-4);
	}

	.hv-agent-task + .hv-agent-task {
		border-top: 1px solid var(--hv-border);
	}

	.hv-agent-task-head {
		display: flex;
		align-items: flex-start;
		justify-content: space-between;
		gap: var(--hv-space-4);
	}

	.hv-agent-task-title {
		margin: 0;
		min-width: 0;
		flex: 1 1 auto;
		font-size: var(--hv-text-sm);
		line-height: var(--hv-leading-sm);
		color: var(--hv-ink);
	}

	.hv-agent-task-untitled {
		color: var(--hv-ink-muted);
		font-style: italic;
	}

	.hv-agent-pill {
		display: inline-flex;
		flex: none;
		align-items: center;
		gap: 0.375rem;
		border-radius: var(--hv-radius-pill);
		padding: 0.25rem 0.625rem;
		font-size: var(--hv-text-2xs);
		line-height: var(--hv-leading-2xs);
		font-weight: var(--hv-weight-label);
		color: var(--hv-ink);
		background: var(--hv-surface-sunken);
	}

	.hv-agent-pill[data-tone='accent'] {
		background: var(--hv-accent-soft);
	}
	.hv-agent-pill[data-tone='success'] {
		background: var(--hv-success-soft);
	}
	.hv-agent-pill[data-tone='warning'] {
		background: var(--hv-warning-soft);
	}
	.hv-agent-pill[data-tone='danger'] {
		background: var(--hv-danger-soft);
	}

	.hv-agent-dot {
		width: 0.375rem;
		height: 0.375rem;
		flex: none;
		border-radius: 50%;
		background: var(--hv-ink-muted);
	}

	.hv-agent-pill[data-tone='accent'] .hv-agent-dot {
		background: var(--hv-accent);
	}
	.hv-agent-pill[data-tone='success'] .hv-agent-dot {
		background: var(--hv-success);
	}
	.hv-agent-pill[data-tone='warning'] .hv-agent-dot {
		background: var(--hv-warning);
	}
	.hv-agent-pill[data-tone='danger'] .hv-agent-dot {
		background: var(--hv-danger);
	}

	/*
	 * The one permitted pulse on this surface, and only because there is at most
	 * a handful of running rows and it answers the only question a long run
	 * raises. It oscillates opacity and never moves, so it is not a spinner.
	 */
	.hv-agent-dot-live {
		animation: hv-agent-pulse 1200ms var(--hv-ease-in-out) infinite;
	}

	@keyframes hv-agent-pulse {
		0%,
		100% {
			opacity: 1;
		}
		50% {
			opacity: 0.45;
		}
	}

	@media (prefers-reduced-motion: reduce) {
		.hv-agent-dot-live {
			animation: none;
		}
		.hv-agent-pack-label {
			transition: none;
		}
	}

	.hv-agent-task-detail {
		margin: 0;
		font-size: var(--hv-text-xs);
		line-height: var(--hv-leading-xs);
		color: var(--hv-ink-secondary);
	}

	.hv-agent-task-result {
		margin: 0;
		font-family: var(--hv-font-mono);
		font-size: var(--hv-text-xs);
		word-break: break-all;
		color: var(--hv-ink-secondary);
	}

	.hv-agent-task-meta {
		display: flex;
		align-items: center;
		gap: var(--hv-space-3);
		font-family: var(--hv-font-mono);
		font-size: var(--hv-text-2xs);
		color: var(--hv-ink-muted);
	}

	.hv-agent-cancel {
		border: 1px solid var(--hv-border);
		border-radius: var(--hv-radius-sm);
		background: transparent;
		padding: 0.125rem 0.5rem;
		font-family: var(--hv-font-sans);
		font-size: var(--hv-text-2xs);
		color: var(--hv-ink-secondary);
		cursor: pointer;
	}

	.hv-agent-cancel:hover {
		background: var(--hv-surface-sunken);
		color: var(--hv-ink);
	}

	.hv-agent-cancel:focus-visible {
		outline: var(--hv-focus-ring);
		outline-offset: 2px;
	}
</style>
