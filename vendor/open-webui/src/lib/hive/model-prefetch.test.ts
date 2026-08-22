import { afterEach, describe, expect, it, vi } from 'vitest';

import {
	consumeModelPrefetch,
	isAuthenticatedConfig,
	prefetchModels,
	resetModelPrefetch
} from './model-prefetch';

// The fetcher is injected, so there is no module to mock and no SvelteKit
// alias to resolve. That is the whole reason this file can run at all: see the
// note at the top of model-prefetch.ts.
const getModels = vi.fn();
const fetchModels = () => getModels();

afterEach(() => {
	resetModelPrefetch();
	getModels.mockReset();
});

describe('model prefetch', () => {
	it('issues one request that the consumer collects', async () => {
		getModels.mockResolvedValue([{ id: 'hive-auto' }]);

		prefetchModels('token', fetchModels);
		prefetchModels('token', fetchModels);

		expect(getModels).toHaveBeenCalledTimes(1);
		await expect(consumeModelPrefetch('token')).resolves.toEqual([{ id: 'hive-auto' }]);
	});

	it('issues nothing without a token, so a signed-out load stays silent', () => {
		prefetchModels('', fetchModels);
		prefetchModels(null, fetchModels);
		prefetchModels(undefined, fetchModels);

		expect(getModels).not.toHaveBeenCalled();
		expect(consumeModelPrefetch('token')).toBeNull();
	});

	it('hands the request over exactly once', async () => {
		getModels.mockResolvedValue([]);

		prefetchModels('token', fetchModels);

		expect(consumeModelPrefetch('token')).not.toBeNull();
		// A second consumer has to fetch for itself rather than receive a
		// promise that has already been read, which is what keeps a later
		// refresh going to the network.
		expect(consumeModelPrefetch('token')).toBeNull();
	});

	it('refuses to hand a list started under one token to another', async () => {
		getModels.mockResolvedValue([{ id: 'tenant-a-model' }]);

		prefetchModels('token-a', fetchModels);

		// The sign-in page navigates client side, so a tab can start this
		// request under one session and mount the application under another.
		// The second principal has to fetch its own list.
		expect(consumeModelPrefetch('token-b')).toBeNull();
		expect(consumeModelPrefetch('token-a')).toBeNull();
	});

	it('drops a failed request rather than handing the failure on', async () => {
		getModels.mockRejectedValue(new Error('gateway down'));

		prefetchModels('token', fetchModels);
		await new Promise((resolve) => setTimeout(resolve, 0));

		// Nothing to collect, so the caller fetches for itself: the same thing
		// it would have done had no prefetch been attempted. Handing over the
		// dead promise instead would turn one failed request into an empty
		// model list with no retry.
		expect(consumeModelPrefetch('token')).toBeNull();
	});

	it('lets a later call retry after a failure', async () => {
		getModels.mockRejectedValueOnce(new Error('gateway down'));

		prefetchModels('token', fetchModels);
		await new Promise((resolve) => setTimeout(resolve, 0));

		getModels.mockResolvedValue([{ id: 'hive-auto' }]);
		prefetchModels('token', fetchModels);

		expect(getModels).toHaveBeenCalledTimes(2);
		await expect(consumeModelPrefetch('token')).resolves.toEqual([{ id: 'hive-auto' }]);
	});

	it('leaves an uncollected failure handled', async () => {
		const unhandled = vi.fn();
		process.on('unhandledRejection', unhandled);
		getModels.mockRejectedValue(new Error('gateway down'));

		prefetchModels('token', fetchModels);
		await new Promise((resolve) => setTimeout(resolve, 10));
		process.off('unhandledRejection', unhandled);

		expect(unhandled).not.toHaveBeenCalled();
	});
});

describe('isAuthenticatedConfig', () => {
	// The two real shapes GET /api/config emits. Upstream adds the whole
	// authenticated block or none of it, keyed on whether it resolved a user
	// from the Authorization header or the `token` cookie.
	const anonymous = {
		features: { auth: true, enable_login_form: false, enable_ldap: false }
	};
	const authenticated = {
		features: { auth: true, enable_login_form: false, enable_api_keys: true }
	};

	it('separates the anonymous reply from the authenticated one', () => {
		expect(isAuthenticatedConfig(authenticated)).toBe(true);
		expect(isAuthenticatedConfig(anonymous)).toBe(false);
	});

	it('reads a present-but-false flag as authenticated', () => {
		// The value is a setting, not a signal. An administrator turning API
		// keys off must not be mistaken for a signed-out reply, which would
		// cost every such deployment an extra request on every page load.
		expect(isAuthenticatedConfig({ features: { enable_api_keys: false } })).toBe(true);
	});

	it('treats a missing or malformed reply as not authenticated', () => {
		// getBackendConfig returns null on failure, and the caller then falls
		// back to fetching again, which is the pre-change behaviour.
		expect(isAuthenticatedConfig(null)).toBe(false);
		expect(isAuthenticatedConfig(undefined)).toBe(false);
		expect(isAuthenticatedConfig({})).toBe(false);
		expect(isAuthenticatedConfig({ features: null })).toBe(false);
	});
});
