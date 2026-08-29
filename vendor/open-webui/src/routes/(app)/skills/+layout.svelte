<script lang="ts">
	/*
	 * The user-created skills library, as a Hive destination.
	 *
	 * A Hive route rather than a restored Workspace tab. Issue #783 removed
	 * that tab and Caddyfile.owui's @removedSurfaces rule still 404s its path;
	 * only the "no user-created skills" half of #783 is reversed, because both
	 * premises it rested on are gone. Skills have a Hive product behind them
	 * now (the owner asked for the ability to add one), and tenant owners
	 * stopped being Open WebUI admins on 2026-08-23, so workspace.skills gates
	 * a real audience instead of nobody.
	 *
	 * ponytail: a flat row in the shell for now. This belongs under the
	 * "Customize" destination in the target sidebar grammar; move it there when
	 * that container exists rather than building one for a single item.
	 *
	 * The chrome below is the Workspace layout's own scroll container, minus
	 * its tab bar, because the components this route mounts were authored for
	 * it. Rendering them in a bare flex region instead put the list underneath
	 * the sidebar and pinned the New Skill button to the window corner, which
	 * is what the first proof capture showed.
	 */
	import { onMount } from 'svelte';
	import { goto } from '$app/navigation';

	import { showSidebar, user } from '$lib/stores';

	let loaded = false;

	onMount(() => {
		// The same guard routes/(app)/workspace/+layout.svelte applies, carried
		// here because this route deliberately sits outside that layout. Without
		// it, a deployment that turned the permission off would render a full
		// editor whose only outcome is a 401 from POST /api/v1/skills/create.
		if ($user?.role !== 'admin' && !$user?.permissions?.workspace?.skills) {
			goto('/');
			return;
		}

		loaded = true;
	});
</script>

{#if loaded}
	<div
		class="relative flex flex-col w-full h-screen max-h-[100dvh] transition-width duration-200 ease-in-out {$showSidebar
			? 'md:max-w-[calc(100%-var(--sidebar-width))]'
			: ''} max-w-full"
	>
		<div class="pt-2 pb-1 px-3 md:px-[18px] flex-1 max-h-full overflow-y-auto" id="hv-skills-container">
			<slot />
		</div>
	</div>
{/if}
