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
	 * /workspace/knowledge is gone rather than merely unlinked: Caddyfile.owui
	 * 404s the path, and this change also deletes the tab in +layout.svelte
	 * that pointed at it, which the granted permission would otherwise have
	 * made visible to every ordinary account.
	 *
	 * replaceState, so a visitor who arrives here and presses Back is not
	 * pushed straight forward again.
	 */
	onMount(() => {
		goto('/projects', { replaceState: true });
	});
</script>
