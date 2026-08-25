import { readFileSync } from 'node:fs';
import { dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

import { describe, expect, it } from 'vitest';

import {
	COMPOSER_MODES,
	isComposerMode,
	nextMode,
	packForMode,
	renderRun,
	runTurnIsDone
} from './coworkMode';

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

	it('derives the pack instead of asking, so no control can pick the wrong one', () => {
		expect(packForMode('cowork')).toBe('knowledge-work-pack');
		expect(packForMode('chat')).toBe('knowledge-work-pack');
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

describe('sending in cowork mode starts a run instead of a completion', () => {
	const chat = readComponent('../components/chat/Chat.svelte');

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
