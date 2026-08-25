<script lang="ts">
	import { getContext, onMount } from 'svelte';
	import { goto } from '$app/navigation';
	import dayjs from 'dayjs';
	import relativeTime from 'dayjs/plugin/relativeTime';

	dayjs.extend(relativeTime);

	import { user } from '$lib/stores';
	import { getKnowledgeBases } from '$lib/apis/knowledge';

	const i18n: any = getContext('i18n');

	/*
	 * A read-only index of the signed-in caller's own knowledge bases.
	 *
	 * The sidebar's Knowledge row points at /knowledge rather than at
	 * /workspace/knowledge because that page sits behind the workspace layout's
	 * permission guard, which bounces a non-admin without the workspace.knowledge
	 * permission straight home (#1109). The bounce is what made the row read as
	 * dead: the click navigates fine, and the guard sends the person back before
	 * anything renders. This surface lives outside that guard and reads
	 * GET /api/v1/knowledge/ directly, which needs only a verified user; it lists
	 * what the caller can actually see, so an account with no bases gets an
	 * honest empty state rather than a redirect.
	 *
	 * An admin or an account holding the workspace.knowledge permission is
	 * forwarded to /workspace/knowledge, the full management surface (create,
	 * upload, delete, sharing). Everyone else gets this list.
	 */

	let loaded = false;
	let error: string | null = null;
	type KnowledgeBase = {
		id: string;
		name: string;
		description?: string | null;
		updated_at: number;
	};
	let items: KnowledgeBase[] = [];

	onMount(async () => {
		if ($user?.role === 'admin' || $user?.permissions?.workspace?.knowledge) {
			goto('/workspace/knowledge');
			return;
		}

		try {
			const res = await getKnowledgeBases(localStorage.token);
			// A null return is the client's own failure path (the fetch helper
			// swallows a rejected request whose error carried no detail), not an
			// empty account. Mapping it to [] would report a network failure as
			// a fact about the caller's data.
			if (!res) {
				throw new Error('knowledge list unavailable');
			}
			items = res.items ?? [];
		} catch {
			// The backend's `detail` string is never rendered: error copy at this
			// boundary is one translated generic message, per the provider-blind
			// errors rule.
			error = $i18n.t('Failed to load your knowledge bases.');
		} finally {
			loaded = true;
		}
	});

	const updated = (ts: number): string => dayjs(ts * 1000).fromNow();
</script>

<div class="hv-panel-region">
	<div class="hv-kb" data-hive-knowledge-index>
		<header>
			<h1 class="hv-kb-title">{$i18n.t('Knowledge')}</h1>
			<p class="hv-kb-subtitle">{$i18n.t('The documents your chats can search.')}</p>
		</header>

		{#if !loaded}
			<p class="hv-kb-status">{$i18n.t('Loading...')}</p>
		{:else if error}
			<p class="hv-kb-status">{error}</p>
		{:else if items.length === 0}
			<p class="hv-kb-status">{$i18n.t('No knowledge bases are shared with you yet.')}</p>
		{:else}
			<ul class="hv-kb-list">
				{#each items as kb (kb.id)}
					<li class="hv-kb-item">
						<div class="hv-kb-item-main">
							<p class="hv-kb-name">{kb.name}</p>
							{#if kb.description}
								<p class="hv-kb-description">{kb.description}</p>
							{/if}
						</div>
						<time class="hv-kb-updated">{updated(kb.updated_at)}</time>
					</li>
				{/each}
			</ul>
		{/if}
	</div>
</div>
