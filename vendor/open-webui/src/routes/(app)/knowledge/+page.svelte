<script>
	// #1109: the Knowledge nav row pointed at /workspace/knowledge, which sits
	// behind the workspace layout's permission guard and bounced a non-admin
	// without the workspace.knowledge permission straight back home. This route
	// lives outside that guard; see hive/KnowledgeIndex.svelte for the surface.
	import KnowledgeIndex from '$lib/hive/KnowledgeIndex.svelte';
</script>

<KnowledgeIndex />
