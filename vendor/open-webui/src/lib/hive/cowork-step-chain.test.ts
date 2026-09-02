import { describe, expect, it } from 'vitest';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';

/*
 * The run's step chain has to be on screen, not one click away (issues #1622,
 * #1504).
 *
 * A Cowork run's steps ride on the same `statusHistory` field an ordinary chat
 * turn uses for "Searching the web" (foldRunSteps in coworkMode.ts), and
 * StatusHistory.svelte renders that list in two halves: a collapsed head
 * showing only the newest entry, and the chain of every entry behind an
 * `expand` prop that defaults to false. For a chat turn that is the right
 * default, because those runs produce one or two statuses and the answer is
 * what the reader came for. For a run whose entire visible output while it
 * works IS the chain, it means a multi step task renders as a single line that
 * replaces itself, with the steps already taken reachable only by discovering
 * that the line is a button. D-045 asks for a step chain; the collapsed head
 * is not one.
 *
 * Source level guards, like chat-noise-guards.test.ts and
 * settings-declutter.test.ts: these are upstream components whose imports
 * cannot be resolved in this scratch tree, so there is no way to render them
 * here and the shipped source is what gets pinned.
 */

const read = (rel: string): string =>
	readFileSync(fileURLToPath(new URL(rel, import.meta.url)), 'utf8');

const chat = () => read('../components/chat/Chat.svelte');

const responseMessage = () => read('../components/chat/Messages/ResponseMessage.svelte');
const statusHistory = () =>
	read('../components/chat/Messages/ResponseMessage/StatusHistory.svelte');

describe('a run turn opens its step chain', () => {
	it('asks StatusHistory to expand for a turn that carries a run', () => {
		// The marker is the run id the composer stamps on the assistant turn
		// (submitCoworkRun in Chat.svelte). Keying on it rather than on the
		// model or on a new flag is what keeps an ordinary chat turn collapsed:
		// only a turn that IS a run gets the chain opened.
		expect(responseMessage()).toMatch(
			/<StatusHistory[^/>]*expand=\{[^}]*hive_agent_task_id[^}]*\}/
		);
	});

	it('types the run marker on the message rather than reaching for it untyped', () => {
		expect(responseMessage()).toMatch(/hive_agent_task_id\?: string;/);
	});
});

describe('StatusHistory keeps the mechanism the run turn depends on', () => {
	it('opens the chain when asked to expand', () => {
		const source = statusHistory();
		expect(source).toContain('export let expand = false;');
		expect(source).toMatch(/\$:\s*if \(expand\) \{\s*showHistory = true;/);
	});

	it('renders the steps already taken, not only the newest', () => {
		// The chain is what makes a multi step run legible: the head alone
		// shows one line and the reader cannot tell a run that took four
		// actions from one that took one.
		expect(statusHistory()).toContain('{#each history.slice(0, -1) as status}');
	});

	it('does not render the newest step twice', () => {
		// Upstream put the newest entry in the toggle button AND at the end of
		// the chain, so expanding showed it twice and showed it above the older
		// steps it came after. Harmless on a chat turn nobody expands; the
		// ordinary render for a run turn, which opens expanded.
		expect(statusHistory()).not.toContain('{#each history as status, idx}');
	});

	it('does not hide the chain once the turn stops generating', () => {
		// A settled run's steps are the record of what it did. Gating them on
		// `done` would delete that record at the exact moment the reader wants
		// to check it, and would make every capture of a finished run show a
		// bare summary, which is what issue #1504 observed.
		expect(statusHistory()).not.toMatch(/message\??\.done/);
	});
});

/*
 * The other half of the #1504 fix lives on the server: a run's remaining steps
 * are flushed immediately before its terminal status is written. That is
 * necessary and not sufficient. What closes the race is this reader asking for
 * the status BEFORE the events, so a reading that observes a terminal status
 * always issues its events request afterwards, and therefore after the flush
 * that preceded that status.
 *
 * Pinned here because reversing those two awaits, or collapsing them into a
 * Promise.all, reopens the bug while every Go test on the other side still
 * passes. An invariant that exists only in the order of two lines needs
 * something that fails when the order changes.
 */
describe('the run reader asks for the status before the events (issue #1504)', () => {
	const readCoworkRun = (): string => {
		const source = chat();
		const start = source.indexOf('const readCoworkRun =');
		expect(start).toBeGreaterThan(-1);
		const end = source.indexOf('const followCoworkRun =', start);
		expect(end).toBeGreaterThan(start);
		return source.slice(start, end);
	};

	it('awaits getTask before it reaches getTaskEvents', () => {
		const body = readCoworkRun();
		const status = body.indexOf('await getTask(');
		const events = body.indexOf('await getTaskEvents(');
		expect(status).toBeGreaterThan(-1);
		expect(events).toBeGreaterThan(-1);
		expect(status).toBeLessThan(events);
	});

	it('does not race the two reads against each other', () => {
		// The call form, not the bare name: the comment inside that function
		// names Promise.all in order to warn the next reader off it, and a
		// guard that fires on its own warning is a guard nobody keeps.
		const body = readCoworkRun();
		expect(body).not.toContain('Promise.all(');
		expect(body).not.toContain('Promise.allSettled(');
	});
});
