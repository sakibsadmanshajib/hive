/*
 * Scheduled agent tasks ("routines"), from the chat frontend.
 *
 * Hive authored. Everything under src/lib/hive/ is ours, so a rebase against
 * a future upstream tag reads as a file list rather than an archaeology
 * exercise.
 *
 * Bridge status, first slice: NOT bridged. The server-side proxy in the chat
 * container (deploy/docker/owui-patches/hive_agent_proxy.py) forwards a FIXED
 * set of four task operations to /v1/agent/* on edge-api and has no schedule
 * routes yet. The browser holds Open WebUI's session token, which edge-api
 * does not accept, so until the proxy gains schedule routes this page cannot
 * reach the backend at all from here. The page therefore renders behind
 * SCHEDULES_BRIDGE_READY=false with an explicit notice instead of shipping a
 * form whose every call 404s. Next wave: add schedule routes to the proxy,
 * then flip this one boolean.
 */

// Same shape rationale as agentTasks.ts's DEFAULT_AGENT_API_BASE_URL comment:
// the default must survive the scratch-dir vitest runner without $lib alias
// resolution, and callers pass the dev-aware base.
export const SCHEDULES_API_BASE = '/api/v1/hive/agent/schedules';

/**
 * Whether the chat-container proxy bridges /v1/agent/schedules today.
 *
 * False in this first slice, deliberately exported as a const rather than
 * read from config: the honest state of the deployment is "not reachable",
 * and a config knob would invite flipping it before the bridge exists.
 */
export const SCHEDULES_BRIDGE_READY = false;

/** Cadence presets the first slice supports. */
export type ScheduleCadence = 'daily' | 'weekly' | 'interval:N';

export interface AgentSchedule {
	id: string;
	name: string;
	instructions: string;
	schedule: string;
	enabled: boolean;
	next_run_at: string | null;
	last_run_at: string | null;
	last_task_id: string | null;
	last_error: string;
	created_at: string;
	updated_at: string;
}

/** Human label for a cadence string; unknown shapes pass through verbatim. */
export function cadenceLabel(schedule: string): string {
	if (schedule === 'daily' || schedule === 'weekly') {
		return schedule.charAt(0).toUpperCase() + schedule.slice(1);
	}
	const match = schedule.match(/^interval:(\d+)$/);
	if (match) {
		return `Every ${match[1]} hour${Number(match[1]) === 1 ? '' : 's'}`;
	}
	return schedule;
}

/** Client-side mirror of the boundary validation the backend applies. */
export function validateScheduleInput(name: string, instructions: string, schedule: string): string | null {
	if (!name.trim() || name.length > 100) {
		return 'Name must be 1-100 characters.';
	}
	const stripped = instructions.replace(/\r/g, '');
	if (!stripped.trim() || stripped.length > 4000) {
		return 'Instructions must be 1-4000 characters.';
	}
	if (!['daily', 'weekly'].includes(schedule) && !/^interval:([1-9]|[1-9][0-9]|1[0-6][0-8])$/.test(schedule)) {
		return 'Pick daily, weekly, or an hourly interval between 1 and 168.';
	}
	return null;
}
