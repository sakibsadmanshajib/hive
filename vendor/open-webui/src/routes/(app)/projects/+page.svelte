<script lang="ts">
	import { getContext } from 'svelte';

	import { WEBUI_NAME, showSidebar, mobile } from '$lib/stores';

	import Tooltip from '$lib/components/common/Tooltip.svelte';
	import SidebarIcon from '$lib/components/icons/Sidebar.svelte';
	import ProjectsList from '$lib/hive/projects/ProjectsList.svelte';

	const i18n: any = getContext('i18n');

	/*
	 * The Projects destination inside the shell, per D-045 ruling 2.
	 *
	 * Same frame as every other destination: this route renders into the one
	 * shell, with the same sidebar and chrome, and swaps nothing.
	 */
</script>

<div
	class="hv-panel flex flex-col w-full h-screen max-h-[100dvh] transition-width duration-200 ease-in-out {$showSidebar
		? 'md:max-w-[calc(100%-var(--sidebar-width))]'
		: ''} max-w-full"
>
	{#if $mobile}
		<nav class="px-2.5 pt-1.5 w-full drag-region select-none">
			<div class="flex items-center">
				<div class="{$showSidebar ? 'md:hidden' : ''} flex flex-none items-center self-end">
					<Tooltip content={$showSidebar ? $i18n.t('Close Sidebar') : $i18n.t('Open Sidebar')} interactive={true}>
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
		<ProjectsList />
	</div>
</div>
