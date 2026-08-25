<script lang="ts">
	/*
	 * Artifacts index (issue #1110). The /artifacts path previously matched
	 * no route: the SPA fallback served the app shell for it, a signed-in
	 * visitor got the app layout's boot spinner with nothing behind it, and
	 * a signed-out visitor was bounced through the SSO consent chain. This
	 * page always terminates in one of three terminal states, loading, error,
	 * or the list, so the route can never again render an unexplained
	 * eternal spinner.
	 *
	 * Scoped to the artifact surface on purpose: the sidebar and the rest of
	 * the shell are another builder's files this week (PR #1117), so this
	 * page ships with no nav entry and is reachable by URL only.
	 */
	import { getContext, onMount } from 'svelte';
	import { goto } from '$app/navigation';
	import { WEBUI_NAME, config, showSidebar, mobile } from '$lib/stores';
	import { WEBUI_API_BASE_URL } from '$lib/constants';
	import { getCodeBlockContents, removeAllDetails } from '$lib/utils';
	import { getOutputText } from '$lib/components/chat/Messages/structuredOutput';
	import { injectCsp } from '$lib/utils/csp';

	import SidebarIcon from '$lib/components/icons/Sidebar.svelte';
	import Spinner from '$lib/components/common/Spinner.svelte';
	import SvgPanZoom from '$lib/components/common/SVGPanZoom.svelte';
	import { withTimeout, buildIframeDoc, type ArtifactEntry } from '$lib/hive/artifactIndex';

	const i18n: any = getContext('i18n');

	let status: 'loading' | 'error' | 'ready' = 'loading';
	let errorMsg = '';
	let artifacts: ArtifactEntry[] = [];
	let selectedIdx: number | null = null;
	let scanToken = 0;

	const extractArtifacts = (chat: any): ArtifactEntry[] => {
		const entries: ArtifactEntry[] = [];
		const messages = chat?.chat?.messages ?? [];
		for (const message of messages) {
			const messageContent =
				getOutputText(message?.output) || removeAllDetails(message?.content ?? '');
			if (!messageContent.trim()) {
				continue;
			}
			const { codeBlocks, htmlGroups } = getCodeBlockContents(messageContent);
			if (htmlGroups && htmlGroups.length > 0) {
				for (const group of htmlGroups) {
					entries.push({
						chatId: chat.id,
						chatTitle: chat.title,
						artifactType: 'iframe',
						content: buildIframeDoc(group)
					});
				}
			} else {
				for (const block of codeBlocks) {
					if (block.lang === 'svg' || (block.lang === 'xml' && block.code.includes('<svg'))) {
						entries.push({
							chatId: chat.id,
							chatTitle: chat.title,
							artifactType: 'svg',
							content: block.code
						});
					}
				}
			}
		}
		return entries;
	};

	const load = async () => {
		const token = localStorage.token;
		if (!token) {
			errorMsg = $i18n.t('You are not signed in.');
			status = 'error';
			return;
		}
		status = 'loading';
		errorMsg = '';
		const myToken = ++scanToken;
		const ac = new AbortController();
		try {
			// The NDJSON export endpoint streams every chat with full message
			// content, one request. Parsed incrementally and stopped at the
			// artifact cap, so a large history costs only what the cap needs,
			// and the whole load is bounded by withTimeout so a slow or dead
			// backend becomes a visible error instead of an eternal spinner.
			// The AbortController makes the timeout real: the response body is
			// torn down when the load fails or the user has navigated on.
			const found = await withTimeout(
				(async () => {
					const res = await fetch(`${WEBUI_API_BASE_URL}/chats/all`, {
						headers: { Accept: 'application/x-ndjson', authorization: `Bearer ${token}` },
						signal: ac.signal
					});
					if (!res.ok || !res.body) {
						throw new Error(`HTTP ${res.status}`);
					}
					const reader = res.body.getReader();
					const decoder = new TextDecoder();
					const found: ArtifactEntry[] = [];
					let buffer = '';
					stream: while (true) {
						const { done, value } = await reader.read();
						if (done) {
							break;
						}
						buffer += decoder.decode(value, { stream: true });
						const lines = buffer.split('\n');
						buffer = lines.pop() ?? '';
						for (const line of lines) {
							if (!line.trim()) {
								continue;
							}
							found.push(...extractArtifacts(JSON.parse(line)));
							if (found.length >= 50) {
								break stream;
							}
						}
					}
					// The generator ends every record with a newline, so the
					// buffer is normally empty here; parse it anyway so a
					// final record without the trailing newline still counts.
					buffer += decoder.decode();
					const rest = buffer.trim();
					if (rest) {
						found.push(...extractArtifacts(JSON.parse(rest)));
					}
					try {
						await reader.cancel();
					} catch {
						// the body may already be closed when the cap ended the loop
					}
					// A single chat can carry several artifacts, so the last
					// push can overshoot the cap; trim to it.
					return found.slice(0, 50);
				})(),
				20000
			);
			if (myToken !== scanToken) {
				return;
			}
			artifacts = found;
			selectedIdx = null;
			status = 'ready';
		} catch (e: any) {
			ac.abort();
			if (myToken === scanToken) {
				if (e?.message !== 'timeout') {
					// Provider names and internal detail never reach the customer:
					// log the real error, show a fixed message.
					console.error('Failed to load artifacts:', e);
				}
				errorMsg =
					e?.message === 'timeout'
						? $i18n.t('Loading your artifacts took too long. Try again.')
						: $i18n.t('Please try again.');
				status = 'error';
			}
		}
	};

	onMount(() => {
		load();
	});

	const openChat = (chatId: string) => {
		goto(`/c/${chatId}`);
	};
