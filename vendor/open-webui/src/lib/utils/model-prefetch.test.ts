import { afterEach, describe, expect, it, vi } from 'vitest';

const getModels = vi.fn();

vi.mock('$lib/apis', () => ({
	getModels: (...args: unknown[]) => getModels(...args)
}));

const { prefetchModels, consumeModelPrefetch, resetModelPrefetch } = await import('./model-prefetch');

afterEach(() => {
	resetModelPrefetch();
	getModels.mockReset();
});

describe('model prefetch', () => {
	it('issues one request that the consumer collects', async () => {
		getModels.mockResolvedValue([{ id: 'hive-auto' }]);

		prefetchModels('token');
		prefetchModels('token');

		expect(getModels).toHaveBeenCalledTimes(1);
		await expect(consumeModelPrefetch('token')).resolves.toEqual([{ id: 'hive-auto' }]);
	});

	it('issues nothing without a token, so a signed-out load stays silent', () => {
		prefetchModels('');
		prefetchModels(null);
		prefetchModels(undefined);

		expect(getModels).not.toHaveBeenCalled();
		expect(consumeModelPrefetch('token')).toBeNull();
	});

	it('hands the request over exactly once', async () => {
		getModels.mockResolvedValue([]);

		prefetchModels('token');

		expect(consumeModelPrefetch('token')).not.toBeNull();
		// A second consumer has to fetch for itself rather than receive a
		// promise that has already been read, which is what keeps a later
		// refresh going to the network.
		expect(consumeModelPrefetch('token')).toBeNull();
	});

	it('refuses to hand a list started under one token to another', async () => {
		getModels.mockResolvedValue([{ id: 'tenant-a-model' }]);

		prefetchModels('token-a');

		// The sign-in page navigates client side, so a tab can start this
		// request under one session and mount the application under another.
		// The second principal has to fetch its own list.
		expect(consumeModelPrefetch('token-b')).toBeNull();
		expect(consumeModelPrefetch('token-a')).toBeNull();
	});

	it('drops a failed request rather than handing the failure on', async () => {
		getModels.mockRejectedValue(new Error('gateway down'));

		prefetchModels('token');
		await new Promise((resolve) => setTimeout(resolve, 0));

		// Nothing to collect, so the caller fetches for itself: the same thing
		// it would have done had no prefetch been attempted. Handing over the
		// dead promise instead would turn one failed request into an empty
		// model list with no retry.
		expect(consumeModelPrefetch('token')).toBeNull();
	});

	it('lets a later call retry after a failure', async () => {
		getModels.mockRejectedValueOnce(new Error('gateway down'));

		prefetchModels('token');
		await new Promise((resolve) => setTimeout(resolve, 0));

		getModels.mockResolvedValue([{ id: 'hive-auto' }]);
		prefetchModels('token');

		expect(getModels).toHaveBeenCalledTimes(2);
		await expect(consumeModelPrefetch('token')).resolves.toEqual([{ id: 'hive-auto' }]);
	});

	it('leaves an uncollected failure handled', async () => {
		const unhandled = vi.fn();
		process.on('unhandledRejection', unhandled);
		getModels.mockRejectedValue(new Error('gateway down'));

		prefetchModels('token');
		await new Promise((resolve) => setTimeout(resolve, 10));
		process.off('unhandledRejection', unhandled);

		expect(unhandled).not.toHaveBeenCalled();
	});
});
