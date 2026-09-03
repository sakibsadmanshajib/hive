<script lang="ts">
	import { goto } from '$app/navigation';
	import { onMount } from 'svelte';

	/*
	 * Hive: the Workspace index redirects to Projects (#1505).
	 *
	 * It used to send every session to /workspace/knowledge, the stock Knowledge
	 * management page, because Knowledge was the only Workspace surface this
	 * product still shipped: Models, Prompts, Tools and Skills are gone, and
	 * Caddyfile.owui's @removedSurfaces rule 404s all four paths.
	 *
	 * That destination was dead for an ordinary customer, since the layout's own
	 * guard bounced any session without the knowledge permission back to /, and
	 * nobody had that permission. Granting it (this issue) would have brought
	 * the page back to life as a second Knowledge destination sitting beside
	 * Projects, reachable from the user menu's Workspace entry. D-045 eliminates
	 * Knowledge rather than keeping two of it, so the index points at Projects,
	 * which is where the same collections live.
	 *
	 * /workspace/knowledge itself is untouched and keeps its route, unlinked,
	 * exactly as /agents has since #944.
	 */
	onMount(() => {
		goto('/projects');
	});
</script>
