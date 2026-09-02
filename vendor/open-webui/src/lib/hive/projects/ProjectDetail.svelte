<script lang="ts">
	/*
	 * One project's contents: the files bound to its scope and the
	 * conversations bound to it.
	 *
	 * Hive authored; storage seam documented in ./projects.ts. Files are the
	 * knowledge collection's files, so anything attached here is exactly what
	 * RAG retrieval pulls in for work against this project. Conversations carry
	 * their binding inside their own persisted blob (`hiveProject`). "New chat"
	 * hands the marker to the composer on the URL and the composer writes it
	 * when it creates the chat; "Link existing" writes it after the fact
	 * through the same merge.
	 */
	import { getContext, onMount } from 'svelte';
	import { goto } from '$app/navigation';

	import Tooltip from '$lib/components/common/Tooltip.svelte';
	import { WEBUI_NAME } from '$lib/stores';
	import { uploadFile } from '$lib/apis/files';
	import { getChatList } from '$lib/apis/chats';

	import {
		type HiveProjectConversation,
		ProjectError,
		addFileToProject,
		bindChatToProject,
		deleteProject,
		getProject,
		removeFileFromProject,
		resolveProjectConversations,
		updateProject,
		type HiveProject,
		type HiveProjectFile
	} from './projects';

	const i18n: any = getContext('i18n');

	export let id: string;

	let token = '';
	let project: (HiveProject & { files: HiveProjectFile[]; writeAccess: boolean }) | null = null;
	let conversations: HiveProjectConversation[] = [];
	let loading = true;
	let error = '';

	let showEdit = false;
	let editName = '';
	let editDescription = '';
	let busy = false;

	let showLinkPicker = false;
	let recentChats: Array<{ id: string; title: string }> = [];
	let linkedIds = new Set<string>();

	const load = async () => {
		error = '';
		try {
			project = await getProject(token, id);
			if (!project) {
				// The null branch below renders its own line; a second message
				// here would say the same thing twice.
				return;
			}
			conversations = await resolveProjectConversations(token, id);
			linkedIds = new Set(conversations.map((c) => c.id));
		} catch (err) {
			error =
				err instanceof ProjectError ? err.message : $i18n.t('The project could not be loaded.');
		} finally {
			loading = false;
		}
	};

	onMount(async () => {
		token = localStorage.getItem('token') ?? '';
		await load();
	});

	const relativeDate = (epochSeconds: number): string => {
		if (!epochSeconds) return '';
		return new Date(epochSeconds * 1000).toLocaleString();
	};

	const openEdit = () => {
		if (!project) return;
		editName = project.name;
		editDescription = project.description;
		showEdit = true;
	};

	const submitEdit = async () => {
		if (!project || !editName.trim() || busy) return;
		busy = true;
		error = '';
		try {
			project = {
				...project,
				...(await updateProject(token, id, {
					name: editName.trim(),
					description: editDescription.trim()
				}))
			};
			showEdit = false;
		} catch (err) {
			error =
				err instanceof ProjectError ? err.message : $i18n.t('The project could not be updated.');
		} finally {
			busy = false;
		}
	};

	const confirmDelete = async () => {
		if (!project || busy) return;
		if (!window.confirm($i18n.t('Delete this project? Its conversations stay in your history.'))) {
			return;
		}
		busy = true;
		try {
			await deleteProject(token, id);
			await goto('/projects');
		} catch (err) {
			error =
				err instanceof ProjectError ? err.message : $i18n.t('The project could not be deleted.');
			busy = false;
		}
	};

	const onFilesChosen = async (event: Event) => {
		const inputEl = event.currentTarget as HTMLInputElement;
		const chosen = Array.from(inputEl.files ?? []);
		inputEl.value = '';
		if (!project || chosen.length === 0 || busy) return;
		busy = true;
		error = '';
		const attached: string[] = [];
		try {
			for (const file of chosen) {
				const uploaded = await uploadFile(token, file);
				if (!uploaded?.id) throw new ProjectError($i18n.t('The file could not be uploaded.'));
				await addFileToProject(token, id, uploaded.id);
				attached.push(uploaded.id);
			}
			project = await getProject(token, id);
		} catch (err) {
			// A mid-batch failure would otherwise leave earlier files attached
			// while the UI reports total failure. Roll back what landed, best
			// effort, then reload so the list matches the server.
			for (const fileId of attached) {
				try {
					await removeFileFromProject(token, id, fileId);
				} catch {
					// Best effort: the reload below still shows the truth.
				}
			}
			error =
				err instanceof ProjectError ? err.message : $i18n.t('The file could not be added.');
			try {
				project = await getProject(token, id);
			} catch {
				// The error line above already says what failed.
			}
		} finally {
			busy = false;
		}
	};

	const detachFile = async (fileId: string) => {
		if (!project || busy) return;
		busy = true;
		try {
			await removeFileFromProject(token, id, fileId);
			project = await getProject(token, id);
		} catch (err) {
			error =
				err instanceof ProjectError ? err.message : $i18n.t('The file could not be removed.');
		} finally {
			busy = false;
		}
	};

	const startNewChat = async () => {
		if (busy) return;
		// Not created here. The composer creates it on the first message, the
		// way every other conversation is created, and picks the binding up off
		// the URL; a chat pre created here would carry a chat_id on its first
		// request and the backend would then skip title and tag generation for
		// it forever (#1358). The conversation therefore appears in the list
		// below once it has a first message rather than immediately, which also
		// stops an abandoned one leaving a permanent empty row.
		await goto(`/?project=${encodeURIComponent(id)}`);
	};

	const openLinkPicker = async () => {
		try {
			recentChats = await getChatList(token, 1);
		} catch {
			recentChats = [];
		}
		showLinkPicker = true;
	};

	const linkChat = async (chatId: string) => {
		if (busy) return;
		busy = true;
		error = '';
		try {
			await bindChatToProject(token, chatId, id);
			conversations = await resolveProjectConversations(token, id);
			linkedIds = new Set(conversations.map((c) => c.id));
			showLinkPicker = false;
		} catch (err) {
			error =
				err instanceof ProjectError
					? err.message
					: $i18n.t('The conversation could not be linked.');
		} finally {
			busy = false;
		}
	};

	const unlinkChat = async (chatId: string) => {
		if (busy) return;
		busy = true;
		try {
			await bindChatToProject(token, chatId, null);
			conversations = await resolveProjectConversations(token, id);
			linkedIds = new Set(conversations.map((c) => c.id));
		} catch (err) {
			error =
				err instanceof ProjectError
					? err.message
					: $i18n.t('The conversation could not be removed.');
		} finally {
			busy = false;
		}
	};
