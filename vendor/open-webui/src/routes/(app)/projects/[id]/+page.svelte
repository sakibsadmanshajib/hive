<script lang="ts">
	import { getContext } from 'svelte';
	import { page } from '$app/stores';

	import { WEBUI_NAME, showSidebar, mobile } from '$lib/stores';

	import Tooltip from '$lib/components/common/Tooltip.svelte';
	import SidebarIcon from '$lib/components/icons/Sidebar.svelte';
	import ProjectDetail from '$lib/hive/projects/ProjectDetail.svelte';

	const i18n: any = getContext('i18n');

	/*
	 * One project's contents, inside the same shell as everything else. The
	 * project id comes from the route, the component owns the rest.
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
		<ProjectDetail id={$page.params.id ?? ''} />
	</div>
</div>