</script>

<svelte:head>
	<title>{$i18n.t('Artifacts')} • {$WEBUI_NAME}</title>
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
				</div>
			</div>
		</nav>
	{/if}

	<div class="hv-panel-region">
		{#if status === 'loading'}
			<div class="m-auto flex flex-col items-center gap-3 text-center">
				<Spinner className="size-5" />
				<div class="text-sm font-medium text-gray-900 dark:text-white">
					{$i18n.t('Loading your artifacts...')}
				</div>
			</div>
		{:else if status === 'error'}
			<div class="m-auto flex flex-col items-center gap-3 text-center px-6">
				<div class="text-sm font-medium text-gray-900 dark:text-white">
					{$i18n.t('Could not load your artifacts.')}
				</div>
				{#if errorMsg}
					<div class="text-xs text-gray-500 dark:text-gray-400 max-w-md">{errorMsg}</div>
				{/if}
				<button
					class="text-sm font-medium px-4 py-2 rounded-lg bg-gray-50 hover:bg-gray-100 dark:bg-gray-850 dark:hover:bg-gray-800 transition"
					on:click={load}
				>
					{$i18n.t('Retry')}
				</button>
			</div>
		{:else if artifacts.length === 0}
			<div class="m-auto flex flex-col items-center gap-2 text-center px-6">
				<div class="text-sm font-medium text-gray-900 dark:text-white">
					{$i18n.t('No artifacts yet.')}
				</div>
				<div class="text-xs text-gray-500 dark:text-gray-400 max-w-md">
					{$i18n.t(
						'Ask the model for a web page, a chart or an interactive snippet, and it will show up here.'
					)}
				</div>
			</div>
		{:else if selectedIdx !== null}
			<div class="flex flex-col w-full h-full">
				<div
					class="flex items-center justify-between gap-2 px-4 py-2.5 border-b border-gray-100 dark:border-gray-800"
				>
					<div class="flex items-center gap-2 min-w-0">
						<button
							class="self-center p-1 hover:bg-black/5 dark:hover:bg-white/5 hover:text-black dark:hover:text-white rounded-md transition"
							on:click={() => {
								selectedIdx = null;
							}}
							aria-label={$i18n.t('Back')}
						>
							<svg
								xmlns="http://www.w3.org/2000/svg"
								fill="none"
								viewBox="0 0 24 24"
								stroke-width="2"
								stroke="currentColor"
								class="size-4"
							>
								<path
									stroke-linecap="round"
									stroke-linejoin="round"
									d="M15.75 19.5 8.25 12l7.5-7.5"
								/>
							</svg>
						</button>
						<div class="text-sm font-medium truncate text-gray-900 dark:text-white">
							{artifacts[selectedIdx].chatTitle}
						</div>
					</div>
					<button
						class="text-xs font-medium px-2.5 py-1 rounded-lg bg-gray-50 hover:bg-gray-100 dark:bg-gray-850 dark:hover:bg-gray-800 transition shrink-0"
						on:click={() => {
							openChat(artifacts[selectedIdx].chatId);
						}}
					>
						{$i18n.t('Open chat')}
					</button>
				</div>
				<div class="flex-1 w-full h-full">
					{#if artifacts[selectedIdx].artifactType === 'iframe'}
						<iframe
							title="Artifact preview"
							srcdoc={injectCsp(artifacts[selectedIdx].content, $config?.ui?.iframe_csp ?? '')}
							class="w-full border-0 h-full"
							sandbox="allow-scripts allow-downloads"
						></iframe>
					{:else}
						<SvgPanZoom
							className="w-full h-full max-h-full overflow-hidden"
							svg={artifacts[selectedIdx].content}
						/>
					{/if}
				</div>
			</div>
		{:else}
			<div class="flex flex-col w-full h-full overflow-y-auto">
				<div class="px-6 pt-6 pb-3">
					<div class="text-lg font-medium text-gray-900 dark:text-white">
						{$i18n.t('Artifacts')}
					</div>
					<div class="text-xs text-gray-500 dark:text-gray-400 mt-1">
						{$i18n.t('Web pages, charts and snippets the model built in your recent chats.')}
					</div>
				</div>
				<div class="px-6 pb-8 flex flex-col gap-2">
					{#each artifacts as artifact, idx}
						<button
							class="flex items-center justify-between gap-3 px-4 py-3 rounded-xl border border-gray-100 dark:border-gray-800 hover:bg-gray-50 dark:hover:bg-gray-850 transition text-left"
							on:click={() => {
								selectedIdx = idx;
							}}
						>
							<div class="flex flex-col gap-0.5 min-w-0">
								<div class="text-sm font-medium truncate text-gray-900 dark:text-white">
									{artifact.chatTitle}
								</div>
								<div class="text-xs text-gray-500 dark:text-gray-400">
									{artifact.artifactType === 'iframe'
										? $i18n.t('Web page')
										: $i18n.t('SVG')}
								</div>
							</div>
							<div class="text-xs font-medium text-gray-500 dark:text-gray-400 shrink-0">
								{$i18n.t('Open')}
							</div>
						</button>
					{/each}
				</div>
			</div>
		{/if}
	</div>
</div>
