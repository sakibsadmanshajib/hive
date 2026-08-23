import { describe, expect, it } from 'vitest';

import { SCHEDULES_BRIDGE_READY, cadenceLabel, validateScheduleInput } from './agentSchedules';

describe('agentSchedules', () => {
	it('exposes the honest bridge state for this slice', () => {
		expect(SCHEDULES_BRIDGE_READY).toBe(false);
	});

	it('labels cadences readably', () => {
		expect(cadenceLabel('daily')).toBe('Daily');
		expect(cadenceLabel('weekly')).toBe('Weekly');
		expect(cadenceLabel('interval:6')).toBe('Every 6 hours');
		expect(cadenceLabel('interval:1')).toBe('Every 1 hour');
	});

	it('validates input at the boundary', () => {
		expect(validateScheduleInput('', 'x', 'daily')).not.toBeNull();
		expect(validateScheduleInput('n', '', 'daily')).not.toBeNull();
		expect(validateScheduleInput('n', 'i', '* * * * *')).not.toBeNull();
		expect(validateScheduleInput('n', 'i', 'interval:99999')).not.toBeNull();
		expect(validateScheduleInput('n', 'i', 'daily')).toBeNull();
	});
});
