import { readFileSync } from 'node:fs';
import { dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

import { describe, expect, it } from 'vitest';

import {
	COMPOSER_MODES,
	COMPOSER_PACKS,
	DEFAULT_COMPOSER_PACK,
	describeEvent,
	dropSummaryEcho,
	foldRunSteps,
	isComposerMode,
	latestStepSeq,
	nextMode,
	nextPack,
	renderRun,
	runTurnIsDone,
	selectPendingCoworkTurns,
	settleRunSteps,
	type RunStep
} from './coworkMode';
import { isTaskPack, type TaskEvent } from './agentTasks';

const here = dirname(fileURLToPath(import.meta.url));
const readComponent = (relative: string): string =>
	readFileSync(resolve(here, relative), 'utf8');

describe('composer mode', () => {
	it('offers exactly the two modes D-045 names, in the reference order', () => {
		expect(COMPOSER_MODES).toEqual(['chat', 'cowork']);
	});

	it('rejects anything that is not one of them', () => {
		expect(isComposerMode('chat')).toBe(true);
		expect(isComposerMode('cowork')).toBe(true);
		expect(isComposerMode('agents')).toBe(false);
		expect(isComposerMode(undefined)).toBe(false);
	});

});

/*
 * #1500. The pack used to be derived rather than chosen, and the assertion that
 * replaced this one said so approvingly. That was the defect: `coding-pack` was
 * unreachable from the chat surface entirely, and the only route that could
 * reach it was the `/agents` destination D-045 retires. The pack is now offered.
 */
describe('the pack the composer will send', () => {
	it('offers both packs the wire accepts, knowledge work first', () => {
		expect(COMPOSER_PACKS.map((option) => option.value)).toEqual([
			'knowledge-work-pack',
			'coding-pack'
		]);
	});

	it('offers only values the API and the database CHECK constraint accept', () => {
		for (const option of COMPOSER_PACKS) {
			expect(isTaskPack(option.value)).toBe(true);
		}
	});

	it('labels the segments in words rather than in wire identifiers', () => {
		// A customer never reads `knowledge-work-pack`. These are the same two
		// words the /agents route already used, so retiring that route in #1501
		// changes no vocabulary anyone has learned.
		expect(COMPOSER_PACKS.map((option) => option.label)).toEqual(['Knowledge work', 'Coding']);
		for (const option of COMPOSER_PACKS) {
			expect(option.label).not.toContain('-pack');
		}
	});

	it('defaults to what the composer already sent, so ignoring the control changes nothing', () => {
		expect(DEFAULT_COMPOSER_PACK).toBe('knowledge-work-pack');
		expect(COMPOSER_PACKS[0].value).toBe(DEFAULT_COMPOSER_PACK);
	});

	it('the store the composer actually reads carries that same default', () => {
		// Review finding on #1518: the store lives in $lib/stores rather than
		// here, because that module is reached by everything and importing
		// lib/hive from it would invert the dependency. The default was
		// therefore hand-duplicated, and the assertion above only checked the
		// constant against itself. This pins the copy against the original, so
		// changing one without the other goes red.
		const stores = readComponent('../stores/index.ts');
		const declaration = stores.slice(stores.indexOf('export const composerPack'));
		expect(declaration).toContain(`writable('${DEFAULT_COMPOSER_PACK}')`);
		// And that the store offers exactly the packs the control offers, so a
		// third pack cannot reach the wire without appearing in the row.
		for (const option of COMPOSER_PACKS) {
			expect(declaration.slice(0, declaration.indexOf('writable('))).toContain(
				`'${option.value}'`
			);
		}
	});
});

describe('nextPack', () => {
	it('moves forward and back with the arrow keys a radiogroup answers to', () => {
		expect(nextPack('knowledge-work-pack', 'ArrowRight')).toBe('coding-pack');
		expect(nextPack('knowledge-work-pack', 'ArrowDown')).toBe('coding-pack');
		expect(nextPack('coding-pack', 'ArrowLeft')).toBe('knowledge-work-pack');
		expect(nextPack('coding-pack', 'ArrowUp')).toBe('knowledge-work-pack');
	});

	it('wraps at both ends, as a native radio group does', () => {
		expect(nextPack('coding-pack', 'ArrowRight')).toBe('knowledge-work-pack');
		expect(nextPack('knowledge-work-pack', 'ArrowLeft')).toBe('coding-pack');
	});

	it('ignores every other key, so typing near the control cannot switch packs', () => {
		for (const key of ['Enter', 'a', 'Tab', 'Escape', ' ']) {
			expect(nextPack('coding-pack', key)).toBeNull();
		}
	});
});

describe('nextMode', () => {
	it('moves forward and back with the arrow keys a radiogroup answers to', () => {
		expect(nextMode('chat', 'ArrowRight')).toBe('cowork');
		expect(nextMode('chat', 'ArrowDown')).toBe('cowork');
		expect(nextMode('cowork', 'ArrowLeft')).toBe('chat');
		expect(nextMode('cowork', 'ArrowUp')).toBe('chat');
	});

	it('wraps at both ends, as a native radio group does', () => {
		expect(nextMode('cowork', 'ArrowRight')).toBe('chat');
		expect(nextMode('chat', 'ArrowLeft')).toBe('cowork');
	});

	it('ignores every other key, so typing near the control cannot switch modes', () => {
		for (const key of ['Enter', 'a', 'Tab', 'Escape', ' ']) {
			expect(nextMode('chat', key)).toBeNull();
		}
	});
});

describe('renderRun', () => {
	it('renders a finished run as its own summary, which is the transcript turn', () => {
		expect(renderRun({ status: 'succeeded', result_summary_ref: 'The answer is 55.' })).toBe(
			'The answer is 55.'
		);
	});

	it('never renders a settled run as an empty turn', () => {
		// An empty bubble under a question reads as a failure the transcript
		// declined to mention, which is the shape this whole surface exists to
		// stop making.
		expect(renderRun({ status: 'succeeded', result_summary_ref: '   ' }).length).toBeGreaterThan(0);
		expect(renderRun({ status: 'failed', error_message: '' }).length).toBeGreaterThan(0);
		expect(renderRun({ status: 'cancelled' }).length).toBeGreaterThan(0);
		expect(renderRun({ status: 'unknown' }).length).toBeGreaterThan(0);
	});

	it("carries the server's own sentence when a run fails, rather than replacing it", () => {
		expect(renderRun({ status: 'failed', error_message: 'sandbox quota reached' })).toContain(
			'sandbox quota reached'
		);
	});

	it('distinguishes queued from running, because waiting for a sandbox is not working', () => {
		expect(renderRun({ status: 'queued' })).not.toBe(renderRun({ status: 'running' }));
	});
});

describe('runTurnIsDone', () => {
	it('holds the turn open only while the run can still change', () => {
		expect(runTurnIsDone({ status: 'queued' })).toBe(false);
		expect(runTurnIsDone({ status: 'running' })).toBe(false);
	});

	it('closes the turn on every settled state, unknown included', () => {
		// `unknown` is deliberately NOT in agentTasks.TERMINAL_STATUSES, which
		// drives polling. It is settled here because a turn left `done: false`
		// disables the composer's send path for that conversation forever, so an
		// unreadable status must not be able to wedge a conversation.
		for (const status of ['succeeded', 'failed', 'cancelled', 'unknown']) {
			expect(runTurnIsDone({ status })).toBe(true);
		}
	});
});

describe('selectPendingCoworkTurns', () => {
	// A conversation can hold more than one run: the user can submit again in
	// Cowork mode once the first settles. A reload leaves BOTH assistant turns
	// carrying a hive_agent_task_id, and loadChat has already stamped both
	// `done: true` regardless of whether their runs actually finished, so the
	// selector must return every carrying turn rather than the oldest one.
	// Picking only the first (what `Object.values(...).find(...)` did before
	// this fix) is exactly the bug this test catches: it would return an array
	// with the older run only, silently dropping the second.
	const messages = {
		older: {
			id: 'older',
			role: 'assistant',
			done: true,
			hive_agent_task_id: 'task-1-settled'
		},
		newer: {
			id: 'newer',
			role: 'assistant',
			done: true, // stamped by loadChat; the run is still running server side
			hive_agent_task_id: 'task-2-still-running'
		},
		userTurn: {
			id: 'userTurn',
			role: 'user',
			content: 'go again'
		}
	};

	it('returns every assistant turn carrying a run id, not just the first', () => {
		const pending = selectPendingCoworkTurns(messages);
		const ids = pending.map((turn) => turn.hive_agent_task_id).sort();
		expect(ids).toEqual(['task-1-settled', 'task-2-still-running']);
	});

	it('never selects a plain user or assistant turn with no run id', () => {
		const pending = selectPendingCoworkTurns(messages);
		expect(pending.find((turn) => turn.id === 'userTurn')).toBeUndefined();
	});

	it('handles an empty or missing history without throwing', () => {
		expect(selectPendingCoworkTurns({})).toEqual([]);
		expect(selectPendingCoworkTurns(null)).toEqual([]);
		expect(selectPendingCoworkTurns(undefined)).toEqual([]);
	});
});

/*
 * Source pins. There is no component harness in this tree, so the wiring that
 * makes the mode reachable is asserted against the shipped sources, the same
 * way settings-declutter.test.ts pins its surface. These catch the failure that
 * unit tests over pure helpers cannot: a correct module nobody mounted.
 */
describe('the composer actually carries the mode', () => {
	const messageInput = readComponent('../components/chat/MessageInput.svelte');

	it('mounts the toggle immediately after the plus button, per D-045', () => {
		const plus = messageInput.indexOf('</InputMenu>');
		const toggle = messageInput.indexOf('<ComposerModeToggle');
		expect(plus).toBeGreaterThan(-1);
		expect(toggle).toBeGreaterThan(plus);
		// Nothing interactive between the two: D-045 puts the control
		// immediately right of the plus, not somewhere further along the rail.
		expect(messageInput.slice(plus, toggle)).not.toContain('<button');
	});

	it('grows the second row only in cowork mode', () => {
		expect(messageInput).toContain("{#if $composerMode === 'cowork'}");
		expect(messageInput).toContain('<ComposerCoworkRow />');
	});

	it('drops voice mode in cowork and keeps dictation in both', () => {
		expect(messageInput).toContain("{#if $composerMode !== 'cowork' && prompt === ''");
		expect(messageInput).toContain('id="voice-input-button"');
		// The microphone must not have picked up the mode condition by accident.
		const mic = messageInput.indexOf('id="voice-input-button"');
		expect(messageInput.slice(Math.max(0, mic - 1200), mic)).not.toContain('$composerMode');
	});
});

/*
 * #1500. The row used to render the pack as a `<span>`, so a Playwright query
 * for clickable elements matching "Knowledge work" returned 0 against the live
 * box and every composer submission ran as knowledge-work-pack. These pins are
 * the source-level half of that; the count-greater-than-zero half is the
 * screenshot on the pull request.
 */
describe('the cowork row offers the pack rather than stating it', () => {
	const row = readComponent('./ComposerCoworkRow.svelte');

	it('is a radiogroup with a radio per pack, the same contract the mode toggle has', () => {
		expect(row).toContain('role="radiogroup"');
		expect(row).toContain('role="radio"');
		expect(row).toContain('data-hive-pack={option.value}');
		expect(row).toContain('aria-checked={$composerPack === option.value}');
	});

	it('writes the store the submit path reads, so the choice reaches the wire', () => {
		expect(row).toContain("import { composerPack } from '$lib/stores'");
		expect(row).toContain('composerPack.set(');
	});

	it('binds the click path, not only the keyboard one', () => {
		// Review finding on #1518: asserting `composerPack.set(` alone would
		// stay green with the click handler detached, because the keyboard
		// handler calls the same function. The mouse is the primary way anyone
		// picks a pack, so its binding is pinned on its own.
		expect(row).toContain('on:click={() => select(option.value)}');
	});

	it('reuses the mode toggle segment classes rather than inventing a control idiom', () => {
		expect(row).toContain('hv-mode-segment');
	});

	it('keeps no static pack label that could disagree with the selection', () => {
		// The briefcase glyph named one pack and was wrong the moment the other
		// was chosen, and the bare `<span>` is the defect itself.
		expect(row).not.toContain('hv-cowork-scope');
	});
});

describe('sending in cowork mode starts a run instead of a completion', () => {
	const chat = readComponent('../components/chat/Chat.svelte');

	it('sends the pack the person chose, not a derived constant (#1500)', () => {
		expect(chat).toContain('createTask(localStorage.token, $composerPack, userPrompt)');
		expect(chat).not.toContain('packForMode');
	});

	it('branches on the mode inside the one submit handler both composers call', () => {
		expect(chat).toContain("if ($composerMode === 'cowork')");
		expect(chat).toContain('await submitCoworkRun(userPrompt)');
	});

	it('renders the run through the ordinary chat machinery, not a parallel one', () => {
		// A run IS a conversation (D-045): same history, same chat creation, same
		// persistence. If any of these three stopped being called the run would
		// quietly become a separate object again.
		expect(chat).toContain('initChatHandler(history)');
		expect(chat).toContain('saveChatHandler(_chatId, history)');
		expect(chat).toContain("role: 'assistant'");
	});

	it('keeps following a run that outlived the tab that started it', () => {
		expect(chat).toContain('resumeCoworkRun');
		expect(chat).toContain('hive_agent_task_id');
	});

	it('bounds the poll loop, so a wedged run cannot poll forever', () => {
		expect(chat).toContain('COWORK_FOLLOW_CEILING_MS');
	});
});

const event = (over: Partial<TaskEvent> = {}): TaskEvent => ({
	seq: 1,
	kind: 'tool_call',
	payload: {},
	created_at: '2026-08-25T10:00:00Z',
	...over
});

describe('describeEvent', () => {
	it('names the tool on a call and on its result', () => {
		expect(
			describeEvent(event({ kind: 'tool_call', payload: { tool_name: 'bash', preview: 'ls -la' } }))
		).toBe('Using bash: ls -la');
		expect(
			describeEvent(
				event({ kind: 'tool_result', payload: { tool_name: 'bash', preview: 'README.md' } })
			)
		).toBe('Used bash: README.md');
	});

	it('still says something when the tool has no name and the preview is empty', () => {
		expect(describeEvent(event({ kind: 'tool_call', payload: {} }))).toBe('Using a tool');
	});

	it('says a preview was shortened rather than showing it as complete', () => {
		// The wire cut every preview to 2000 runes and left no marker, so a
		// preview sitting exactly on the boundary is the only evidence.
		const long = 'x'.repeat(2000);
		expect(describeEvent(event({ kind: 'message', payload: { preview: long } }))).toBe(
			`(shortened) ${long}`
		);
		const short = 'x'.repeat(1999);
		expect(describeEvent(event({ kind: 'message', payload: { preview: short } }))).toBe(short);
	});

	it('puts the marker in front, where a one-line clamp cannot eat it', () => {
		// Appended, it is the first thing the clamp drops, and the line then
		// reads as a complete tool result. Seen in the first capture of this
		// surface, which is why this test exists.
		const line = describeEvent(
			event({ kind: 'tool_result', payload: { tool_name: 'execute_bash', preview: 'y'.repeat(2000) } })
		);
		expect(line?.startsWith('Used execute_bash (shortened): ')).toBe(true);
	});

	it('counts runes, not UTF-16 units, the way the backend cap does', () => {
		// 1500 astral characters is 3000 UTF-16 units and 1500 runes: under the
		// cap, so nothing was removed and nothing may claim otherwise.
		const astral = '𝄞'.repeat(1500);
		expect(describeEvent(event({ kind: 'message', payload: { preview: astral } }))).toBe(astral);
	});

	it('reports a payload the backend replaced with its truncation marker', () => {
		expect(describeEvent(event({ kind: 'tool_result', payload: { truncated: true, size: 82000 } }))).toBe(
			'An update too large to show here (82000 bytes).'
		);
	});

	it('skips a status row that only repeats the task state the turn already shows', () => {
		expect(describeEvent(event({ kind: 'status', payload: { status: 'running' } }))).toBeNull();
	});

	it('surfaces an upstream event class this build has never met', () => {
		// The syncer's fallback stores an unmapped class as `status` with its raw
		// payload precisely so it cannot vanish. It must not vanish here either.
		expect(
			describeEvent(event({ kind: 'status', payload: { sandbox_kind: 'CondenserEvent' } }))
		).toBe('Sandbox event: CondenserEvent');
	});

	it('filters an event kind this build has never heard of, rather than apologizing', () => {
		// Used to render the literal string "An update this version of Hive
		// cannot read.", which reached real users as a junk line in the step
		// list. Filtered out (null) instead, same as any other unreadable row.
		expect(describeEvent(event({ kind: 'unknown', payload: {} }))).toBeNull();
	});

	it('filters a status row that is neither a sandbox event nor a task-status echo', () => {
		expect(describeEvent(event({ kind: 'status', payload: {} }))).toBeNull();
	});

	it('names a workspace file', () => {
		expect(describeEvent(event({ kind: 'file', payload: { name: 'report.md', size: 12 } }))).toBe(
			'Workspace file: report.md'
		);
	});
});

describe('foldRunSteps', () => {
	it('closes a tool call with its own result rather than adding a second line', () => {
		const called = foldRunSteps([], [
			event({ seq: 1, kind: 'tool_call', payload: { tool_name: 'bash', tool_call_id: 'c1' } })
		]);
		expect(called).toHaveLength(1);
		expect(called[0].done).toBe(false);

		const answered = foldRunSteps(called, [
			event({
				seq: 2,
				kind: 'tool_result',
				payload: { tool_name: 'bash', tool_call_id: 'c1', preview: 'ok' }
			})
		]);
		expect(answered).toHaveLength(1);
		expect(answered[0].done).toBe(true);
		expect(answered[0].description).toBe('Used bash: ok');
		expect(answered[0].seq).toBe(2);
	});

	it('gives an orphan result its own line instead of dropping it', () => {
		// A conversation reopened mid-run has its earlier events behind the
		// cursor, so the call this result belongs to is not on the turn.
		const steps = foldRunSteps([], [
			event({ seq: 9, kind: 'tool_result', payload: { tool_name: 'bash', tool_call_id: 'gone' } })
		]);
		expect(steps).toHaveLength(1);
		expect(steps[0].done).toBe(true);
	});

	it('pairs a result with the newest matching open call, not an already-closed one', () => {
		const steps = foldRunSteps([], [
			event({ seq: 1, kind: 'tool_call', payload: { tool_name: 'bash', tool_call_id: 'c1' } }),
			event({ seq: 2, kind: 'tool_result', payload: { tool_name: 'bash', tool_call_id: 'c1' } }),
			event({ seq: 3, kind: 'tool_call', payload: { tool_name: 'bash', tool_call_id: 'c1' } }),
			event({ seq: 4, kind: 'tool_result', payload: { tool_name: 'bash', tool_call_id: 'c1' } })
		]);
		expect(steps).toHaveLength(2);
		expect(steps.every((step) => step.done)).toBe(true);
	});

	it('never mutates the lines already on the turn', () => {
		const before: RunStep[] = [
			{ action: 'hive_agent_step', description: 'Using bash', done: false, seq: 1, tool_call_id: 'c1' }
		];
		const after = foldRunSteps(before, [
			event({ seq: 2, kind: 'tool_result', payload: { tool_name: 'bash', tool_call_id: 'c1' } })
		]);
		expect(before[0].done).toBe(false);
		expect(after[0].done).toBe(true);
		expect(after).not.toBe(before);
	});

	it('renders nothing the backend did not send', () => {
		expect(foldRunSteps([], [])).toEqual([]);
		// A status row repeating the task state contributes no line, and adds no
		// placeholder in its stead.
		expect(foldRunSteps([], [event({ kind: 'status', payload: { status: 'running' } })])).toEqual(
			[]
		);
	});
});

describe('the follower cursor', () => {
	it('resumes from the highest seq the turn already carries', () => {
		const steps = foldRunSteps([], [
			event({ seq: 4, kind: 'message', payload: { preview: 'starting' } }),
			event({ seq: 11, kind: 'message', payload: { preview: 'still going' } })
		]);
		expect(latestStepSeq(steps)).toBe(11);
	});

	it('starts at zero for a turn with no lines, and for one stored before seq existed', () => {
		expect(latestStepSeq([])).toBe(0);
		expect(latestStepSeq(undefined)).toBe(0);
		expect(latestStepSeq([{ action: 'hive_agent_step', description: 'x', done: true } as RunStep])).toBe(
			0
		);
	});
});

describe('settleRunSteps', () => {
	it('stops a step shimmering under a run that has finished', () => {
		const settled = settleRunSteps([
			{ action: 'hive_agent_step', description: 'Using bash', done: false, seq: 1 }
		]);
		expect(settled[0].done).toBe(true);
		// The text is not rewritten: what is known is that it stopped, not that
		// it succeeded.
		expect(settled[0].description).toBe('Using bash');
	});
});

describe('dropSummaryEcho', () => {
	const step = (description: string, seq: number): RunStep => ({
		action: 'hive_agent_step',
		description,
		done: true,
		seq
	});

	// The sentence issue #1509 saw twice. The event feed's copy has the message's
	// content blocks joined with a single space (controlclient/events.go), the
	// turn's copy keeps the message's own line breaks, and they are the same
	// words, which is the whole defect.
	const summary = 'Created `sixcap.txt` with the text `HIVE-COWORK-OK` and displayed its contents:\n\n```\nHIVE-COWORK-OK\n```';
	const echo = summary.split(/\s+/).join(' ');

	it('drops the closing step that only repeats the turn content (#1509)', () => {
		const kept = dropSummaryEcho([step('Using bash', 1), step(echo, 2)], summary);
		expect(kept.map((s) => s.description)).toEqual(['Using bash']);
	});

	it('matches across the line breaks the two routes disagree about', () => {
		// Exact string equality would fail here and the duplicate would survive:
		// one route joined the content blocks with a space, the other did not.
		expect(echo).not.toBe(summary);
		expect(dropSummaryEcho([step(echo, 1)], summary)).toEqual([]);
	});

	it('drops a shortened preview that is a marked prefix of the summary', () => {
		const long = `${'x'.repeat(40)} tail that the wire cut off`;
		const kept = dropSummaryEcho([step(`(shortened) ${'x'.repeat(40)}`, 1)], long);
		expect(kept).toEqual([]);
	});

	it('keeps a short step that merely begins the summary but is not marked', () => {
		// Without the marker there is no evidence the wire cut anything, so a
		// prefix is just a different, shorter thing the agent said.
		const kept = dropSummaryEcho([step('Created', 1)], 'Created the file and read it back');
		expect(kept.map((s) => s.description)).toEqual(['Created']);
	});

	it('keeps every step when the summary is an artifact URL rather than prose', () => {
		// publishDeckArtifact replaces the agent's text summary with the deck's
		// URL, so the closing message is NOT the turn content and is the only
		// place that text appears. Dropping it there would delete real content.
		const kept = dropSummaryEcho(
			[step('Using bash', 1), step(echo, 2)],
			'https://artifacts.example.invalid/d/abc123'
		);
		expect(kept.map((s) => s.description)).toEqual(['Using bash', echo]);
	});

	it('drops only the last echo, so a sentence the agent really did repeat stays visible', () => {
		const kept = dropSummaryEcho([step(echo, 1), step('Using bash', 2), step(echo, 3)], summary);
		expect(kept.map((s) => s.description)).toEqual([echo, 'Using bash']);
	});

	it('drops the echo even when a file step lands after it (#1509 review)', () => {
		// "The echo is the last step" is not a construction guarantee: the event
		// sync appends workspace file events after the mapped sandbox events in
		// the same batch (eventsync.go), so a file first seen on the final pass
		// carries a higher seq than the closing message. The backwards scan is
		// what makes that safe, and this is the ordering that catches a rewrite
		// into a cheaper last-element check.
		const kept = dropSummaryEcho(
			[step('Using bash', 1), step(echo, 2), step('Workspace file: sixcap.txt', 3)],
			summary
		);
		expect(kept.map((s) => s.description)).toEqual([
			'Using bash',
			'Workspace file: sixcap.txt'
		]);
	});

	it('drops a shortened line describeEvent itself produced, not a hand-built one', () => {
		// The prefix branch parses '(shortened) ' at position 0, which only
		// describeEvent's message arm produces; every other arm goes through
		// withPreview and puts the marker after the label. Building the marked
		// string by hand in a test leaves that contract uncrossed, so this case
		// runs the real producer into the real consumer: if the message arm ever
		// adopts the withPreview shape, this fails instead of quietly passing.
		const full = `${'sixcap '.repeat(400)}tail the wire cut off`;
		const cut = Array.from(full).slice(0, 2000).join('');
		const description = describeEvent({
			seq: 9,
			kind: 'message',
			payload: { role: 'assistant', preview: cut },
			created_at: ''
		} as TaskEvent);
		expect(description).not.toBeNull();
		expect(description as string).toContain('(shortened)');

		const kept = dropSummaryEcho(
			[step('Using bash', 1), step(description as string, 2)],
			full
		);
		expect(kept.map((s) => s.description)).toEqual(['Using bash']);
	});

	it('drops nothing when the turn has no content to echo', () => {
		const steps = [step('Using bash', 1)];
		expect(dropSummaryEcho(steps, '')).toEqual(steps);
		expect(dropSummaryEcho(steps, '   ')).toEqual(steps);
	});

	it('returns a new array rather than editing the one the turn already holds', () => {
		const steps = [step('Using bash', 1), step(echo, 2)];
		const kept = dropSummaryEcho(steps, summary);
		expect(kept).not.toBe(steps);
		expect(steps).toHaveLength(2);
	});
});

describe('the run turn follows the detail endpoint, not the task list', () => {
	const chat = readComponent('../components/chat/Chat.svelte');

	it('reads one task by id rather than filtering every task the user owns', () => {
		expect(chat).toContain('getTask(localStorage.token, taskId)');
		expect(chat).not.toContain('listTasks');
	});

	it('drops the summary echo only once the run has settled (#1509)', () => {
		// The duplicate exists only after the turn's content becomes the run's
		// summary; while it is still going the content is a status line and
		// there is nothing to match, so the drop rides the settled branch.
		expect(chat).toContain('settleRunSteps(dropSummaryEcho(steps, turn.content))');
		expect(chat).toContain('dropSummaryEcho(turn.statusHistory as RunStep[], turn.content)');
	});

	it('uses the events cursor, so a poll asks only for what it has not seen', () => {
		expect(chat).toContain('getTaskEvents(');
		expect(chat).toContain('latestStepSeq(steps)');
	});

	it('renders progress from the events themselves', () => {
		expect(chat).toContain('foldRunSteps(steps, events)');
		expect(chat).toContain('turn.statusHistory');
	});

	it('keeps the three behaviours #1193 review fixed', () => {
		// The navigation guard, resuming every pending run rather than the first,
		// and the clean refusal when files are attached in Cowork mode.
		expect(chat).toContain('if ($chatId !== _chatId) {');
		expect(chat).toContain('for (const pending of pendingTurns)');
		expect(chat).toContain('Attachments are not supported in Work mode yet');
	});
});
