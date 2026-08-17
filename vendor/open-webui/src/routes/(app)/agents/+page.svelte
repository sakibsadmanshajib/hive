<script lang="ts">
	import { getContext } from 'svelte';

	import { WEBUI_NAME, showSidebar, mobile } from '$lib/stores';

	import Tooltip from '$lib/components/common/Tooltip.svelte';
	import SidebarIcon from '$lib/components/icons/Sidebar.svelte';
	import AgentTasks from '$lib/hive/AgentTasks.svelte';

	const i18n: any = getContext('i18n');

	/*
	 * The agent workspace as a destination inside the shell, rendered natively.
	 *
	 * This route used to hold a single <iframe> pointed at /agent-workspace/tasks,
	 * which booted apps/agent-console, a second whole application, inside the
	 * page. That frame is gone. The composer and the task list are Svelte
	 * components in this application now (src/lib/hive/AgentTasks.svelte), and the
	 * composer is built from the chat composer's own container and send button, so
	 * the agent surface reads as the same product rather than a panel embedded in
	 * it.
	 *
	 * What made the port possible was never a layout problem, it was a credential
	 * one: this frontend holds Open WebUI's session token, while /v1/agent/*
	 * wants a Supabase token carrying a tenant claim, and the one the OAuth login
	 * produces carries none and is not in the browser anyway. It is resolved
	 * server side now, by the agent proxy in the chat container
	 * (deploy/docker/owui-patches/hive_agent_proxy.py), on the same mechanism this
	 * deployment already runs for chat completions.
	 */
</script>

<svelte:head>
	<title>
		{$i18n.t('Agents')} • {$WEBUI_NAME}
	</title>
</svelte:head>

<div
	class="hv-panel flex flex-col w-full h-screen max-h-[100dvh] transition-width duration-200 ease-in-out {$showSidebar
		? 'md:max-w-[calc(100%-var(--sidebar-width))]'
		: ''} max-w-full"
>
	{#if $mobile}
		<nav class="px-2.5 pt-1.5 w-full drag-region select-none">
			<div class="flex items-center">
				<div class="{$showSidebar ? 'md:hidden' : ''} flex flex-none items-center self-end">
					<Tooltip
						content={$showSidebar ? $i18n.t('Close Sidebar') : $i18n.t('Open Sidebar')}
						interactive={true}
					>
						<button
							id="sidebar-toggle-button"
							class="cursor-pointer flex rounded-lg hover:bg-gray-100 dark:hover:bg-gray-850 transition"
							on:click={() => {
								showSidebar.set(!$showSidebar);
							}}
							aria-label={$showSidebar ? $i18n.t('Close Sidebar') : $i18n.t('Open Sidebar')}
						>
							<div class="self-center p-1.5">
								<SidebarIcon />
							</div>
						</button>
					</Tooltip>
				</div>
			</div>
		</nav>
	{/if}

	<div class="hv-panel-region overflow-y-auto">
		<AgentTasks />
	</div>
</div>
