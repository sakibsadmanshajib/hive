import { getModels } from '$lib/apis';

// GET /api/models is the single slowest call in startup and it gates first
// render: the app layout does not reveal the composer until the model list
// resolves. It is slow because it fans out past this backend to the gateway,
// the control plane and the database, and it used to be issued last, after
// the config fetch, the session fetch and the user settings fetch had each
// completed in turn.
//
// Nothing in the request depends on any of those. It needs the session token,
// which is already in localStorage when the root layout mounts, so the root
// layout starts it there and the app layout collects the result. The list
// arrives while the rest of startup is still talking to the server instead of
// after it has finished.
//
// ponytail: one in-flight promise handed over once, not a cache. Nothing here
// stores a model list or decides when it is stale; a later refresh calls
// getModels directly and goes to the network, exactly as before.

type ModelsRequest = ReturnType<typeof getModels>;

let inFlight: ModelsRequest | null = null;
let inFlightToken: string | null = null;

/**
 * Starts the model list request early. Safe to call more than once: only the
 * first call for a given page load issues a request.
 */
export const prefetchModels = (token: string | null | undefined): void => {
	if (!token || inFlight) {
		return;
	}

	const request = getModels(token, null);
	inFlightToken = token;
	inFlight = request;

	request.catch(() => {
		// Two jobs here. First, a prefetch that nobody collects (the user is
		// signed out mid-startup, or the app layout never mounts) must not
		// surface as an unhandled rejection. Second, a failure must not stick:
		// leaving a rejected promise in the slot would make every later caller
		// inherit one dead request, both by short-circuiting prefetchModels and
		// by handing the same rejection to whoever collects it. Dropping it
		// means the next caller fetches for itself, which is the same thing it
		// would have done had no prefetch been attempted.
		//
		// Guarded on identity so a slow failure cannot clear a newer request
		// that has already replaced it.
		if (inFlight === request) {
			inFlight = null;
			inFlightToken = null;
		}
	});
};

/**
 * Hands over the in-flight request, if there is one started under this same
 * token, and forgets it. Returns null when there is nothing to hand over,
 * which tells the caller to fetch normally.
 */
export const consumeModelPrefetch = (token: string | null | undefined): ModelsRequest | null => {
	const pending = inFlight;
	const pendingToken = inFlightToken;
	inFlight = null;
	inFlightToken = null;

	// A prefetch belongs to the session that started it. The sign-in page
	// navigates client side rather than reloading, so one tab can start this
	// request under one token and then mount the application under another:
	// a model list is a tenant's entitlement, and handing one principal's list
	// to a different principal is not a thing to leave to chance for the sake
	// of a few hundred milliseconds. Mismatch means fetch normally.
	if (!pending || !token || pendingToken !== token) {
		return null;
	}

	return pending;
};

/** Test seam: drops any pending request without consuming it. */
export const resetModelPrefetch = (): void => {
	inFlight = null;
	inFlightToken = null;
};
