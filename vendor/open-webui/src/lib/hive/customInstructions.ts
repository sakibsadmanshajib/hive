/*
 * The signed-in person's standing custom instructions (issue #1363).
 *
 * Hive authored. Everything under src/lib/hive/ is ours, so a rebase against a
 * future upstream tag reads as a file list rather than an archaeology exercise.
 *
 * These two calls go to Open WebUI's own backend, not to edge-api, for the
 * reason agentTasks.ts documents at length: the browser has no credential
 * edge-api accepts. The server-side shim
 * (deploy/docker/owui-patches/hive_instructions.py) resolves the signed-in
 * user's token per request and forwards to /v1/user/instructions on the same
 * mechanism this deployment already runs for chat completions.
 *
 * This deliberately REPLACES Open WebUI's own Settings > General "System
 * Prompt" field, which wrote `$settings.system` into Open WebUI's database and
 * was delivered by the browser attaching a system message to each request.
 * Two stores for one concept is the defect shape this repository keeps
 * relearning, and the Open WebUI half was the wrong one to keep: it is invisible
 * to every client that is not this browser, it dies with a volume reset, and
 * `.wolf/decisions.md` D-044 puts the source of truth in Hive's own backend
 * rather than in Open WebUI's database.
 */

// A parameter with a production default rather than an import of
// `$lib/constants`, and that is load bearing rather than stylistic: that module
// reaches `$app/environment`, which only SvelteKit's resolver can satisfy, and
// scripts/test-owui-hive-frontend.sh runs these files under plain vitest with no
// alias resolution. A module that imported it would take its own test file down
// with it, silently, by never being loadable at all. Same reasoning, same
// default shape as agentTasks.ts.
export const DEFAULT_INSTRUCTIONS_API_BASE_URL = '/api/v1/hive/instructions';

/**
 * The cap edge-api enforces, in characters, mirrored here so the textarea can
 * show a counter and refuse locally instead of round-tripping to be told no.
 * The server is still the authority: `MaxContentLen` in
 * apps/edge-api/internal/userinstructions/store.go and the CHECK on
 * public.user_instructions both hold the same number, and this copy exists for
 * the message, not for the rule.
 */
export const MAX_INSTRUCTIONS_LENGTH = 4000;

/**
 * Read the signed-in person's instructions.
 *
 * `null` means UNREADABLE. A string, including the empty one, means read
 * successfully, and empty means they have written none.
 *
 * These two must not collapse, and an earlier revision of this file collapsed
 * them. Every failure returned `''`, the pane marked itself loaded, and the
 * person's next Save posted that empty string over text they still had. Empty
 * content deletes the row, so one 502 on load erased somebody's instructions
 * the next time they opened Settings. The caller now refuses to save what it
 * could not read.
 *
 * Same distinction `tolerateBoxed` draws in apps/web-console/lib/console/data.ts,
 * with no box: that helper wraps because its own value is nullable and needs a
 * third state. Here absence IS the empty string, so `string | null` already
 * carries both meanings and a wrapper would add a layer over nothing.
 */
export async function getCustomInstructions(
	baseUrl: string = DEFAULT_INSTRUCTIONS_API_BASE_URL,
	fetchImpl: typeof fetch = fetch
): Promise<string | null> {
	let response: Response;
	try {
		response = await fetchImpl(baseUrl, {
			method: 'GET',
			headers: { Accept: 'application/json' },
			credentials: 'include'
		});
	} catch {
		return null;
	}
	if (!response.ok) return null;

	try {
		const body = await response.json();
		const content = (body ?? {}).content;
		// A 200 whose body is not the shape this endpoint documents is not a
		// read either. Answering '' here would be the same deletion by another
		// route: a proxy returning an HTML error page with a 200 is exactly
		// how that happens in practice.
		return typeof content === 'string' ? content : null;
	} catch {
		return null;
	}
}

/**
 * Replace the signed-in person's instructions, returning what was actually
 * stored.
 *
 * The stored text comes back rather than the submitted text, so a character
 * the server strips is visible in the textarea immediately rather than on the
 * next page load. An empty string clears them, which is a legal request, not
 * an error: it is what an emptied textarea means.
 *
 * Throws on failure. The caller must surface it: silently discarding somebody's
 * instructions while the pane closes as though it saved is the worst available
 * outcome here.
 */
export async function saveCustomInstructions(
	content: string,
	baseUrl: string = DEFAULT_INSTRUCTIONS_API_BASE_URL,
	fetchImpl: typeof fetch = fetch
): Promise<string> {
	if (content.length > MAX_INSTRUCTIONS_LENGTH) {
		throw new Error(`Custom instructions are limited to ${MAX_INSTRUCTIONS_LENGTH} characters.`);
	}

	const response = await fetchImpl(baseUrl, {
		method: 'PUT',
		headers: { 'Content-Type': 'application/json', Accept: 'application/json' },
		credentials: 'include',
		body: JSON.stringify({ content })
	});

	if (!response.ok) {
		throw new Error(await readErrorMessage(response));
	}

	const body = await response.json();
	const stored = (body ?? {}).content;
	return typeof stored === 'string' ? stored : '';
}

/**
 * The sentence to show a person when a save fails.
 *
 * edge-api's error envelope and the shim's own FastAPI refusals have different
 * shapes, and both reach this client: the shim returns edge-api's JSON verbatim
 * when it got that far, and its own `{detail: "..."}` when it did not. Read
 * both, fall back to a plain sentence, and never render a raw status code at
 * somebody who is trying to save a paragraph of prose.
 */
async function readErrorMessage(response: Response): Promise<string> {
	try {
		const body = await response.json();
		const message = (body ?? {})?.error?.message ?? (body ?? {})?.detail;
		if (typeof message === 'string' && message.trim() !== '') return message;
	} catch {
		// Falls through to the default sentence below.
	}
	return 'Your custom instructions could not be saved. Try again.';
}
