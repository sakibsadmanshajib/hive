<script lang="ts">
	/*
	 * Not found, rendered inside the shell.
	 *
	 * Before this route existed, any path the application did not recognise fell
	 * through to SvelteKit's root +error.svelte, which is a single line of text
	 * on a bare page: no sidebar, no chrome, no way back, and nothing that looks
	 * like the product. Three of the four routes probed in the 2026-08-23 capture
	 * set landed there, and it was the worst single frame in the whole set.
	 *
	 * It has to be a catch-all page rather than an `(app)/+error.svelte`, and the
	 * distinction is not cosmetic: for a URL that matches NO route, SvelteKit has
	 * no layout to render an error inside, so it uses the root error page and a
	 * group-level one is never reached. Claiming the unmatched path with a real
	 * page is the only way the shell's layout runs at all.
	 *
	 * More specific routes always win over a rest parameter, so every real
	 * destination is unaffected, and the route groups outside (app) -- auth, s,
	 * watch, error -- are matched before this and keep their own treatment.
	 *
	 * Shape copied from the agent surface's empty state, which is the one good
	 * empty state already in the product: a bordered card, a short title, one
	 * explanatory sentence, and something to do next.
	 */
	import { getContext } from 'svelte';
	import { page } from '$app/stores';

	import { WEBUI_NAME, showSidebar, mobile } from '$lib/stores';

	import Tooltip from '$lib/components/common/Tooltip.svelte';
	import SidebarIcon from '$lib/components/icons/Sidebar.svelte';

	const i18n: any = getContext('i18n');
</script>

<svelte:head>
	<title>
		{$i18n.t('Not found')} • {$WEBUI_NAME}
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
		<div class="hv-notfound" data-hive-notfound>
			<p class="hv-notfound-code">404</p>
			<h1 class="hv-notfound-title">{$i18n.t('This page does not exist')}</h1>
			<p class="hv-notfound-body">
				<!-- The path is echoed because the usual cause is a stale bookmark or a
				     mistyped URL, and a person cannot correct one they cannot see. It
				     is rendered as text, never as markup, so a crafted URL cannot put
				     anything into this page. -->
				{$i18n.t('Nothing is served at')}
				{$page.url.pathname}.
			</p>
			<a class="hv-notfound-action" href="/">{$i18n.t('Back to chat')}</a>
		</div>
	</div>
</div>
