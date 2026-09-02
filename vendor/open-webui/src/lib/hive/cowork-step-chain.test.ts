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
