<script lang="ts">
	/*
	 * Scheduled tasks route, first slice.
	 *
	 * Its own route file per the slice brief: nav.ts is untouched, a later
	 * wave owns adding the nav row. The panel is the same hv-panel shell the
	 * agents page uses.
	 */
	import { getContext } from 'svelte';
	import { WEBUI_NAME, showSidebar, mobile } from '$lib/stores';

	import Tooltip from '$lib/components/common/Tooltip.svelte';
	import SidebarIcon from '$lib/components/icons/Sidebar.svelte';
	import AgentSchedules from '$lib/hive/AgentSchedules.svelte';

	const i18n: any = getContext('i18n');
</script>

<svelte:head>
	<title>
		{$i18n.t('Scheduled')} {$i18n.t('Tasks')} • {$WEBUI_NAME}
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

	<div class="hv-panel-region">
		<AgentSchedules />
	</div>
</div>
