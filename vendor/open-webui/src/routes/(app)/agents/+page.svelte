<script lang="ts">
	import { getContext, onMount, onDestroy } from 'svelte';

	import { WEBUI_NAME, showSidebar, mobile, theme } from '$lib/stores';

	import Tooltip from '$lib/components/common/Tooltip.svelte';
	import SidebarIcon from '$lib/components/icons/Sidebar.svelte';

	const i18n: any = getContext('i18n');

	/*
	 * The agent workspace as a destination inside the shell.
	 *
	 * Until the task console is ported to Svelte it stays the Next.js
	 * application that already owns it, served by the same Caddy listener at
	 * /agent-workspace on this origin (deploy/docker/Caddyfile.owui). Rendering
	 * it here keeps the sidebar, the identity and the origin: the agent stops
	 * being a link out of the product and becomes a room inside it.
	 *
	 * ponytail: same origin frame, not a port. The port is real work and it is
	 * blocked on a token bridge, because this frontend holds Open WebUI's own
	 * session token in localStorage while /v1/agent/* authenticates the
	 * Supabase bearer that only the embedded application carries. When that
	 * bridge exists the frame is replaced by native rows and this file becomes
	 * the transcript.
	 *
	 * Known ceiling, deliberately not papered over: if the embedded
	 * application has no session of its own it renders its own sign in inside
	 * this frame. The fix is the single sign on work, not a spinner here.
	 */
	const AGENT_PANEL_PATH = '/agent-workspace/tasks';

	let systemPrefersDark = false;
	let media: MediaQueryList | null = null;

	const onSystemThemeChange = (event: MediaQueryListEvent) => {
		systemPrefersDark = event.matches;
	};

	onMount(() => {
		media = window.matchMedia('(prefers-color-scheme: dark)');
		systemPrefersDark = media.matches;
		media.addEventListener('change', onSystemThemeChange);
	});

	onDestroy(() => {
		media?.removeEventListener('change', onSystemThemeChange);
	});

	// Same resolution the layout applies to the document class, so the panel
	// and the shell are never in two different themes.
	$: resolvedTheme = $theme.includes('dark')
		? 'dark'
		: $theme === 'system' && systemPrefersDark
			? 'dark'
			: 'light';

	$: panelSrc = `${AGENT_PANEL_PATH}?embed=1&theme=${resolvedTheme}`;

	let panelReady = false;
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

	<!--
		Keyed on the resolved theme so a theme change reloads the panel with the
		matching palette. Rebuilding the frame is acceptable here because a theme
		change is a rare, deliberate act; nothing else remounts it.
	-->
	{#key resolvedTheme}
		<div class="hv-panel-region">
			{#if !panelReady}
				<!--
					Never a blank region: the panel is same origin and resolves fast, but
					"fast" is not "instant" on a cold container, and an empty rectangle is
					the state that reads as broken. One line, no spinner, and it sits
					behind the frame rather than in front of it (see hive.css).
				-->
				<p class="hv-panel-loading">{$i18n.t('Opening the agent workspace')}</p>
			{/if}
			<iframe
				class="hv-panel-frame"
				src={panelSrc}
				title={$i18n.t('Agent workspace')}
				loading="eager"
				on:load={() => {
					panelReady = true;
				}}
			></iframe>
		</div>
	{/key}
</div>
