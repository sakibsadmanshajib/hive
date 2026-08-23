<script lang="ts">
	/*
	 * Scheduled agent tasks ("routines") panel, first slice.
	 *
	 * The backend (control-plane scheduler + edge-api CRUD) is real and tested;
	 * what is missing is the credential bridge. hive_agent_proxy.py forwards
	 * only task operations to /v1/agent/*, so this page cannot reach
	 * /v1/agent/schedules from the browser yet. Until that bridge wave lands,
	 * everything renders behind SCHEDULES_BRIDGE_READY=false with an explicit
	 * notice, per the slice brief's "say so plainly" requirement.
	 */
	import { getContext } from 'svelte';
	import { toast } from 'svelte-sonner';

	import {
		SCHEDULES_API_BASE,
		SCHEDULES_BRIDGE_READY,
		cadenceLabel,
		validateScheduleInput,
		type AgentSchedule
	} from './agentSchedules';

	const i18n: any = getContext('i18n');

	let schedules: AgentSchedule[] = [];
	let name = '';
	let instructions = '';
	let cadence = 'daily';
	let busy = false;
	let pendingDelete: AgentSchedule | null = null;

	async function load() {
		try {
			const res = await fetch(SCHEDULES_API_BASE);
			if (!res.ok) throw new Error(String(res.status));
			const data = await res.json();
			schedules = data.schedules ?? [];
		} catch (e) {
			toast.error($i18n.t('Could not load schedules'));
			console.log('agent schedules load failed', e);
		}
	}

	async function create() {
		const problem = validateScheduleInput(name, instructions, cadence);
		if (problem) {
			toast.error($i18n.t(problem));
			return;
		}
		busy = true;
		try {
			const res = await fetch(SCHEDULES_API_BASE, {
				method: 'POST',
				headers: { 'Content-Type': 'application/json' },
				body: JSON.stringify({ name, instructions, schedule: cadence })
			});
			if (!res.ok) throw new Error(String(res.status));
			name = '';
				instructions = '';
			await load();
		} catch {
			toast.error($i18n.t('Could not create schedule'));
		} finally {
			busy = false;
		}
	}

	async function toggle(s: AgentSchedule) {
		busy = true;
		try {
			const res = await fetch(`${SCHEDULES_API_BASE}/${s.id}`, {
				method: 'PUT',
				headers: { 'Content-Type': 'application/json' },
				body: JSON.stringify({
					name: s.name,
					instructions: s.instructions,
					schedule: s.schedule,
					enabled: !s.enabled
				})
			});
			if (!res.ok) throw new Error(String(res.status));
			await load();
		} catch {
			toast.error($i18n.t('Could not update schedule'));
		} finally {
			busy = false;
		}
	}

	function confirmDelete(s: AgentSchedule) {
		// #866 lesson: destructive actions need confirmation before firing.
		pendingDelete = s;
	}

	async function doDelete() {
		if (!pendingDelete) return;
		const target = pendingDelete;
		pendingDelete = null;
		busy = true;
		try {
			const res = await fetch(`${SCHEDULES_API_BASE}/${target.id}`, { method: 'DELETE' });
			if (!res.ok) throw new Error(String(res.status));
			await load();
		} catch {
			toast.error($i18n.t('Could not delete schedule'));
		} finally {
			busy = false;
		}
	}

	// The flag is a compile-time constant today; this guard keeps the list
	// load off the wire while the bridge is pending.
	if (SCHEDULES_BRIDGE_READY) {
		void load();
	}
</script>

<div class="hv-panel flex flex-col w-full h-screen max-h-[100dvh] max-w-full">
	<div class="px-6 py-4 border-b border-gray-100 dark:border-gray-850">
		<h2 class="text-lg font-medium">Scheduled tasks</h2>
	</div>

	{#if !SCHEDULES_BRIDGE_READY}
		<div class="flex-1 flex items-center items-center justify-center px-6">
			<div class="max-w-md text-center space-y-2 text-sm text-gray-500 dark:text-gray-400">
				<p class="font-medium text-gray-700 dark:text-gray-300">Not available yet</p>
				<p>
					Scheduled task management has not been bridged into the chat backend on this
					deployment yet. The scheduling engine runs server side; this panel lights up once its
					routes are reachable from here.
				</p>
			</div>
		</div>
	{:else}
		<div class="flex-1 overflow-y-auto px-6 py-4 space-y-4">
			<!-- Create form -->
			<div class="rounded-xl border border-gray-100 dark:border-gray-850 p-4 space-y-3">
				<input class="w-full rounded-lg px-3 py-2 text-sm bg-transparent border border-gray-200 dark:border-gray-800" bind:value={name} placeholder="Name" />
				<textarea rows="4" class="w-full rounded-lg px-3 py-2 text-sm bg-transparent border border-gray-200 dark:border-gray-800" bind:value={instructions} placeholder={$i18n.t('Instructions (these become the prompt each run)')} />
				<select class="w-full rounded-lg px-3 py-2 text-sm bg-transparent border border-gray-200 dark:border-gray-800" bind:value={cadence}>
					<option value="daily">Daily</option>
					<option value="weekly">Weekly</option>
					<option value="interval:1">Every hour</option>
					<option value="interval:6">Every 6 hours</option>
				</select>
				<button disabled={busy} class="px-3 py-1.5 text-sm rounded-lg bg-black text-white dark:bg-white dark:text-black" on:click={create}>
					{$i18n.t('Create schedule')}
				</button>
			</div>

			<!-- List -->
			{#each schedules as s (s.id)}
				<div class="flex items-start justify-between rounded-xl border border-gray-100 dark:border-gray-850 p-3">
					<div class="min-w-0">
						<p class="font-medium truncate">{s.name}</p>
						<p class="text-xs text-gray-500">{cadenceLabel(s.schedule)}</p>
						{#if s.last_error}
							<p class="text-xs text-red-500">Last run failed: {s.last_error}</p>
						{/if}
					</div>
					<div class="flex gap-2 flex-none">
						<button disabled={busy} class="text-sm px-2 py-1 rounded-lg hover:bg-gray-100 dark:hover:bg-gray-850" on:click={() => toggle(s)}>
							{s.enabled ? $i18n.t('Disable') : $i18n.t('Enable')}
						</button>
						<button disabled={busy} class="text-sm px-2 py-1 rounded-lg text-red-500 hover:bg-red-50" on:click={() => confirmDelete(s)}>
							{$i18n.t('Delete')}
						</button>
					</div>
				</div>
			{/each}
		</div>

		<!-- Delete confirmation (#866) -->
		{#if pendingDelete}
			<svelte:window on:keydown={(e) => e.key === 'Escape' && (pendingDelete = null)} />
			<div class="fixed inset-0 z-50 flex items-center justify-center bg-black/40">
				<div role="dialog" aria-modal="true" class="bg-white dark:bg-gray-900 rounded-xl p-5 max-w-sm space-y-3 mx-4">
					<p class="font-medium">Delete "{pendingDelete.name}"?</p>
					<p class="text-sm text-gray-500">Future runs stop immediately. Already created tasks stay.</p>
					<div class="flex justify-end gap-2">
						<button class="px-3 py-1.5 text-sm rounded-lg border" on:click={() => (pendingDelete = null)}>Cancel</button>
						<button class="px-3 py-1.5 text-sm rounded-lg bg-red-600 text-white" on:click={doDelete}>Delete</button>
					</div>
				</div>
			</div>
		{/if}
	{/if}
</div>