</script>

<svelte:head>
	<title>{project ? `${project.name}` : $i18n.t('Projects')} • {$WEBUI_NAME}</title>
</svelte:head>

<div class="w-full flex flex-col px-4 md:px-10 py-6 gap-6 max-w-5xl mx-auto">
	<nav class="text-xs text-gray-400" aria-label={$i18n.t('Breadcrumb')}>
		<a href="/projects" class="hover:text-gray-600 dark:hover:text-gray-300 transition">{$i18n.t('Projects')}</a>
		<span class="mx-1">/</span>
		<span class="text-gray-600 dark:text-gray-300">{project?.name ?? ''}</span>
	</nav>

	{#if error}
		<p role="alert" class="text-sm text-red-500">{error}</p>
	{/if}

	{#if loading}
		<p class="text-sm text-gray-500" role="status">{$i18n.t('Loading...')}</p>
	{:else if !project}
		<p class="text-sm text-gray-500">{$i18n.t('This project does not exist.')}</p>
	{:else}
		<header class="flex items-start justify-between gap-3">
			<div class="min-w-0">
				<h1 class="hv-display text-2xl text-gray-850 dark:text-gray-100 truncate">{project.name}</h1>
				<p class="mt-1 text-sm text-gray-500">{project.description}</p>
				<p class="mt-1 text-[11px] text-gray-400">
					{$i18n.t('Updated')} {relativeDate(project.updatedAt)}
				</p>
			</div>
			{#if project.writeAccess}
				<div class="flex gap-2 flex-none">
					<Tooltip content={$i18n.t('Rename project')} placement="bottom">
						<button
							id="project-edit-button"
							class="px-3 py-2 text-xs font-medium rounded-lg border border-gray-200 dark:border-gray-800 hover:bg-gray-50 dark:hover:bg-gray-900 transition"
							on:click={openEdit}
						>
							{$i18n.t('Rename')}
						</button>
					</Tooltip>
					<Tooltip content={$i18n.t('Delete project')} placement="bottom">
						<button
							id="project-delete-button"
							class="px-3 py-2 text-xs font-medium rounded-lg border border-red-200 dark:border-red-900 text-red-500 hover:bg-red-50 dark:hover:bg-red-950 transition"
							on:click={confirmDelete}
						>
							{$i18n.t('Delete')}
						</button>
					</Tooltip>
				</div>
			{/if}
		</header>

		<!-- Files bound to this project's scope -->
		<section class="flex flex-col gap-2" aria-labelledby="project-files-heading">
			<div class="flex items-center justify-between gap-2">
				<h2 id="project-files-heading" class="text-sm font-semibold text-gray-700 dark:text-gray-200">
					{$i18n.t('Files')}
				</h2>
				{#if project.writeAccess}
					<label class="cursor-pointer px-3 py-1.5 text-xs font-medium rounded-lg border border-gray-200 dark:border-gray-800 hover:bg-gray-50 dark:hover:bg-gray-900 transition">
						{$i18n.t('Add files')}
						<input type="file" multiple class="hidden" on:change={onFilesChosen} disabled={busy} />
					</label>
				{/if}
			</div>
			{#if project.files.length === 0}
				<p class="text-xs text-gray-400">
					{$i18n.t('No files yet. Files here are given to every conversation in this project.')}
				</p>
			{:else}
				<ul class="flex flex-col divide-y divide-gray-100 dark:divide-gray-800 rounded-xl border border-gray-200 dark:border-gray-800">
					{#each project.files as file (file.id)}
						<li class="flex items-center justify-between gap-3 px-4 py-2.5">
							<span class="text-sm text-gray-700 dark:text-gray-200 truncate">{file.name}</span>
							{#if project.writeAccess}
								<button
									class="text-xs text-gray-400 hover:text-red-500 transition flex-none"
									on:click={() => detachFile(file.id)}
								>
									{$i18n.t('Remove')}
								</button>
							{/if}
						</li>
					{/each}
				</ul>
			{/if}
		</section>

		<!-- Conversations bound to this project -->
		<section class="flex flex-col gap-2" aria-labelledby="project-conversations-heading">
			<div class="flex items-center justify-between gap-2">
				<h2 id="project-conversations-heading" class="text-sm font-semibold text-gray-700 dark:text-gray-200">
					{$i18n.t('Conversations')}
				</h2>
				<div class="flex gap-2">
					<button
						id="project-link-chat-button"
						class="px-3 py-1.5 text-xs font-medium rounded-lg border border-gray-200 dark:border-gray-800 hover:bg-gray-50 dark:hover:bg-gray-900 transition"
						on:click={openLinkPicker}
					>
						{$i18n.t('Link existing')}
					</button>
					<button
						id="project-new-chat-button"
						class="px-3 py-1.5 text-xs font-medium rounded-lg bg-black text-white dark:bg-white dark:text-black hover:opacity-80 transition"
						on:click={startNewChat}
					>
						{$i18n.t('New chat')}
					</button>
				</div>
			</div>
			{#if conversations.length === 0}
				<p class="text-xs text-gray-400">
					{$i18n.t('No conversations yet. New chats started here belong to this project.')}
				</p>
			{:else}
				<ul class="flex flex-col divide-y divide-gray-100 dark:divide-gray-800 rounded-xl border border-gray-200 dark:border-gray-800">
					{#each conversations as convo (convo.id)}
						<li class="flex items-center justify-between gap-3 px-4 py-2.5">
							<a href={`/c/${convo.id}`} class="text-sm text-gray-700 dark:text-gray-200 truncate hover:underline min-w-0">
								{convo.title}
							</a>
							<button
								class="text-xs text-gray-400 hover:text-red-500 transition flex-none"
								on:click={() => unlinkChat(convo.id)}
							>
								{$i18n.t('Unlink')}
							</button>
						</li>
					{/each}
				</ul>
			{/if}
		</section>
	{/if}
</div>

{#if showEdit && project}
	<div class="fixed inset-0 z-50 flex items-center justify-center bg-black/40" role="dialog" aria-modal="true">
		<button class="absolute inset-0 cursor-default" aria-label={$i18n.t('Close')} on:click={() => (showEdit = false)} />
		<form
			class="relative w-full max-w-md mx-4 p-5 rounded-2xl bg-white dark:bg-gray-900 border border-gray-200 dark:border-gray-800 shadow-xl flex flex-col gap-3"
			on:submit|preventDefault={submitEdit}
		>
			<h2 class="text-base font-semibold text-gray-850 dark:text-gray-100">{$i18n.t('Rename')}</h2>
			<label class="flex flex-col gap-1">
				<span class="text-xs font-medium text-gray-500">{$i18n.t('Name')}</span>
				<input
					id="projects-rename-name"
					class="px-3 py-2 text-sm rounded-lg bg-gray-50 dark:bg-gray-800 border border-gray-200 dark:border-gray-700 outline-none focus:border-gray-400 transition"
					bind:value={editName}
					maxlength={255}
					required
				/>
			</label>
			<label class="flex flex-col gap-1">
				<span class="text-xs font-medium text-gray-500">{$i18n.t('Description')}</span>
				<textarea
					id="projects-rename-description"
					class="px-3 py-2 text-sm rounded-lg bg-gray-50 dark:bg-gray-800 border border-gray-200 dark:border-gray-700 outline-none focus:border-gray-400 transition resize-y"
					bind:value={editDescription}
					rows="3"
					maxlength={1000}
				/>
			</label>
			<div class="flex justify-end gap-2 mt-1">
				<button
					type="button"
					class="px-3 py-2 text-sm rounded-lg hover:bg-gray-100 dark:hover:bg-gray-800 transition"
					on:click={() => (showEdit = false)}
				>
					{$i18n.t('Cancel')}
				</button>
				<button
					type="submit"
					disabled={!editName.trim() || busy}
					class="px-3 py-2 text-sm font-medium rounded-lg bg-black text-white dark:bg-white dark:text-black disabled:opacity-40 hover:opacity-80 transition"
				>
					{$i18n.t('Save')}
				</button>
			</div>
		</form>
	</div>
{/if}

{#if showLinkPicker}
	<div class="fixed inset-0 z-50 flex items-center justify-center bg-black/40" role="dialog" aria-modal="true">
		<button class="absolute inset-0 cursor-default" aria-label={$i18n.t('Close')} on:click={() => (showLinkPicker = false)} />
		<div class="relative w-full max-w-md mx-4 max-h-[70vh] overflow-y-auto p-5 rounded-2xl bg-white dark:bg-gray-900 border border-gray-200 dark:border-gray-800 shadow-xl flex flex-col gap-2">
			<h2 class="text-base font-semibold text-gray-850 dark:text-gray-100">
				{$i18n.t('Link a conversation')}
			</h2>
			<p class="text-xs text-gray-400">{$i18n.t('Pick from your recent conversations.')}</p>
			{#if recentChats.length === 0}
				<p class="text-sm text-gray-500 py-4">{$i18n.t('No recent conversations')}</p>
			{:else}
				<ul class="flex flex-col divide-y divide-gray-100 dark:divide-gray-800">
					{#each recentChats as chat (chat.id)}
						<li class="flex items-center justify-between gap-3 py-2">
							<span class="text-sm text-gray-700 dark:text-gray-200 truncate min-w-0">{chat.title}</span>
							{#if linkedIds.has(chat.id)}
								<span class="text-[11px] text-gray-400 flex-none">{$i18n.t('Linked')}</span>
							{:else}
								<button
									class="text-xs font-medium text-gray-600 dark:text-gray-300 hover:underline flex-none"
									on:click={() => linkChat(chat.id)}
								>
									{$i18n.t('Link')}
								</button>
							{/if}
						</li>
					{/each}
				</ul>
			{/if}
		</div>
	</div>
{/if}
