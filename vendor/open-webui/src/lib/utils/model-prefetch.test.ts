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
		await expect(consumeModelPrefetch()).resolves.toEqual([{ id: 'hive-auto' }]);
	});

	it('issues nothing without a token, so a signed-out load stays silent', () => {
		prefetchModels('');
		prefetchModels(null);
		prefetchModels(undefined);

		expect(getModels).not.toHaveBeenCalled();
		expect(consumeModelPrefetch()).toBeNull();
	});

	it('hands the request over exactly once', async () => {
		getModels.mockResolvedValue([]);

		prefetchModels('token');

		expect(consumeModelPrefetch()).not.toBeNull();
		// A second consumer has to fetch for itself rather than receive a
		// promise that has already been read, which is what keeps a later
		// refresh going to the network.
		expect(consumeModelPrefetch()).toBeNull();
	});

	it('still rejects for its consumer, so a failed load is not silently empty', async () => {
		getModels.mockRejectedValue(new Error('gateway down'));

		prefetchModels('token');

		await expect(consumeModelPrefetch()).rejects.toThrow('gateway down');
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
