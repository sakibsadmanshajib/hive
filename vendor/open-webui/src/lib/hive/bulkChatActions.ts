/*
 * Archive All and Delete All in Settings > Data Controls (#866).
 *
 * Hive authored. Everything under src/lib/hive/ is ours, so a rebase against a
 * future upstream tag reads as a file list rather than an archaeology exercise.
 *
 * WHY A MODULE AND NOT FOUR LINES IN THE COMPONENT
 * -----------------------------------------------
 * DataControls.svelte imports $lib stores, $app/navigation and the chat API
 * client, so it cannot be rendered by the one runner that covers this front end
 * (scripts/test-owui-hive-frontend.sh copies lib/hive into a scratch tree with
 * vitest and nothing else). Logic left inside the component is logic no test
 * executes. The decision lives here; the component keeps the wiring.
 *
 * WHAT THESE TWO WRITES ACTUALLY ANSWER
 * -------------------------------------
 * `POST /api/v1/chats/archive/all` and `DELETE /api/v1/chats/` are both
 * declared `response_model=bool`, and both model functions behind them
 * (`archive_all_chats_by_user_id` and `delete_chats_by_user_id` in
 * backend/open_webui/models/chats.py) wrap their writes in `try/except` and
 * return False on any exception. A bulk write that touched nothing therefore
 * arrives as HTTP 200 with `false` in the body, not as an error the fetch
 * wrapper in $lib/apis/chats would throw. Both handlers used to discard that
 * body and refetch the list, so a refused archive and a refused delete were
 * indistinguishable from a completed one: no message either way, and a sidebar
 * that looked the same because nothing had changed. That is this repository's
 * dominant defect shape, a surface reporting a state the system does not have,
 * and it is why the boolean is checked here rather than assumed.
 */

export type BulkChatAction = {
	/** The API call itself. Returns the server's parsed body, or throws. */
	write: () => Promise<unknown>;
	/** Leave whatever chat is open; its row may no longer exist. */
	navigateHome: () => Promise<void>;
	/** Refetch the sidebar lists, pinned section included. */
	refresh: () => Promise<void>;
	notifyError: (message: string) => void;
	notifySuccess: (message: string) => void;
	successMessage: string;
	failureMessage: string;
};

/*
 * The fetch wrapper in $lib/apis/chats throws `err.detail` rather than an
 * Error, and `detail` is a plain string on every refusal this backend raises
 * (a caller without the `chat.delete` permission gets 401 "Access prohibited").
 * A transport failure throws a TypeError instead, and a body with no `detail`
 * throws undefined, so all three shapes are unwrapped and anything left over
 * falls back to the caller's own wording rather than rendering as "[object
 * Object]" or an empty toast.
 */
const errorText = (error: unknown, fallback: string): string => {
	if (typeof error === 'string' && error.trim() !== '') return error;
	if (error instanceof Error && error.message.trim() !== '') return error.message;
	const detail = (error as { detail?: unknown } | null | undefined)?.detail;
	if (typeof detail === 'string' && detail.trim() !== '') return detail;
	return fallback;
};

/*
 * Anything but a refusal counts as done. Written as a denylist on purpose: the
 * refusal signal is `false`, and an empty body is the fetch wrapper's own way
 * of saying it never got one. Requiring `=== true` instead would turn a
 * response_model change upstream into a permanent "failed" toast over writes
 * that had in fact landed, which is the same lie in the other direction.
 */
const accepted = (result: unknown): boolean =>
	!(result === false || result === null || result === undefined);

export const runBulkChatAction = async (action: BulkChatAction): Promise<boolean> => {
	let result: unknown;
	try {
		result = await action.write();
	} catch (error) {
		action.notifyError(errorText(error, action.failureMessage));
		return false;
	}

	if (!accepted(result)) {
		action.notifyError(action.failureMessage);
		return false;
	}

	await action.navigateHome();

	// The refetch can fail on its own, after a write that already landed.
	// Reporting that as a failed delete would be false, and swallowing it would
	// leave a stale sidebar with nothing said, so both facts are told.
	try {
		await action.refresh();
	} catch (error) {
		action.notifyError(errorText(error, action.failureMessage));
	}

	action.notifySuccess(action.successMessage);
	return true;
};
