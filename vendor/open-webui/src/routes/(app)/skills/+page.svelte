<script lang="ts">
	/*
	 * The user-created skills library.
	 *
	 * A Hive route rather than a restored /workspace/skills. Issue #783 removed
	 * the Workspace tab and Caddyfile.owui's @removedSurfaces rule still 404s
	 * that path; only the "no user-created skills" half of #783 is reversed,
	 * because the two premises it rested on are both gone. Skills now have a
	 * Hive product behind them (the owner asked for the ability to add one), and
	 * tenant owners stopped being Open WebUI admins on 2026-08-23, so
	 * workspace.skills gates a real audience instead of nobody.
	 *
	 * Living outside routes/(app)/workspace/ also keeps this clear of that
	 * layout's own permission guard, the same reason #1109 moved Knowledge to
	 * /knowledge. The guard itself is repeated below rather than inherited.
	 *
	 * ponytail: a flat row in the shell for now. This belongs under the
	 * "Customize" destination in the target sidebar grammar; move it there when
	 * that container exists rather than building one for a single item.
	 */
	import { onMount } from 'svelte';
	import { goto } from '$app/navigation';

	import { user } from '$lib/stores';
	import Skills from '$lib/components/workspace/Skills.svelte';

	let loaded = false;

	onMount(() => {
		// Same shape as routes/(app)/workspace/+layout.svelte's guard. Without
		// it a deployment that turned the permission off would render a full
		// editor whose only outcome is a 401 from POST /api/v1/skills/create.
		if ($user?.role !== 'admin' && !$user?.permissions?.workspace?.skills) {
			goto('/');
			return;
		}

		loaded = true;
	});
</script>

{#if loaded}
	<div class="hv-panel-region">
		<Skills />
	</div>
{/if}
