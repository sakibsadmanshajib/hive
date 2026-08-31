import { describe, expect, it } from 'vitest';

import { sortModelItems } from './model-sort';

const item = (value: string, label: string) => ({ value, label });

describe('sortModelItems', () => {
	it('orders alphabetically by label, not by wire position', () => {
		const items = [
			item('hive-medium', 'Hive Medium'),
			item('hive-auto', 'Hive Auto'),
			item('hive-fast', 'Hive Fast')
		];

		expect(sortModelItems(items).map((i) => i.value)).toEqual([
			'hive-auto',
			'hive-fast',
			'hive-medium'
		]);
	});

	it('puts pinned models first, alphabetical within each group', () => {
		const items = [
			item('hive-medium', 'Hive Medium'),
			item('hive-auto', 'Hive Auto'),
			item('hive-fast', 'Hive Fast'),
			item('hive-small', 'Hive Small')
		];

		expect(sortModelItems(items, ['hive-fast', 'hive-auto']).map((i) => i.value)).toEqual([
			'hive-auto',
			'hive-fast',
			'hive-medium',
			'hive-small'
		]);
	});

	it('does not mutate the input array', () => {
		const items = [item('b', 'B'), item('a', 'A')];
		const original = [...items];

		sortModelItems(items);

		expect(items).toEqual(original);
	});

	it('defaults to no pins and treats an empty list as a no-op', () => {
		expect(sortModelItems([])).toEqual([]);
	});
});
