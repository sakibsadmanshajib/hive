<script lang="ts">
	import { goto } from '$app/navigation';
	import { onMount } from 'svelte';

	/*
	 * Hive: Knowledge is the only Workspace surface this product ships, so the
	 * index is an unconditional redirect to it rather than a chain that walks
	 * per-permission to Models, Prompts, Tools or Skills. Those four are gone
	 * (see +layout.svelte), and a session without knowledge permission is
	 * bounced to / by that layout's own guard, which is unchanged.
	 */
	onMount(() => {
		goto('/workspace/knowledge');
	});
</script>
