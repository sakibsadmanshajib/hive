<script lang="ts">
	import { goto } from '$app/navigation';
	import { onMount } from 'svelte';

	/*
	 * Hive (#1505): this used to render KnowledgeIndex.svelte, a list of the
	 * collections shared with you and nothing else. It had no create control and
	 * no file input, so the one thing a customer needed to do here was the one
	 * thing it could not do, and its own empty state said so: "No knowledge
	 * bases are shared with you yet."
	 *
	 * Projects is the surface that can do it. A Project IS a knowledge
	 * collection on this backend (lib/hive/projects/projects.ts), so this is not
	 * two stores with a link between them: the rows behind the two destinations
	 * were always the same rows, and Projects is the one that can create, upload
	 * and delete. D-045 eliminates Knowledge rather than renaming it, and issue
	 * #1595 settles that the whole destination goes.
	 *
	 * A redirect rather than a deleted route, so the bookmarks and the shell's
	 * own older links land somewhere real instead of on a 404.
	 */
	onMount(() => {
		goto('/projects');
	});
</script>
